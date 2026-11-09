package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/runner"
	"github.com/instaclustr/labs/insta/internal/state"
	"github.com/instaclustr/labs/insta/internal/templates"
	"gopkg.in/yaml.v3"
)

func taskSealFixture(t *testing.T, s *session, script string) {
	t.Helper()
	b, err := yaml.Marshal(s.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	agentWrite(t, s.Root, "insta.yaml", string(b))
	s.Manifest, s.Hash, err = manifest.Load(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	py := runner.Python(s.Root, s.Project.SelectedStack)
	if err := os.MkdirAll(filepath.Dir(py), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(py, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	s.Project.Stack().RuntimeDigest, err = runner.RuntimeDigest(s.Root, s.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	s.Project.Stack().RuntimeStatus = "ready"
	s.Project.Stack().ApprovedManifest = s.Hash
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskArgvWorkingDirectoryEnvironmentAndFailureEvidence(t *testing.T) {
	s := agentFixture(t)
	t.Setenv("INSTACLUSTR_PROVISION_USERNAME", "fixture-management-user")
	t.Setenv("INSTACLUSTR_PROVISION_API_KEY", "fixture-management-secret")
	t.Setenv("TASK_INHERITED_SETTING", "shell-value")
	s.Manifest.Config["TASK_DECLARED_SETTING"] = manifest.Definition{}
	s.Values["TASK_DECLARED_SETTING"] = "resolved-value"
	task := s.Manifest.Tasks["example"]
	task.Operations["validate"] = manifest.Operation{
		Command:      []string{"python", "-m", "src.validate", "an argument with spaces; touch SHOULD_NOT_EXIST", "--literal"},
		Effects:      []string{"local-read"},
		Verification: "Inspect the declared application assertions.",
	}
	s.Manifest.Tasks["example"] = task
	taskSealFixture(t, s, `
if [ "${INSTACLUSTR_PROVISION_API_KEY+set}" = "set" ] || [ "${INSTACLUSTR_PROVISION_USERNAME+set}" = "set" ]; then exit 98; fi
printf 'cwd=%s\n' "$PWD"
printf 'arg=%s\n' "$@"
printf 'venv=%s\n' "$VIRTUAL_ENV"
printf 'declared=%s inherited=%s\n' "$TASK_DECLARED_SETTING" "$TASK_INHERITED_SETTING"
printf 'credential=%s\n' "$OPENAI_API_KEY"
exit 23
`)
	s.App.ConfirmFunc = func(string) error { t.Error("local read required approval"); return nil }
	s.App.DataClient = lifecycleClient(func(http.ResponseWriter, *http.Request) { t.Error("local read contacted OpenSearch") })
	result, err := s.RunTask(context.Background(), "example", "validate")
	var ce *Error
	if !errors.As(err, &ce) || ce.Kind != "child_failed" || ce.Code != 6 || ce.ChildExitCode == nil || *ce.ChildExitCode != 23 {
		t.Fatalf("child exit status/category lost: %v", err)
	}
	if result.Status != "failed" || result.ExitCode == nil || *result.ExitCode != 23 || result.FinishedAt == nil || result.Verification != "Not independently verified." {
		t.Fatal("failure was recorded as successful task verification")
	}
	output := s.App.Out.(*bytes.Buffer).String()
	for _, expected := range []string{
		"cwd=" + filepath.Join(s.Root, "scripts/example"),
		"arg=an argument with spaces; touch SHOULD_NOT_EXIST",
		"arg=--literal",
		"venv=" + runner.Venv(s.Root, "dev"),
		"declared=resolved-value inherited=shell-value",
		"credential=[redacted]",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing subprocess evidence %q in %q", expected, output)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Root, "scripts/example/SHOULD_NOT_EXIST")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("argument text was interpreted by a shell")
	}
	log, err := os.ReadFile(filepath.Join(s.Root, result.Log))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(filepath.Join(s.Root, ".insta/project.json"))
	if err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]string{"terminal": output, "log": string(log), "tail": result.OutputTail, "state": string(persisted)} {
		if strings.Contains(value, "fixture-secret") || strings.Contains(value, "fixture-management-secret") {
			t.Errorf("%s exposed a known credential", label)
		}
	}
	p, err := state.Load(s.Root)
	if err != nil || p.Stack().Operations["example/validate"].Status != "failed" {
		t.Fatal("failure checkpoint was not retained", err)
	}
	// The public command preserves one JSON result while routing child output
	// through the redacted diagnostic stream and retaining its own exit code.
	t.Setenv("OPENAI_API_KEY", "fixture-secret")
	t.Chdir(s.Root)
	s.App.Out.(*bytes.Buffer).Reset()
	s.App.Err.(*bytes.Buffer).Reset()
	if code := s.App.Execute(context.Background(), []string{"task", "run", "example", "validate", "--json"}); code != 6 {
		t.Fatalf("JSON task failure exit=%d", code)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Category      string `json:"category"`
			ChildExitCode int    `json:"child_exit_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(s.App.Out.(*bytes.Buffer).Bytes(), &envelope); err != nil || envelope.OK || envelope.Error.Category != "child_failed" || envelope.Error.ChildExitCode != 23 {
		t.Fatal("JSON result was polluted or lost child evidence", err)
	}
	diagnostics := s.App.Err.(*bytes.Buffer).String()
	if strings.Contains(diagnostics, "fixture-secret") || !strings.Contains(diagnostics, "credential=[redacted]") {
		t.Fatal("JSON-mode child diagnostics were not redacted")
	}
}

func TestTaskConditionalCredentialsAndObservedIndexContents(t *testing.T) {
	s := agentFixture(t)
	s.Values["OPENAI_API_KEY"] = ""
	s.Values["OPENAI_MODEL"] = "fixture-model"
	s.Values["LLM_PROVIDER"] = "openai"
	for key, value := range map[string]string{"OPENSEARCH_HOST": "cluster.example.com", "OPENSEARCH_USER": "fixture-user", "OPENSEARCH_PASSWORD": "fixture-password", "OPENSEARCH_PORT": "9200", "OPENSEARCH_SSL": "true", "OPENSEARCH_INDEX": ""} {
		s.Manifest.Config[key] = manifest.Definition{Secret: key == "OPENSEARCH_PASSWORD"}
		s.Values[key] = value
	}
	s.Manifest.Config["OPENAI_MODEL"] = manifest.Definition{}
	s.Manifest.Config["LLM_PROVIDER"] = manifest.Definition{}
	task := s.Manifest.Tasks["example"]
	task.Environment = manifest.Environment{Conditional: []manifest.Conditional{{Operations: []string{"query"}, When: map[string]string{"LLM_PROVIDER": "openai"}, Required: []string{"OPENAI_API_KEY", "OPENAI_MODEL"}}}}
	task.Operations = map[string]manifest.Operation{
		"ingest": {Command: []string{"python", "-m", "src.ingest"}, Effects: []string{"index-write"}},
		"query":  {Command: []string{"python", "-m", "src.query"}, Indexes: []manifest.Index{{Config: "OPENSEARCH_INDEX", Default: "fixture-index"}}, Effects: []string{"remote-model-call"}, Verification: "Inspect citations and the task assertions."},
	}
	s.Manifest.Tasks["example"] = task
	taskSealFixture(t, s, "printf 'operation=%s\n' \"$2\"\nexit 0\n")
	s.App.ConfirmFunc = func(string) error { return nil }
	count, requests := 0, 0
	s.App.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/fixture-index/_count" {
			t.Errorf("wrong index evidence request: %s %s", r.Method, r.URL)
		}
		if count == 0 {
			_, _ = io.WriteString(w, `{"count":0}`)
		} else {
			_, _ = io.WriteString(w, `{"count":12}`)
		}
	})
	if _, err := s.RunTask(context.Background(), "example", "ingest"); err != nil {
		t.Fatal("ingestion prematurely required generation credentials", err)
	}
	if _, err := s.RunTask(context.Background(), "example", "query"); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") || requests != 0 {
		t.Fatal("generation-specific precondition was not checked before requests", err)
	}
	s.Values["OPENAI_API_KEY"] = "fixture-secret"
	if _, err := s.RunTask(context.Background(), "example", "query"); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatal("old ingestion checkpoint substituted for observed indexed data", err)
	}
	if _, exists := s.Project.Stack().Operations["example/query"]; exists {
		t.Fatal("unexecuted query was recorded as a completed operation")
	}
	count = 12
	result, err := s.RunTask(context.Background(), "example", "query")
	if err != nil || requests != 2 || result.Status != "exited" || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatal("query did not recheck current index contents", err)
	}
	if result.Verification != "Inspect citations and the task assertions." {
		t.Fatal("process evidence and application verification were conflated")
	}
}

func TestTaskChangedManifestRequiresRenewedApproval(t *testing.T) {
	s := agentFixture(t)
	taskSealFixture(t, s, "printf 'executed\n'\nexit 0\n")
	approved := s.Project.Stack().ApprovedManifest
	task := s.Manifest.Tasks["example"]
	op := task.Operations["validate"]
	op.Command = append(op.Command, "new-effect")
	op.Effects = []string{"remote-model-call"}
	task.Operations["validate"] = op
	s.Manifest.Tasks["example"] = task
	b, err := yaml.Marshal(s.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	agentWrite(t, s.Root, "insta.yaml", string(b))
	s.Manifest, s.Hash, err = manifest.Load(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	prompts := []string{}
	s.App.ConfirmFunc = func(prompt string) error {
		prompts = append(prompts, prompt)
		return errors.New("fixture declined changed manifest")
	}
	if _, err := s.RunTask(context.Background(), "example", "validate"); err == nil {
		t.Fatal("ran changed manifest without renewed approval")
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "new-effect") || !strings.Contains(prompts[0], "remote-model-call") || len(s.Project.Stack().Operations) != 0 || s.Project.Stack().ApprovedManifest != approved {
		t.Fatal("manifest approval omitted concrete changes or persisted declined consent")
	}
	prompts = nil
	s.App.ConfirmFunc = func(prompt string) error { prompts = append(prompts, prompt); return nil }
	if _, err := s.RunTask(context.Background(), "example", "validate"); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || s.Project.Stack().ApprovedManifest != s.Hash {
		t.Fatal("manifest approval bypassed operation-effects confirmation")
	}
}

func TestTaskReadOnlyPolicyRegistryNeedsNoCloudCredentials(t *testing.T) {
	s := agentFixture(t)
	b, err := templates.Manifest("rag-retrieval")
	if err != nil {
		t.Fatal(err)
	}
	var realManifest manifest.Manifest
	if err := yaml.Unmarshal(b, &realManifest); err != nil {
		t.Fatal(err)
	}
	operation, ok := realManifest.Tasks["operationalize-production"].Operations["policy-registry"]
	if !ok {
		t.Fatal("bundled policy-registry operation missing")
	}
	task := s.Manifest.Tasks["example"]
	task.Operations = map[string]manifest.Operation{"policy-registry": operation}
	s.Manifest.Tasks["example"] = task
	s.Values["OPENAI_API_KEY"] = ""
	t.Setenv("INSTACLUSTR_PROVISION_USERNAME", "")
	t.Setenv("INSTACLUSTR_PROVISION_API_KEY", "")
	taskSealFixture(t, s, "printf 'policy registry compared\n'\nexit 0\n")
	s.App.ConfirmFunc = func(string) error { t.Error("read-only local policy operation required cloud approval"); return nil }
	s.App.DataClient = lifecycleClient(func(http.ResponseWriter, *http.Request) { t.Error("read-only policy operation contacted OpenSearch") })
	result, err := s.RunTask(context.Background(), "example", "policy-registry")
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 || len(s.Project.Stack().Platforms) != 0 {
		t.Fatal("read-only policy operation was blocked by unrelated infrastructure credentials", err)
	}
}
