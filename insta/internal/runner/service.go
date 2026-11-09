package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/state"
)

type ServiceStatus struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid"`
	Token   string `json:"token,omitempty"`
}
type controlRequest struct {
	Token  string `json:"token"`
	Action string `json:"action"`
}

type completionReceipt struct {
	Token string `json:"token"`
}

// Complete proves that this exact supervisor owner finished cleanup. A missing
// control socket alone does not establish that its workers have stopped.
func Complete(s *state.Service) bool {
	if s == nil || s.Socket == "" || s.Token == "" {
		return false
	}
	name := s.Socket + ".done"
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		return false
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return false
	}
	var receipt completionReceipt
	return json.Unmarshal(b, &receipt) == nil && receipt.Token == s.Token
}

func completeService(s *state.Service) error {
	if s == nil || s.Socket == "" || s.Token == "" {
		return errors.New("cannot record completion without service ownership")
	}
	return state.WriteJSON(s.Socket+".done", completionReceipt{Token: s.Token})
}

// Control authenticates an owned supervisor over its private Unix socket. No
// lifecycle operation kills a PID loaded from a state file.
func Control(ctx context.Context, s *state.Service, action string) (ServiceStatus, error) {
	if s == nil || s.Socket == "" || s.Token == "" {
		return ServiceStatus{}, errors.New("service ownership record is incomplete")
	}
	if action != "status" && action != "stop" {
		return ServiceStatus{}, errors.New("unsupported service control action")
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	c, e := d.DialContext(ctx, "unix", s.Socket)
	if e != nil {
		return ServiceStatus{}, e
	}
	defer c.Close()
	deadline := time.Now().Add(3 * time.Second)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	c.SetDeadline(deadline)
	stopCancel := context.AfterFunc(ctx, func() { c.Close() })
	defer stopCancel()
	if e = json.NewEncoder(c).Encode(controlRequest{Token: s.Token, Action: action}); e != nil {
		if ctx.Err() != nil {
			return ServiceStatus{}, ctx.Err()
		}
		return ServiceStatus{}, e
	}
	var result ServiceStatus
	if e = json.NewDecoder(io.LimitReader(c, 4096)).Decode(&result); e != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, e
	}
	if result.Token != s.Token {
		return ServiceStatus{}, errors.New("service ownership token mismatch; refusing process operation")
	}
	return result, nil
}
func CheckProbe(ctx context.Context, p manifest.Probe) error {
	u, e := probeURL(p.URL)
	if e != nil {
		return e
	}
	method := p.Method
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return errors.New("service probe must use GET or POST")
	}
	req, e := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(p.Body))
	if e != nil {
		return e
	}
	if p.Body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("service probe failed at %s", p.URL)
	}
	defer resp.Body.Close()
	expected := p.Status
	if expected == 0 {
		expected = 200
	}
	if resp.StatusCode != expected {
		return fmt.Errorf("service probe returned HTTP %d at %s; expected %d", resp.StatusCode, p.URL, expected)
	}
	return nil
}

func probeURL(value string) (*url.URL, error) {
	u, e := url.Parse(value)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("service probe requires a loopback HTTP address without credentials")
	}
	ip := net.ParseIP(u.Hostname())
	port, e := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || e != nil || port < 1 || port > 65535 {
		return nil, errors.New("service probe requires an explicit loopback IP and port")
	}
	return u, nil
}

func LaunchService(ctx context.Context, executable, root, stack, name string, def manifest.Service, env []string, record *state.Service) (err error) {
	if record == nil || record.Socket == "" || record.Token == "" {
		return errors.New("service launch needs an ownership record")
	}
	launched := false
	// A pre-spawn failure has no worker to clean up and can be retried safely.
	defer func() {
		if !launched {
			err = errors.Join(err, completeService(record))
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	// A listening port may belong to another project. Refuse it before spawn.
	u, e := probeURL(def.Probe.URL)
	if e != nil {
		return e
	}
	l, e := net.Listen("tcp", u.Host)
	if e != nil {
		return fmt.Errorf("service port %s is already occupied; insta will not adopt or stop that process", u.Host)
	}
	l.Close()
	if e = os.MkdirAll(filepath.Dir(record.Socket), 0700); e != nil {
		return e
	}
	if len(record.Socket) > 100 {
		return errors.New("project path is too long for a Unix service socket; create the project under a shorter directory")
	}
	cmd := exec.Command(executable, "__serve", root, stack, name, record.Token, record.Socket, record.Log)
	cmd.Dir = root
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Supervisor writes only redacted output to its private log.
	if e = cmd.Start(); e != nil {
		return fmt.Errorf("start service supervisor: %w", e)
	}
	record.PID = cmd.Process.Pid
	launched = true
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return fmt.Errorf("service %s exited during startup; inspect %s", name, record.Log)
		case <-deadline.C:
			return fmt.Errorf("service %s did not become ready; inspect %s and retry insta up or insta task start", name, record.Log)
		case <-tick.C:
			status, e := Control(ctx, record, "status")
			if e != nil || !status.Running {
				continue
			}
			if e = CheckProbe(ctx, def.Probe); e == nil {
				return nil
			}
		}
	}
}

func StopService(ctx context.Context, s *state.Service) error {
	_, e := Control(ctx, s, "stop")
	if e != nil {
		if errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ECONNREFUSED) {
			if Complete(s) {
				return nil
			}
			return errors.New("service control is unavailable and cleanup is unconfirmed; ownership record retained")
		}
		return e
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("service supervisor did not finish stopping; ownership entry retained")
		case <-tick.C:
			if Complete(s) {
				return nil
			}
			_, e := Control(ctx, s, "status")
			if (errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ECONNREFUSED)) && Complete(s) {
				return nil
			}
		}
	}
}

// Serve is an internal child entry point, deliberately absent from CLI help.
// The supervisor owns a live exec.Cmd and its process group, and exits with it.
func Serve(ctx context.Context, root, stack, name, token, socket, logPath string) (resultErr error) {
	if len(token) != 32 {
		return errors.New("invalid service token")
	}
	expectedPrefix := filepath.Join(root, ".insta", "run") + string(os.PathSeparator)
	if filepath.Clean(socket) != socket || filepath.Clean(logPath) != logPath || !strings.HasPrefix(socket, expectedPrefix) || !strings.HasPrefix(logPath, filepath.Join(root, ".insta", "logs")+string(os.PathSeparator)) {
		return errors.New("invalid service control or log path")
	}
	completionManaged := false
	defer func() {
		if !completionManaged {
			resultErr = errors.Join(resultErr, completeService(&state.Service{Socket: socket, Token: token}))
		}
	}()
	if e := os.Remove(socket + ".done"); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	m, _, e := manifest.Load(root)
	if e != nil {
		return e
	}
	def, ok := m.Services[name]
	if !ok {
		return errors.New("unknown service")
	}
	if e = os.MkdirAll(filepath.Dir(logPath), 0700); e != nil {
		return e
	}
	log, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	redactor := NewRedactor(nil)
	writer := redactor.Writer(log)
	defer writer.Flush()
	listener, e := net.Listen("unix", socket)
	if e != nil {
		return e
	}
	completionManaged = true
	os.Chmod(socket, 0600)
	var child *exec.Cmd
	defer func() {
		clean := true
		if child != nil && child.Process != nil {
			killErr := syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
			clean = killErr == nil || errors.Is(killErr, syscall.ESRCH)
			if !clean {
				resultErr = errors.Join(resultErr, errors.New("could not confirm service descendant cleanup"))
			}
		}
		// UnixListener.Close removes its own socket before publishing completion.
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			clean = false
			resultErr = errors.Join(resultErr, closeErr)
		}
		if clean {
			resultErr = errors.Join(resultErr, completeService(&state.Service{Socket: socket, Token: token}))
		}
	}()
	cwd, e := manifest.Path(root, def.Cwd, true)
	if e != nil {
		return e
	}
	args := Args(root, stack, def.Command)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	if e = cmd.Start(); e != nil {
		return e
	}
	child = cmd
	var running atomic.Bool
	running.Store(true)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); running.Store(false) }()
	stop := make(chan struct{}, 1)
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				var req controlRequest
				if json.NewDecoder(io.LimitReader(c, 4096)).Decode(&req) != nil || req.Token != token {
					return
				}
				if req.Action != "status" && req.Action != "stop" {
					return
				}
				json.NewEncoder(c).Encode(ServiceStatus{Running: running.Load(), PID: cmd.Process.Pid, Token: token})
				if req.Action == "stop" {
					select {
					case stop <- struct{}{}:
					default:
					}
				}
			}()
		}
	}()
	sigCtx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	select {
	case e = <-done:
		return e
	case <-stop:
	case <-sigCtx.Done():
	}
	if running.Load() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	select {
	case e = <-done:
		return e
	case <-time.After(8 * time.Second):
		if running.Load() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return <-done
	}
}
