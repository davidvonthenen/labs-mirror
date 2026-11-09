package coding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixtureThread = "0199a213-81c0-7800-8aa1-bbab2a035a53"

func TestPromptArgvInputAndEnvironment(t *testing.T) {
	script := `printf '%s\n' "$@" > args
printf '%s' "${INSTACLUSTR_PROVISION_API_KEY-unset}/${INSTA_ASSISTANT_API_KEY-unset}/${INSTA_EXEC_TASK_APPROVAL-unset}/$LAB_VALUE" > env
cat > prompt
printf '%s\n' '{"type":"thread.started","thread_id":"` + fixtureThread + `"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"tool output"}}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"reply"}}'
printf '%s\n' '{"type":"turn.completed"}'
`
	c := &Codex{Executable: executableFixture(t, script)}
	for _, tc := range []struct {
		name, profile, thread, args string
		oneShot                     bool
	}{
		{"one-shot", "workshop", "", "exec --profile workshop --skip-git-repo-check --json --ephemeral -", true},
		{"first turn", "", "", "exec --skip-git-repo-check --json -", false},
		{"follow-up", "workshop", fixtureThread, "exec --profile workshop --skip-git-repo-check --json resume " + fixtureThread + " -", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			var out bytes.Buffer
			req := SessionRequest{
				Workspace: root, Instruction: "--flag $(touch injected)\nfollow up",
				TaskID: "baseline", Blueprint: "tasks/baseline.md", Profile: tc.profile, OneShot: tc.oneShot,
				Stdin: strings.NewReader("next turn must stay in insta"), Stdout: &out,
				Environment: []string{"PATH=/usr/bin:/bin", "LAB_VALUE=kept", "INSTACLUSTR_PROVISION_API_KEY=hidden", "INSTA_ASSISTANT_API_KEY=hidden", TaskApprovalEnv + "=outer-session"},
			}
			id, err := c.RunPrompt(context.Background(), req, tc.thread)
			wantID := fixtureThread
			if tc.oneShot {
				wantID = ""
			}
			if err != nil || id != wantID || out.String() != "reply\n" {
				t.Fatalf("thread=%q output=%q err=%v", id, out.String(), err)
			}
			read := func(name string) string {
				t.Helper()
				b, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			if got := read("args"); got != strings.ReplaceAll(tc.args, " ", "\n")+"\n" {
				t.Fatalf("unexpected arguments: %q", got)
			}
			prompt := read("prompt")
			if tc.thread == "" {
				if !strings.Contains(prompt, "INSTA.md") || !strings.Contains(prompt, "tasks/baseline.md") || !strings.HasSuffix(prompt, req.Instruction) {
					t.Fatalf("missing instruction or workspace context: %s", prompt)
				}
			} else if prompt != req.Instruction {
				t.Fatalf("wrong follow-up: %q", prompt)
			}
			if env := read("env"); env != "unset/unset/unset/kept" {
				t.Fatalf("wrong child environment: %q", env)
			}
		})
	}
}

func TestApprovedCodexLaunch(t *testing.T) {
	const scope = "c09d09b82c8973f9e94f5254a6418a8f3088488f6c9a0612a0b4a10639962ddf"
	c := &Codex{Executable: executableFixture(t, `printf '%s\n' "$@" > args
printf '%s' "${INSTA_EXEC_TASK_APPROVAL-unset}" > inherited
cat > input
printf '%s\n' '{"type":"thread.started","thread_id":"`+fixtureThread+`"}' '{"type":"turn.completed"}'
`), isTerminal: func(io.Reader, io.Writer) bool { return true }}
	for _, mode := range []string{"native", "one-shot", "loop", "resume"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			req := SessionRequest{Workspace: root, Profile: "workshop", Instruction: "run the query",
				TaskApprovalScope: scope, OneShot: mode == "one-shot", Stdin: strings.NewReader(""),
				Environment: []string{"PATH=/usr/bin:/bin", TaskApprovalEnv + "=stale"}}
			var err error
			if mode == "native" {
				err = c.RunInteractive(context.Background(), req)
			} else {
				thread := ""
				if mode == "resume" {
					thread = fixtureThread
				}
				_, err = c.RunPrompt(context.Background(), req, thread)
			}
			if err != nil {
				t.Fatal(err)
			}
			read := func(name string) string {
				t.Helper()
				b, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			args := read("args")
			want := "-c\napproval_policy=\"never\"\n-c\nsandbox_mode=\"workspace-write\"\n-c\nsandbox_workspace_write.network_access=true\n-c\nshell_environment_policy.set." + TaskApprovalEnv + "=\"" + scope + "\"\n"
			if !strings.HasPrefix(args, want) || !strings.Contains(args, "--profile\nworkshop\n") {
				t.Fatalf("incorrect Codex override or profile: %s", args)
			}
			if read("inherited") != "unset" {
				t.Fatal("stale approval leaked into the child environment")
			}
			instruction := read("input")
			if mode == "native" {
				instruction = args
			}
			if mode == "resume" {
				if !strings.Contains(args, "\nresume\n"+fixtureThread+"\n-\n") || instruction != req.Instruction {
					t.Fatalf("resume lost its thread or user input: %q, %q", args, instruction)
				}
			} else if !strings.Contains(instruction, "insta exec --approve") {
				t.Fatal("initial lab instruction omitted the active approval mode")
			}
		})
	}
}

func TestPromptStreamsBeforeProcessExit(t *testing.T) {
	// The child cannot finish until the caller has actually seen the first reply.
	root := t.TempDir()
	script := `printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"early reply"}}'
while [ ! -f delivered ]; do sleep 0.02; done
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"final reply\n"}}'
printf '%s\n' '{"type":"turn.completed"}'`
	c := &Codex{Executable: executableFixture(t, script)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() {
		_, err := c.RunPrompt(ctx, SessionRequest{Workspace: root, OneShot: true, Stdout: writer}, "")
		writer.Close()
		done <- err
	}()
	first := make([]byte, len("early reply\n"))
	if _, err := io.ReadFull(reader, first); err != nil || string(first) != "early reply\n" {
		t.Fatalf("missing early output: %q, %v", first, err)
	}
	if err := os.WriteFile(filepath.Join(root, "delivered"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	last, err := io.ReadAll(reader)
	if err != nil || string(last) != "final reply\n" {
		t.Fatalf("bad final reply: %q, %v", last, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPromptFailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"missing completion", `printf '%s\n' '{"type":"turn.started"}'`, "without completing"},
		{"no thread", `printf '%s\n' '{"type":"turn.completed"}'`, "conversation ID"},
		{"bad thread", `printf '%s\n' '{"type":"thread.started","thread_id":"--last"}'`, "conversation ID"},
		{"bad JSON", `echo 'not JSON'`, "invalid codex exec"},
		{"failed event", `printf '%s\n' '{"type":"turn.failed","error":{"message":"request rejected"}}'`, "request rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Codex{Executable: executableFixture(t, tc.script)}
			_, err := c.RunPrompt(context.Background(), SessionRequest{Workspace: t.TempDir()}, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestPromptPreservesChildExitAndDiagnostic(t *testing.T) {
	c := &Codex{Executable: executableFixture(t, `printf '%s\n' '{"type":"turn.failed","error":{"message":"bad profile"}}'; exit 23`)}
	var diagnostic bytes.Buffer
	_, err := c.RunPrompt(context.Background(), SessionRequest{Workspace: t.TempDir(), Stderr: &diagnostic}, "")
	var child *ChildExitError
	if !errors.As(err, &child) || child.Code != 23 || !strings.Contains(diagnostic.String(), "bad profile") {
		t.Fatalf("lost child exit or diagnostic: %v, %q", err, diagnostic.String())
	}
}

func TestPromptLargeToolEventAndRecoveredError(t *testing.T) {
	var turn execTurn
	events := `{"type":"error","message":"Reconnecting"}
{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"` + strings.Repeat("x", 300<<10) + `"}}
{"type":"turn.completed"}
`
	if err := turn.read(strings.NewReader(events), io.Discard); err != nil || !turn.completed {
		t.Fatalf("valid turn with long intermediate tool output failed: %v", err)
	}
}
