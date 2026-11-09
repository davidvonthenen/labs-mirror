// Package runner executes declared argv without a shell and redacts managed output.
package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/instaclustr/labs/insta/internal/manifest"
)

type Redactor struct{ secrets []string }

func NewRedactor(secrets []string) *Redactor {
	copySecrets := append([]string{}, secrets...)
	for _, s := range os.Environ() {
		k, v, _ := strings.Cut(s, "=")
		u := strings.ToUpper(k)
		if strings.Contains(u, "PASSWORD") || strings.Contains(u, "SECRET") || strings.HasSuffix(u, "_TOKEN") || strings.HasSuffix(u, "_API_KEY") || u == "OPENSEARCH_PASS" {
			copySecrets = append(copySecrets, v)
		}
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(copySecrets))
	for _, secret := range copySecrets {
		if secret != "" && !seen[secret] {
			seen[secret] = true
			unique = append(unique, secret)
		}
	}
	sort.Slice(unique, func(i, j int) bool { return len(unique[i]) > len(unique[j]) })
	return &Redactor{secrets: unique}
}

// WithSecrets keeps earlier observed credentials redacted after configuration is
// refreshed. Redactors are immutable, so existing writers remain race-safe.
func (r *Redactor) WithSecrets(secrets []string) *Redactor {
	all := append([]string{}, secrets...)
	if r != nil {
		all = append(all, r.secrets...)
	}
	return NewRedactor(all)
}
func (r *Redactor) Text(s string) string { out, _ := r.chunk([]byte(s), true); return string(out) }

// chunk emits text that cannot be part of a credential and retains only an
// incomplete possible secret prefix. Full matches use the longest known value.
// This supports interactive prompts without waiting for a newline.
func (r *Redactor) chunk(input []byte, final bool) ([]byte, []byte) {
	var out bytes.Buffer
	for i := 0; i < len(input); {
		remaining := input[i:]
		match := 0
		incomplete := false
		for _, secret := range r.secrets {
			if len(remaining) < len(secret) {
				if bytes.Equal(remaining, []byte(secret[:len(remaining)])) {
					incomplete = true
				}
			} else if match == 0 && bytes.Equal(remaining[:len(secret)], []byte(secret)) {
				match = len(secret)
			}
		}
		if incomplete && !final {
			return out.Bytes(), append([]byte(nil), remaining...)
		}
		if match > 0 {
			out.WriteString("[redacted]")
			i += match
			continue
		}
		out.WriteByte(input[i])
		i++
	}
	return out.Bytes(), nil
}

type SafeWriter struct {
	mu       sync.Mutex
	target   io.Writer
	redactor *Redactor
	pending  []byte
}

func (r *Redactor) Writer(w io.Writer) *SafeWriter { return &SafeWriter{target: w, redactor: r} }
func (w *SafeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	output, pending := w.redactor.chunk(w.pending, false)
	w.pending = pending
	if len(output) > 0 {
		n, e := w.target.Write(output)
		if e != nil {
			return 0, e
		}
		if n != len(output) {
			return 0, io.ErrShortWrite
		}
	}
	return len(p), nil
}
func (w *SafeWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	output, _ := w.redactor.chunk(w.pending, true)
	w.pending = nil
	if len(output) == 0 {
		return nil
	}
	n, e := w.target.Write(output)
	if e == nil && n != len(output) {
		e = io.ErrShortWrite
	}
	return e
}

type Result struct {
	ExitCode   int   `json:"exit_code"`
	DurationMS int64 `json:"duration_ms"`
}
type Request struct {
	Argv        []string
	Cwd         string
	Env         []string
	Stdin       io.Reader
	Output      io.Writer
	Redactor    *Redactor
	Interactive bool
}

func Run(ctx context.Context, r Request) (Result, error) {
	if len(r.Argv) == 0 {
		return Result{ExitCode: -1}, errors.New("empty command")
	}
	if r.Redactor == nil {
		r.Redactor = NewRedactor(nil)
	}
	if r.Output == nil {
		r.Output = io.Discard
	}
	cmd := exec.CommandContext(ctx, r.Argv[0], r.Argv[1:]...)
	cmd.Dir = r.Cwd
	cmd.Env = r.Env
	cmd.Stdin = r.Stdin
	w := r.Redactor.Writer(r.Output)
	defer w.Flush()
	cmd.Stdout = w
	cmd.Stderr = w
	// Noninteractive descendants belong to this command's process group.
	if !r.Interactive {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		cmd.WaitDelay = 5 * time.Second
	}
	started := time.Now()
	e := cmd.Start()
	if e == nil {
		// A one-shot operation cannot leave background descendants behind. The
		// group is owned by this live exec.Cmd, never a PID recovered from state.
		// This also catches descendants that ignore TERM or close their streams.
		if !r.Interactive {
			defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		e = cmd.Wait()
	}
	result := Result{DurationMS: time.Since(started).Milliseconds()}
	if e != nil {
		result.ExitCode = -1
		var ee *exec.ExitError
		if errors.As(e, &ee) {
			result.ExitCode = ee.ExitCode()
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("command failed (exit %d)", result.ExitCode)
	}
	return result, nil
}

func Venv(root, stack string) string   { return filepath.Join(root, ".insta", "venvs", stack) }
func Python(root, stack string) string { return filepath.Join(Venv(root, stack), "bin", "python") }
func Args(root, stack string, args []string) []string {
	out := append([]string{}, args...)
	if len(out) > 0 && (out[0] == "python" || out[0] == "python3") {
		out[0] = Python(root, stack)
	}
	return out
}

func RuntimeDigest(root string, m *manifest.Manifest) (string, error) {
	h := sha256.New()
	p, e := manifest.Path(root, m.Runtime.Requirements, false)
	if e != nil {
		return "", e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	h.Write(b)
	fmt.Fprintf(h, "%s\x00%v", m.Runtime.Python, m.Runtime.Setup)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Prepare creates the venv and installs declared prerequisites. An interrupted
// preparation has no success marker; the next up retries these same steps.
func Prepare(ctx context.Context, root, stack string, m *manifest.Manifest, env []string, output io.Writer, redactor *Redactor) error {
	py := Python(root, stack)
	if _, e := os.Stat(py); errors.Is(e, os.ErrNotExist) {
		var interpreter string
		for _, name := range []string{"python3.12", "python3", "python"} {
			path, e := exec.LookPath(name)
			if e != nil {
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			cmd := exec.CommandContext(probeCtx, path, "-c", "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
			b, e := cmd.Output()
			cancel()
			if e == nil && strings.TrimSpace(string(b)) == m.Runtime.Python {
				interpreter = path
				break
			}
		}
		if interpreter == "" {
			return errors.New("Python 3.12 with venv is required; install Python 3.12, then run insta up to resume managed setup")
		}
		if e := os.MkdirAll(filepath.Dir(Venv(root, stack)), 0700); e != nil {
			return e
		}
		if _, e := Run(ctx, Request{Argv: []string{interpreter, "-m", "venv", Venv(root, stack)}, Cwd: root, Env: env, Output: output, Redactor: redactor}); e != nil {
			return fmt.Errorf("create Python environment: %w", e)
		}
	}
	steps := [][]string{{py, "-m", "pip", "install", "-r", filepath.Join(root, m.Runtime.Requirements)}}
	for _, cmd := range m.Runtime.Setup {
		steps = append(steps, Args(root, stack, cmd))
	}
	for _, args := range steps {
		if _, e := Run(ctx, Request{Argv: args, Cwd: root, Env: env, Output: output, Redactor: redactor}); e != nil {
			return fmt.Errorf("runtime setup failed; run insta up to retry: %w", e)
		}
	}
	return nil
}
