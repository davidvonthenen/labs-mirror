package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instaclustr/labs/insta/internal/assistant"
	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/runner"
	"github.com/instaclustr/labs/insta/internal/state"
	"gopkg.in/yaml.v3"
)

func agentWrite(t *testing.T, root, relative, contents string) {
	t.Helper()
	p := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func agentFixture(t *testing.T) *session {
	t.Helper()
	root := t.TempDir()
	for p, text := range map[string]string{
		"AGENTS.md":                       "Read source before running declared commands.\n",
		"INSTA.md":                        "Use insta task commands.\n",
		"README.md":                       "Teaching fixture.\n",
		"tasks/example.md":                "Exercise blueprint with fixture-secret.\n",
		"scripts/example/src/validate.py": "print('fixture-secret')\n",
		"support/requirements.txt":        "\n",
		"support/common/config.py":        "API_KEY = 'fixture-secret'\n",
	} {
		agentWrite(t, root, p, text)
	}
	m := &manifest.Manifest{
		SchemaVersion: 1, ID: "rag-retrieval", Name: "Fixture", Instructions: "AGENTS.md",
		Runtime:   manifest.Runtime{Kind: "python", Python: "3.12", Requirements: "support/requirements.txt"},
		Config:    map[string]manifest.Definition{"OPENAI_API_KEY": {Secret: true}},
		Platforms: map[string]manifest.Platform{}, Services: map[string]manifest.Service{},
		Tasks: map[string]manifest.Task{"example": {
			Blueprint: "tasks/example.md", Source: "scripts/example",
			Operations: map[string]manifest.Operation{
				"validate": {Command: []string{"python", "-m", "src.validate"}},
				"query":    {Mode: "interactive", Command: []string{"python", "-m", "src.query"}},
			},
		}},
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	agentWrite(t, root, "insta.yaml", string(b))
	p, err := state.New("rag-retrieval", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err = state.Save(root, p); err != nil {
		t.Fatal(err)
	}
	var output, diagnostic bytes.Buffer
	return &session{Root: root, Project: p, Manifest: m, Values: map[string]string{"agent.backend": "codex", "OPENAI_API_KEY": "fixture-secret"}, Redactor: runner.NewRedactor([]string{"fixture-secret"}), App: &App{In: strings.NewReader(""), Out: &output, Err: &diagnostic}}
}

type agentFakeCoding struct {
	readiness    coding.Readiness
	probes, runs int
	req          coding.SessionRequest
	check        func(coding.SessionRequest) error
	prompt       func(context.Context, coding.SessionRequest, string) (string, error)
}

func (*agentFakeCoding) Name() string { return "fake" }
func (f *agentFakeCoding) Probe(context.Context) (coding.Readiness, error) {
	f.probes++
	return f.readiness, nil
}
func (f *agentFakeCoding) RunInteractive(_ context.Context, r coding.SessionRequest) error {
	f.runs++
	f.req = r
	if f.check != nil {
		return f.check(r)
	}
	return nil
}

func (f *agentFakeCoding) RunPrompt(ctx context.Context, req coding.SessionRequest, thread string) (string, error) {
	if f.prompt == nil {
		return "", errors.New("unexpected non-interactive run")
	}
	return f.prompt(ctx, req, thread)
}

func TestExecLaunchHasContextAndNoHeldLock(t *testing.T) {
	s := agentFixture(t)
	f := &agentFakeCoding{readiness: coding.Readiness{State: coding.Ready, Installed: true, Configured: true}}
	f.check = func(req coding.SessionRequest) error {
		l, err := state.Lock(req.Workspace)
		if err != nil {
			return err
		}
		return l.Close()
	}
	s.App.CodingCLI = f
	t.Setenv("INSTACLUSTR_PROVISION_API_KEY", "management-secret")
	t.Setenv("INSTACLUSTR_PROVISION_USERNAME", "management-user")
	if err := s.Exec(context.Background(), execOptions{mode: execInteractive, instruction: "Explain fixture-secret", taskID: "example"}); err != nil {
		t.Fatal(err)
	}
	if f.probes != 1 || f.runs != 1 {
		t.Fatalf("wrong calls %d %d", f.probes, f.runs)
	}
	prompt, _ := coding.LaunchInstruction(f.req)
	if f.req.Workspace != s.Root || f.req.TaskID != "example" || f.req.Blueprint != "tasks/example.md" || !strings.Contains(prompt, "INSTA.md") {
		t.Fatalf("missing context: %+v", f.req)
	}
	if strings.Contains(f.req.Instruction, "fixture-secret") {
		t.Fatal("secret entered initial instruction")
	}
	for _, entry := range f.req.Environment {
		if strings.HasPrefix(entry, "INSTACLUSTR_PROVISION_") {
			t.Fatal("management credential reached coding adapter")
		}
	}
	if f.req.Stdin != s.App.In || f.req.Stdout != s.App.Out || f.req.Stderr != s.App.Err {
		t.Fatal("terminal streams were replaced")
	}
}

func TestExecMissingCodexAndExplicitStub(t *testing.T) {
	s := agentFixture(t)
	f := &agentFakeCoding{readiness: coding.Readiness{State: coding.Missing, Diagnostic: "missing executable"}}
	s.App.CodingCLI = f
	err := s.Exec(context.Background(), execOptions{mode: execInteractive, instruction: "Explain"})
	if err == nil || !strings.Contains(err.Error(), "no guided session started") || f.runs != 0 {
		t.Fatalf("missing Codex pretended success: %v", err)
	}
	if strings.Count(s.App.Err.(*bytes.Buffer).String(), coding.Warning) != 1 {
		t.Fatal("warning did not appear exactly once")
	}
	s.Values["agent.backend"] = "claude-code"
	var unsupported *coding.NotImplementedError
	if !errors.As(s.Exec(context.Background(), execOptions{mode: execInteractive, instruction: "Explain"}), &unsupported) || f.probes != 1 {
		t.Fatal("explicit stub silently switched provider")
	}
}

func TestExecJSONAndFallbackNonTTY(t *testing.T) {
	s := agentFixture(t)
	f := &agentFakeCoding{readiness: coding.Readiness{State: coding.Missing}}
	s.App.CodingCLI = f
	s.App.JSON = true
	if err := s.Exec(context.Background(), execOptions{mode: execInteractive, instruction: "Explain"}); err == nil || f.probes != 0 {
		t.Fatal("JSON launched an interactive session")
	}
	s.App.JSON = false
	s.App.Yes = true
	s.Values["assistant.api_key"] = "separate-key"
	s.Values["assistant.model"] = "fixture-model"
	if err := s.Exec(context.Background(), execOptions{mode: execInteractive, instruction: "Explain"}); err == nil || !strings.Contains(err.Error(), "explicit API-assistance consent") {
		t.Fatalf("--yes bypassed fallback TTY consent: %v", err)
	}
}

func TestExecPreservesCodingExitStatus(t *testing.T) {
	s := agentFixture(t)
	s.App.CodingCLI = &agentFakeCoding{readiness: coding.Readiness{State: coding.Ready, Installed: true, Configured: true}, check: func(coding.SessionRequest) error {
		return &coding.ChildExitError{Code: 23}
	}}
	err := s.Exec(context.Background(), execOptions{mode: execInteractive, instruction: "Explain"})
	var result *Error
	if !errors.As(err, &result) || result.Code != 23 || result.ChildExitCode == nil || *result.ChildExitCode != 23 {
		t.Fatalf("lost native exit status: %v", err)
	}
}

func TestAssistantSourceAllowlistAndRedaction(t *testing.T) {
	s := agentFixture(t)
	text, err := s.readAssistantSource(s.Manifest, "scripts/example/src/validate.py")
	if err != nil || strings.Contains(text, "fixture-secret") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("bad source response %q %v", text, err)
	}
	for _, relative := range []string{".env", ".insta/project.json", "../AGENTS.md", "/etc/passwd", "support/recipes/recipes.json", "scripts/example/src/secrets.py", "scripts/example/src/policy_history.json", "scripts/example/output/result.py", "unapproved.py"} {
		if _, err := s.readAssistantSource(s.Manifest, relative); err == nil {
			t.Fatalf("read forbidden %s", relative)
		}
	}
	if err := os.Symlink(filepath.Join(s.Root, ".insta", "project.json"), filepath.Join(s.Root, "scripts/example/src/alias.py")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readAssistantSource(s.Manifest, "scripts/example/src/alias.py"); err == nil {
		t.Fatal("allowed alias exposed internal state")
	}
	outside := filepath.Join(t.TempDir(), "outside.py")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Root, "scripts/example/src/outside.py")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readAssistantSource(s.Manifest, "scripts/example/src/outside.py"); err == nil {
		t.Fatal("symlink escaped workspace")
	}
	if err := os.Symlink("../../../support/common/config.py", filepath.Join(s.Root, "scripts/example/src/common.py")); err != nil {
		t.Fatal(err)
	}
	if text, err := s.readAssistantSource(s.Manifest, "scripts/example/src/common.py"); err != nil || !strings.Contains(text, "[redacted]") {
		t.Fatalf("valid internal source link failed %v", err)
	}
}

func TestAssistantReadBoundsAndTextValidation(t *testing.T) {
	s := agentFixture(t)
	agentWrite(t, s.Root, "scripts/example/src/large.py", strings.Repeat("x", 20<<10))
	text, err := s.readAssistantSource(s.Manifest, "scripts/example/src/large.py")
	if err != nil || len(text) > 17000 || !strings.Contains(text, "truncated at 16 KiB") {
		t.Fatalf("source not bounded %d %v", len(text), err)
	}
	for _, bad := range []string{"\x00binary", "\xffinvalid"} {
		agentWrite(t, s.Root, "scripts/example/src/binary.py", bad)
		if _, err := s.readAssistantSource(s.Manifest, "scripts/example/src/binary.py"); err == nil {
			t.Fatal("binary source transmitted")
		}
	}
}

func TestAssistantToolUsesDeclaredSourcesAndObservedStatus(t *testing.T) {
	s := agentFixture(t)
	s.Project.Stack().Services["ner"] = &state.Service{Token: "private-service-token", Socket: filepath.Join(s.Root, "absent.sock"), PID: 123}
	s.Project.Stack().Platforms["opensearch"] = &state.Platform{Provider: "instaclustr-opensearch", ID: "cluster-id", Owned: true, Status: "RUNNING"}
	if err := state.Save(s.Root, s.Project); err != nil {
		t.Fatal(err)
	}
	for _, call := range []assistant.ToolCall{{Name: "list_tasks"}, {Name: "read_task", TaskID: "example"}, {Name: "get_lab_status"}} {
		text, err := s.assistantTool(context.Background(), call)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "fixture-secret") || strings.Contains(text, "private-service-token") {
			t.Fatal("tool leaked credentials/ownership token")
		}
		if call.Name == "get_lab_status" && (!strings.Contains(text, `"recorded_status":"RUNNING"`) || !strings.Contains(text, `"owned_process_running":false`)) {
			t.Fatalf("recorded/observed state confused: %s", text)
		}
	}
	for _, call := range []assistant.ToolCall{{Name: "shell"}, {Name: "read_task", TaskID: "missing"}, {Name: "run_task_operation", TaskID: "missing", Operation: "validate"}, {Name: "run_task_operation", TaskID: "example", Operation: "undeclared"}, {Name: "run_task_operation", TaskID: "example", Operation: "query"}} {
		if _, err := s.assistantTool(context.Background(), call); err == nil {
			t.Fatalf("undeclared/unsupported tool ran: %+v", call)
		}
	}
}

func TestAssistantRejectsChangedProjectIdentity(t *testing.T) {
	s := agentFixture(t)
	other, err := state.New("rag-retrieval", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Save(s.Root, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.assistantTool(context.Background(), assistant.ToolCall{Name: "list_tasks"}); err == nil {
		t.Fatal("continued into a different project")
	}
}

func TestAgentTerminalRejectsPipes(t *testing.T) {
	if agentTerminal(strings.NewReader("yes"), io.Discard) {
		t.Fatal("pipe accepted as terminal")
	}
}
