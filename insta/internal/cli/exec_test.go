package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/instaclustr/labs/insta/internal/manifest"
)

// Exercise Cobra's actual flag parsing and project discovery from the lab root.
func TestExecModes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		input  string
		want   []string
		native bool
		single bool
	}{
		{"loop", []string{"--task", "example", "--profile", "lab"}, "\nfirst\nsecond\n /quit \nnot sent\n", []string{"first", "second"}, false, false},
		{"one-shot", []string{"-p", "--leading-flag\n$HOME", "--task", "example", "--profile", "lab"}, "", []string{"--leading-flag\n$HOME"}, false, true},
		{"interactive", []string{"-i", "--task", "example", "--profile", "lab"}, "", []string{""}, true, false},
		{"interactive-instruction", []string{"-i", "--task", "example", "--profile", "lab", "start"}, "", []string{"start"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := agentFixture(t)
			t.Chdir(s.Root)
			t.Setenv("INSTA_AGENT_BACKEND", "codex")
			a := s.App
			a.In = strings.NewReader(tc.input)
			f := &agentFakeCoding{readiness: coding.Readiness{State: coding.Ready, Configured: true}}
			calls := 0
			check := func(req coding.SessionRequest) error {
				if calls >= len(tc.want) || req.Instruction != tc.want[calls] || req.Profile != "lab" || req.TaskID != "example" || req.OneShot != tc.single || req.TaskApprovalScope != "" {
					t.Fatalf("call %d unexpected request %+v", calls, req)
				}
				calls++
				return nil
			}
			f.check = func(req coding.SessionRequest) error {
				if !tc.native || req.Stdin != a.In {
					t.Fatal("unexpected native launch or detached stdin")
				}
				return check(req)
			}
			f.prompt = func(_ context.Context, req coding.SessionRequest, thread string) (string, error) {
				if tc.native || req.Stdin != nil || (calls == 0 && thread != "") || (calls != 0 && thread != "my-thread") {
					t.Fatal("unexpected wrapped mode, stdin, or thread", thread)
				}
				fmt.Fprintln(req.Stdout, "answer")
				return "my-thread", check(req)
			}
			a.CodingCLI = f
			if code := a.Execute(context.Background(), append([]string{"exec"}, tc.args...)); code != 0 || calls != len(tc.want) {
				t.Fatalf("code=%d calls=%d stderr=%s", code, calls, a.Err)
			}
			if !tc.native && a.Out.(*bytes.Buffer).String() != strings.Repeat("answer\n", calls) {
				t.Fatalf("unexpected stdout: %q", a.Out)
			}
		})
	}
}

func TestExecApproveNestedTask(t *testing.T) {
	s := agentFixture(t)
	t.Chdir(s.Root)
	t.Setenv("INSTA_AGENT_BACKEND", "codex")
	t.Setenv(coding.TaskApprovalEnv, "")
	task := s.Manifest.Tasks["example"]
	task.Operations["query"] = manifest.Operation{
		Command: []string{"python", "-m", "src.query"},
		Effects: []string{"index-read", "remote-model-call", "model-cache-write"},
	}
	taskSealFixture(t, s, "printf 'query ran\\n'\n")
	var scope string
	for _, args := range [][]string{{"exec", "--approve", "-i"}, {"exec", "-p", "query", "--approve"}, {"exec", "--approve"}} {
		check := func(req coding.SessionRequest) error {
			if req.TaskApprovalScope == "" || (scope != "" && req.TaskApprovalScope != scope) {
				t.Fatal("missing or inconsistent scope", args)
			}
			scope = req.TaskApprovalScope
			return nil
		}
		s.App.CodingCLI = &agentFakeCoding{
			readiness: coding.Readiness{State: coding.Ready, Configured: true}, check: check,
			prompt: func(_ context.Context, req coding.SessionRequest, _ string) (string, error) {
				return "thread", check(req)
			},
		}
		s.App.In = strings.NewReader("query\n/quit\n")
		if code := s.App.Execute(context.Background(), args); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, s.App.Err)
		}
	}
	// Model shell invocations get a fresh CLI with no TTY and no --yes.
	for _, tc := range []struct {
		name, inherited string
		code            int
	}{
		{"ordinary", "", 2},
		{"different lab", "other-scope", 2},
		{"same approved session", scope, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(coding.TaskApprovalEnv, tc.inherited)
			var out, diagnostic bytes.Buffer
			child := New(strings.NewReader(""), &out, &diagnostic)
			code := child.Execute(context.Background(), []string{"task", "run", "example", "query"})
			if code != tc.code || strings.Contains(out.String(), "query ran") != (tc.code == 0) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, &out, &diagnostic)
			}
			if !strings.Contains(diagnostic.String(), "remote-model-call") || strings.Contains(diagnostic.String(), "preapproved") != (tc.code == 0) {
				t.Fatalf("missing effects or wrong confirmation: %s", &diagnostic)
			}
		})
	}

	// Even a grant matching the current files cannot approve a manifest review.
	s.Project.Stack().ApprovedManifest = "earlier-manifest"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(coding.TaskApprovalEnv, scope)
	var diagnostic bytes.Buffer
	child := New(strings.NewReader(""), io.Discard, &diagnostic)
	code := child.Execute(context.Background(), []string{"task", "run", "example", "query"})
	if code != 2 || !strings.Contains(diagnostic.String(), "manifest or deployment request changed") {
		t.Fatalf("manifest review skipped: %d %s", code, &diagnostic)
	}
}

func TestTaskApprovalScopeIdentity(t *testing.T) {
	s := agentFixture(t)
	s.Hash = "manifest-digest"
	want, err := s.taskApprovalScope()
	if err != nil {
		t.Fatal(err)
	}
	check := func(root, projectID, stack, digest string, match bool) {
		t.Helper()
		copy := *s
		project := *s.Project
		project.ID, project.SelectedStack = projectID, stack
		copy.Root, copy.Project, copy.Hash = root, &project, digest
		got, err := copy.taskApprovalScope()
		if err != nil || (got == want) != match {
			t.Fatalf("unexpected approval scope for %q %q %q %q: %v", root, projectID, stack, digest, err)
		}
	}
	id, stack := s.Project.ID, s.Project.SelectedStack
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(s.Root, alias); err != nil {
		t.Fatal(err)
	}
	check(alias, id, stack, s.Hash, true)
	check(t.TempDir(), id, stack, s.Hash, false)
	check(s.Root, "other-project", stack, s.Hash, false)
	check(s.Root, id, "other-stack", s.Hash, false)
	check(s.Root, id, stack, "changed-manifest", false)
}

func TestExecFlagErrorsBeforeProjectOrProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"start"}, {"-i", "-p", "a"}, {"-p", "a", "b"}, {"-p", ""}, {"-p", " "}, {"-p"}, {"--profile="}, {"--json", "-p", "a"}} {
		var diag bytes.Buffer
		a := New(strings.NewReader(""), io.Discard, &diag)
		if code := a.Execute(context.Background(), append([]string{"exec"}, args...)); code == 0 || strings.Contains(diag.String(), "project root") {
			t.Fatalf("unexpected validation for %q: code=%d stderr=%s", args, code, &diag)
		}
	}
}

func TestWrappedFailureAndRecovery(t *testing.T) {
	s := agentFixture(t)
	a := s.App
	f := &agentFakeCoding{readiness: coding.Readiness{State: coding.Missing}}
	a.CodingCLI = f
	s.Values["assistant.api_key"] = "key"
	s.Values["assistant.model"] = "model"
	if err := s.Exec(context.Background(), execOptions{mode: execInteractive, approve: true}); err == nil || !strings.Contains(err.Error(), "--approve requires Codex") {
		t.Fatalf("preapproval silently changed providers: %v", err)
	}
	for _, mode := range []execMode{execPrompt, execLoop} {
		if err := s.Exec(context.Background(), execOptions{mode: mode}); err == nil || strings.Contains(err.Error(), "consent") {
			t.Fatalf("wrapped mode %v silently offered fallback: %v", mode, err)
		}
	}
	// Installed Codex can serve a configured non-interactive provider without a stored login.
	f.readiness = coding.Readiness{State: coding.Unconfigured, Installed: true}
	exits := &coding.ChildExitError{Code: 42}
	f.prompt = func(_ context.Context, _ coding.SessionRequest, _ string) (string, error) { return "", exits }
	err := s.Exec(context.Background(), execOptions{mode: execPrompt, instruction: "one"})
	var code *Error
	if !errors.As(err, &code) || code.Code != 42 {
		t.Fatalf("lost child exit: %v", err)
	}
	a.In = strings.NewReader("fixture-secret\nfail\nretry") // final line ends at EOF
	calls := 0
	f.prompt = func(_ context.Context, req coding.SessionRequest, thread string) (string, error) {
		calls++
		if calls == 1 && (thread != "" || strings.Contains(req.Instruction, "fixture-secret")) {
			t.Fatal("wrong initial context or unsanitized prompt")
		}
		if calls > 1 && thread != "session-1" {
			t.Fatal("lost active session on failure")
		}
		if calls == 2 {
			return "", exits
		}
		return "session-1", nil
	}
	if err := s.Exec(context.Background(), execOptions{}); err != nil || calls != 3 || !strings.Contains(a.Err.(*bytes.Buffer).String(), "42") {
		t.Fatalf("calls=%d error=%v diagnostic=%s", calls, err, a.Err)
	}
}

func TestLoopControlsAndInputBoundaries(t *testing.T) {
	for _, sentinel := range []string{" /quit ", "/exit", ""} {
		s := agentFixture(t)
		input := strings.Repeat("x", maxExecPrompt+1) + "\n" + sentinel
		if sentinel != "" {
			input += "\nshould not run\n"
		}
		s.App.In = strings.NewReader(input)
		s.App.CodingCLI = &agentFakeCoding{readiness: coding.Readiness{State: coding.Ready, Configured: true}, prompt: func(context.Context, coding.SessionRequest, string) (string, error) {
			t.Fatal("sent invalid prompt or read past quit")
			return "", nil
		}}
		if err := s.Exec(context.Background(), execOptions{}); err != nil || !strings.Contains(s.App.Err.(*bytes.Buffer).String(), "8192") {
			t.Fatalf("err=%v stderr=%s", err, s.App.Err)
		}
	}
	boundary := strings.Repeat("y", maxExecPrompt)
	line, err := readExecLine(bufio.NewReader(strings.NewReader(boundary)))
	if err != nil || line != boundary {
		t.Fatal("lost exact-sized final line", len(line), err)
	}
}

func TestExecCancellationDuringPartialInput(t *testing.T) {
	s := agentFixture(t)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	if _, err = write.WriteString("unfinished"); err != nil {
		t.Fatal(err)
	}
	s.App.In = read
	s.App.CodingCLI = &agentFakeCoding{readiness: coding.Readiness{State: coding.Ready, Configured: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- s.Exec(ctx, execOptions{}) }()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation blocked on an unfinished line")
	}
}
