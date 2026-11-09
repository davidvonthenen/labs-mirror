package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/state"
	"gopkg.in/yaml.v3"
)

func serviceTemp(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "insta-service-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestProbeRequiresLoopbackAndExpectedStatus(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:9000@remote.example:8000/health", "http://remote.example:8000/health", "http://localhost:8000/health", "http://127.0.0.1/health", "http://127.0.0.1:0/health", "https://127.0.0.1:8000/health"} {
		if _, err := probeURL(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Accept") != "application/json, text/event-stream" {
			t.Error("protocol probe did not preserve headers/method")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	probe := manifest.Probe{URL: server.URL, Method: "POST", Body: `{"jsonrpc":"2.0"}`, Status: 202}
	if err := CheckProbe(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	probe.Status = 200
	if err := CheckProbe(context.Background(), probe); err == nil {
		t.Fatal("unexpected readiness status accepted")
	}
}

func TestCompletionReceiptRequiresExactOwner(t *testing.T) {
	root := serviceTemp(t)
	s := &state.Service{Token: strings.Repeat("a", 32), Socket: filepath.Join(root, "absent.sock"), PID: os.Getpid()}
	if Complete(s) {
		t.Fatal("missing receipt counted as completion")
	}
	if err := StopService(context.Background(), s); err == nil {
		t.Fatal("unknown stale process was discarded")
	}
	if err := completeService(s); err != nil {
		t.Fatal(err)
	}
	if !Complete(s) {
		t.Fatal("own receipt not recognized")
	}
	other := *s
	other.Token = strings.Repeat("b", 32)
	if Complete(&other) {
		t.Fatal("receipt was accepted by another owner")
	}
	if err := StopService(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := StopService(context.Background(), s); err != nil {
		t.Fatal("repeated cleanup not safe", err)
	}
}

func TestControlCancellationAndWrongToken(t *testing.T) {
	root := serviceTemp(t)
	socket := filepath.Join(root, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Control(ctx, &state.Service{Socket: socket, Token: "owner"}, "status")
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("control ignored cancellation: %v", err)
	}
	select {
	case conn := <-accepted:
		conn.Close()
	default:
	}
	if _, err := Control(context.Background(), nil, "stop"); err == nil {
		t.Fatal("nil ownership record accepted")
	}
	if _, err := Control(context.Background(), &state.Service{Socket: socket, Token: "owner"}, "kill"); err == nil {
		t.Fatal("undeclared control action accepted")
	}
}

func serviceFixture(t *testing.T, root, program string) *state.Service {
	t.Helper()
	for _, dir := range []string{".insta/run", ".insta/logs", "support", "tasks"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range map[string]string{"AGENTS.md": "Fixture", "support/requirements.txt": "", "tasks/example.md": "Fixture"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := &manifest.Manifest{SchemaVersion: 1, ID: "fixture", Name: "Fixture", Instructions: "AGENTS.md", Runtime: manifest.Runtime{Kind: "python", Python: "3.12", Requirements: "support/requirements.txt"}, Config: map[string]manifest.Definition{}, Platforms: map[string]manifest.Platform{}, Services: map[string]manifest.Service{"worker": {Cwd: ".", Command: []string{program}, Probe: manifest.Probe{URL: "http://127.0.0.1:9999/health"}}}, Tasks: map[string]manifest.Task{"example": {Blueprint: "tasks/example.md", Source: ".", Operations: map[string]manifest.Operation{"validate": {Command: []string{program}}}}}}
	b, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "insta.yaml"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return &state.Service{Token: strings.Repeat("a", 32), Socket: filepath.Join(root, ".insta", "run", "worker.sock"), Log: filepath.Join(root, ".insta", "logs", "worker.log"), PID: os.Getpid()}
}

func TestSupervisorOwnedStopKillsDescendantsAndWritesReceipt(t *testing.T) {
	root := serviceTemp(t)
	heartbeat := filepath.Join(root, "heartbeat")
	t.Setenv("HEARTBEAT", heartbeat)
	t.Setenv("OPENSEARCH_PASSWORD", "service-secret")
	program := runnerScript(t, root, `printf '%s' 'service-'; printf '%s\n' 'secret'
trap 'exit 0' TERM
(trap '' TERM; while :; do printf x >> "$HEARTBEAT"; /bin/sleep 0.03; done) >/dev/null 2>&1 &
wait`)
	record := serviceFixture(t, root, program)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, root, "dev", "worker", record.Token, record.Socket, record.Log) }()
	waitForHeartbeat(t, heartbeat)
	observed, err := Control(context.Background(), record, "status")
	if err != nil || !observed.Running {
		t.Fatalf("owned process absent: %+v %v", observed, err)
	}
	wrong := *record
	wrong.Token = strings.Repeat("b", 32)
	if err := StopService(context.Background(), &wrong); err == nil {
		t.Fatal("wrong token authorized a stop")
	}
	if status, err := Control(context.Background(), record, "status"); err != nil || !status.Running {
		t.Fatal("wrong owner stopped the service")
	}
	if err := StopService(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor failed to exit")
	}
	if !Complete(record) {
		t.Fatal("clean shutdown did not publish its receipt")
	}
	assertHeartbeatStopped(t, heartbeat)
	log, err := os.ReadFile(record.Log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "service-secret") || !strings.Contains(string(log), "[redacted]") {
		t.Fatalf("service output was not redacted: %q", log)
	}
	if err := StopService(context.Background(), record); err != nil {
		t.Fatal("second cleanup failed", err)
	}
}

func TestLaunchPreSpawnFailureIsRecoverableAndDoesNotAdoptPort(t *testing.T) {
	root := serviceTemp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "unrelated") }))
	defer server.Close()
	record := &state.Service{Token: strings.Repeat("a", 32), Socket: filepath.Join(root, "worker.sock"), Log: filepath.Join(root, "worker.log")}
	err := LaunchService(context.Background(), filepath.Join(root, "absent-executable"), root, "dev", "worker", manifest.Service{Probe: manifest.Probe{URL: server.URL}}, nil, record)
	if err == nil || !strings.Contains(err.Error(), "already occupied") || !Complete(record) {
		t.Fatalf("pre-spawn failure not journaled: %v", err)
	}
	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal("unrelated service was disturbed", err)
	}
	res.Body.Close()
}

func TestLaunchNoticesImmediateSupervisorExit(t *testing.T) {
	root := serviceTemp(t)
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := port.Addr().String()
	port.Close()
	executable := runnerScript(t, root, "exit 7")
	record := &state.Service{Token: strings.Repeat("a", 32), Socket: filepath.Join(root, "worker.sock"), Log: filepath.Join(root, "worker.log")}
	start := time.Now()
	err = LaunchService(context.Background(), executable, root, "dev", "worker", manifest.Service{Probe: manifest.Probe{URL: "http://" + address + "/health"}}, nil, record)
	if err == nil || !strings.Contains(err.Error(), "exited during startup") || time.Since(start) > 2*time.Second {
		t.Fatalf("startup failure waited for readiness timeout: %v", err)
	}
	if Complete(record) {
		t.Fatal("an arbitrary started executable cannot imply descendant cleanup")
	}
}

func TestReceiptFileDoesNotAcceptMalformedJSONOrSymlink(t *testing.T) {
	root := serviceTemp(t)
	record := &state.Service{Token: "owner", Socket: filepath.Join(root, "worker.sock")}
	if err := os.WriteFile(record.Socket+".done", []byte("malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	if Complete(record) {
		t.Fatal("malformed receipt accepted")
	}
	if err := os.Remove(record.Socket + ".done"); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "outside.json")
	b, _ := json.Marshal(completionReceipt{Token: record.Token})
	if err := os.WriteFile(other, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, record.Socket+".done"); err != nil {
		t.Fatal(err)
	}
	if Complete(record) {
		t.Fatal("receipt symlink accepted")
	}
	if err := completeService(nil); err == nil {
		t.Fatal("missing owner accepted")
	}
	if _, err := Control(context.Background(), &state.Service{}, "status"); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete record treated as absent")
	}
}
