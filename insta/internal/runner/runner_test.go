package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamingRedactorShowsPromptsAndHandlesEverySplit(t *testing.T) {
	secret := "fixture-key-123456"
	r := NewRedactor([]string{secret})
	for split := 0; split <= len(secret); split++ {
		var output bytes.Buffer
		writer := r.Writer(&output)
		if _, err := writer.Write([]byte("Question > ")); err != nil {
			t.Fatal(err)
		}
		if output.String() != "Question > " {
			t.Fatal("interactive prompt was buffered until newline")
		}
		writer.Write([]byte(secret[:split]))
		if strings.Contains(output.String(), secret) {
			t.Fatal("partial write leaked a key")
		}
		writer.Write([]byte(secret[split:] + " done\n"))
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
		if got := output.String(); got != "Question > [redacted] done\n" {
			t.Fatalf("split %d: %q", split, got)
		}
	}
}

func TestStreamingRedactorLongestMatchBoundedSuffixAndRefresh(t *testing.T) {
	r := NewRedactor([]string{"token-123", "token-123456", ""})
	var output bytes.Buffer
	w := r.Writer(&output)
	for _, piece := range []string{"before tok", "en-123", "456 aft", "er"} {
		w.Write([]byte(piece))
		if len(w.pending) >= len("token-123456") {
			t.Fatal("retained more than a possible secret prefix")
		}
	}
	w.Flush()
	if output.String() != "before [redacted] after" {
		t.Fatalf("longest match failed: %q", output.String())
	}
	newer := r.WithSecrets([]string{"rotated-key"})
	if newer.Text("token-123456 rotated-key") != "[redacted] [redacted]" {
		t.Fatal("refresh forgot an earlier credential")
	}
	if r.Text("rotated-key") != "rotated-key" {
		t.Fatal("redactor was mutated in place")
	}
}

func TestSafeWriterConcurrentWrites(t *testing.T) {
	var output bytes.Buffer
	w := NewRedactor([]string{"fixture-key"}).Writer(&output)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := w.Write([]byte("fixture-key\n")); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "[redacted]\n") != 200 || strings.Contains(output.String(), "fixture-key") {
		t.Fatal("concurrent writes lost data or exposed secrets")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }
func TestSafeWriterReportsShortWrite(t *testing.T) {
	w := NewRedactor(nil).Writer(shortWriter{})
	if _, err := w.Write([]byte("output")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write hidden: %v", err)
	}
}

func runnerScript(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "fixture.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunUsesArgvCwdEnvironmentAndReportsExit(t *testing.T) {
	root := t.TempDir()
	program := runnerScript(t, root, `printf '%s\n' "$PWD" "$1" "$FIXTURE_KEY"
exit 17`)
	var output bytes.Buffer
	result, err := Run(context.Background(), Request{Argv: []string{program, "$(touch injected)"}, Cwd: root, Env: []string{"FIXTURE_KEY=fixture-key"}, Output: &output, Redactor: NewRedactor([]string{"fixture-key"})})
	if err == nil || result.ExitCode != 17 {
		t.Fatalf("exit status lost: %+v %v", result, err)
	}
	if !strings.Contains(output.String(), root) || !strings.Contains(output.String(), "$(touch injected)") || strings.Contains(output.String(), "fixture-key") {
		t.Fatalf("incorrect context/redaction: %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "injected")); !os.IsNotExist(err) {
		t.Fatal("argv interpreted as shell syntax")
	}
}

func waitForHeartbeat(t *testing.T, filename string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(filename); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture child did not start")
}

func assertHeartbeatStopped(t *testing.T, filename string) {
	t.Helper()
	// A queued filesystem write can finish while SIGKILL is delivered.
	time.Sleep(60 * time.Millisecond)
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("owned background descendant survived command completion")
	}
}

func TestRunCancellationStopsTermIgnoringDescendants(t *testing.T) {
	root := t.TempDir()
	heartbeat := filepath.Join(root, "heartbeat")
	program := runnerScript(t, root, `trap 'exit 0' TERM
(trap '' TERM; while :; do printf x >> "$HEARTBEAT"; /bin/sleep 0.03; done) >/dev/null 2>&1 &
wait`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Request{Argv: []string{program}, Cwd: root, Env: []string{"HEARTBEAT=" + heartbeat}, Output: io.Discard})
		done <- err
	}()
	waitForHeartbeat(t, heartbeat)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation hidden: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled task did not stop")
	}
	assertHeartbeatStopped(t, heartbeat)
}

func TestRunCompletionStopsDetachedDescendants(t *testing.T) {
	root := t.TempDir()
	heartbeat := filepath.Join(root, "heartbeat")
	program := runnerScript(t, root, `(trap '' TERM; while :; do printf x >> "$HEARTBEAT"; /bin/sleep 0.03; done) >/dev/null 2>&1 &
while [ ! -s "$HEARTBEAT" ]; do /bin/sleep 0.01; done
exit 0`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := Run(ctx, Request{Argv: []string{program}, Cwd: root, Env: []string{"HEARTBEAT=" + heartbeat}, Output: io.Discard})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("fixture command failed: %+v %v", result, err)
	}
	assertHeartbeatStopped(t, heartbeat)
}
