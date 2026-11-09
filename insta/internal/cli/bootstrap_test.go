package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instaclustr/labs/insta/internal/bootstrap"
	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/state"
)

// This subprocess double exercises the actual venv/pip/model argv and managed
// interpreter path without installing Python packages or downloading models.
const bootstrapPythonDouble = `#!/bin/sh
if [ "$1" = "-c" ]; then
  printf '3.12\n'
  exit 0
fi
if [ "$1" = "--version" ]; then
  printf 'Python 3.12.0\n'
  exit 0
fi
printf 'cwd=%s\n' "$PWD" >> "$TEST_PYTHON_TRACE"
printf 'arg=%s\n' "$@" >> "$TEST_PYTHON_TRACE"
if [ "$1" = "-m" ] && [ "$2" = "venv" ]; then
  /bin/mkdir -p "$3/bin" || exit 71
  /bin/cp "$0" "$3/bin/python" || exit 72
  exit 0
fi
if [ "$1" = "-m" ] && [ "$2" = "pip" ]; then
  if [ "$TEST_PYTHON_FAIL" = "yes" ]; then
    printf 'simulated dependency installation failure\n' >&2
    exit 73
  fi
  exit 0
fi
if [ "$1" = "-m" ] && [ "$2" = "spacy" ]; then
  exit 0
fi
printf 'unexpected fake Python invocation\n' >&2
exit 74
`

func bootstrapHarness(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer, string, *int) {
	t.Helper()
	archivePath := os.Getenv("INSTA_TEST_SOURCE_ARCHIVE")
	if archivePath == "" {
		t.Skip("set INSTA_TEST_SOURCE_ARCHIVE for complete pinned-source CLI bootstrap fixtures")
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	t.Chdir(base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "user-config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(base, "user-cache"))
	// A clean initial shell has no lab settings or management credentials.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OPENSEARCH_") || strings.HasPrefix(key, "INSTACLUSTR_") || strings.HasPrefix(key, "INSTA_ASSISTANT_") || key == "INSTA_OPENSEARCH_MODE" || key == "OPENAI_API_KEY" || key == "OPENAI_MODEL" || key == "LLM_PROVIDER" || key == "TAVILY_API_KEY" || key == "FINNHUB_API_KEY" {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	bin := filepath.Join(base, "fake-bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "python3.12"), []byte(bootstrapPythonDouble), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	trace := filepath.Join(base, "python-invocations.txt")
	t.Setenv("TEST_PYTHON_TRACE", trace)
	t.Setenv("TEST_PYTHON_FAIL", "")
	var output, diagnostic bytes.Buffer
	a := New(strings.NewReader(""), &output, &diagnostic)
	a.CodingCLI = &agentFakeCoding{readiness: coding.Readiness{State: coding.Missing, Diagnostic: "fixture Codex executable is missing"}}
	requests := 0
	a.Source = &bootstrap.Source{
		Repository: bootstrap.DefaultRepository,
		CacheDir:   filepath.Join(base, "user-cache", "insta", "sources"),
		HTTP: lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected source method: %s", r.Method)
			}
			switch r.URL.String() {
			case "https://api.github.com/repos/instaclustr/labs/commits/heads/main":
				_, _ = io.WriteString(w, `{"sha":"c321a65c9586706d8696645327e28b07f6b78b77"}`)
			case "https://codeload.github.com/instaclustr/labs/zip/c321a65c9586706d8696645327e28b07f6b78b77":
				requests++
				_, _ = w.Write(archive)
			default:
				t.Errorf("unexpected source request: %s", r.URL)
			}
		}),
	}
	a.DataClient = lifecycleClient(func(http.ResponseWriter, *http.Request) {
		t.Error("new contacted a data platform")
	})
	return a, &output, &diagnostic, trace, &requests
}

func TestNewFromPinnedArchiveCreatesManagedProjectWithoutCodex(t *testing.T) {
	for _, labID := range []string{"rag-retrieval", "agentic-rag"} {
		t.Run(labID, func(t *testing.T) {
			a, output, diagnostic, trace, requests := bootstrapHarness(t)
			name := "project-" + labID
			if code := a.Execute(context.Background(), []string{"new", name, "--lab", labID, "--json"}); code != 0 {
				t.Fatalf("new exit=%d\n%s\n%s", code, output, diagnostic)
			}
			root, _ := filepath.Abs(name)
			p, err := state.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if p.Lab != labID || p.SelectedStack != "dev" || len(p.Stacks) != 1 || p.Stack().RuntimeStatus != "ready" || p.Stack().RuntimeDigest == "" || p.Stack().ApprovedManifest == "" {
				t.Fatal("project/default stack/runtime initialization incomplete")
			}
			if len(p.Stack().Platforms) != 0 || len(p.Stack().Services) != 0 || len(p.Stack().Operations) != 0 {
				t.Fatal("new provisioned resources or ran a teaching task")
			}
			if p.Source["origin"] != bootstrap.DefaultRepository.Origin || p.Source["commit"] != "c321a65c9586706d8696645327e28b07f6b78b77" || p.Source["ref_type"] != "heads" || p.Source["ref"] != "main" || len(p.Source["sha256"]) != 64 || len(p.Source["overlay_sha256"]) != 64 {
				t.Fatal("source and overlay provenance missing")
			}
			for _, relative := range []string{"AGENTS.md", "insta.yaml", "insta.schema.json", "INSTA.md", ".env", ".insta/venvs/dev/bin/python"} {
				if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
					t.Fatal(err)
				}
			}
			marker, err := os.ReadFile(filepath.Join(root, ".env"))
			if err != nil || strings.Contains(string(marker), "API_KEY=") || strings.Contains(string(marker), "PASSWORD=") {
				t.Fatal("compatibility .env marker contained settings or was missing")
			}
			if labID == "rag-retrieval" {
				if _, err := os.Stat(filepath.Join(root, ".agents/skills/instaclustr-rag-builder/SKILL.md")); err != nil {
					t.Fatal("hidden agent instructions missing", err)
				}
				info, err := os.Lstat(filepath.Join(root, "scripts/baseline-vector-rag/src/common"))
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("shared source symlink was not preserved", err)
				}
				if !strings.HasSuffix(p.Stack().Config["OPENSEARCH_VECTOR_RECIPES_BASELINE_INDEX"], "-"+p.ID[:8]+"-dev") {
					t.Fatal("stack index default was not scoped")
				}
			}
			invocations, err := os.ReadFile(trace)
			if err != nil || !bytes.Contains(invocations, []byte("arg=venv")) || !bytes.Contains(invocations, []byte("arg=pip")) {
				t.Fatal("managed runtime bootstrap was not invoked", err)
			}
			if bytes.Contains(invocations, []byte("src.ingest")) || bytes.Contains(invocations, []byte("src.query")) {
				t.Fatal("new ran educational commands")
			}
			if labID == "rag-retrieval" && !bytes.Contains(invocations, []byte("arg=spacy")) {
				t.Fatal("declared NER model bootstrap omitted")
			}
			if *requests != 1 || strings.Count(diagnostic.String(), "Codex is unavailable or not configured") != 1 {
				t.Fatal("unexpected download/warning count")
			}
			var result struct {
				OK bool `json:"ok"`
			}
			if err := json.Unmarshal(output.Bytes(), &result); err != nil || !result.OK {
				t.Fatal("new did not produce a single JSON result", err)
			}
			before := len(invocations)
			output.Reset()
			diagnostic.Reset()
			if code := a.Execute(context.Background(), []string{"new", name, "--lab", labID, "--json"}); code != 2 {
				t.Fatalf("existing destination exit=%d", code)
			}
			after, _ := os.ReadFile(trace)
			if *requests != 1 || len(after) != before {
				t.Fatal("rejected destination caused bootstrap effects")
			}
			// Project commands work from nested task directories immediately.
			t.Chdir(filepath.Join(root, "tasks"))
			for _, args := range [][]string{{"stack", "list", "--json"}, {"config", "list", "--json"}, {"task", "list", "--json"}} {
				output.Reset()
				diagnostic.Reset()
				if code := a.Execute(context.Background(), args); code != 0 {
					t.Fatalf("%v exit=%d: %s", args, code, diagnostic)
				}
			}
		})
	}
}

func TestNewIncompleteRuntimeResumesThroughUp(t *testing.T) {
	a, output, diagnostic, trace, requests := bootstrapHarness(t)
	t.Setenv("TEST_PYTHON_FAIL", "yes")
	if code := a.Execute(context.Background(), []string{"new", "retry-agentic", "--lab", "agentic-rag", "--json"}); code != 3 {
		t.Fatalf("incomplete bootstrap exit=%d\n%s\n%s", code, output, diagnostic)
	}
	root, _ := filepath.Abs("retry-agentic")
	p, err := state.Load(root)
	if err != nil || p.Stack().RuntimeStatus != "failed" || len(p.Stack().Platforms) != 0 {
		t.Fatal("failed bootstrap was lost or reported ready", err)
	}
	if !strings.Contains(output.String(), "bootstrap_incomplete") || !strings.Contains(output.String(), "insta up to retry") {
		t.Fatal("incomplete bootstrap lacks a CLI-managed recovery path")
	}
	t.Setenv("TEST_PYTHON_FAIL", "")
	t.Setenv("INSTA_OPENSEARCH_MODE", "attach")
	t.Setenv("OPENSEARCH_HOST", "existing.example.com")
	t.Setenv("OPENSEARCH_USER", "fixture-user")
	t.Setenv("OPENSEARCH_PASS", "fixture-password")
	healthChecks := 0
	a.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		healthChecks++
		if r.Method != http.MethodGet || r.URL.Path != "/_cluster/health" {
			t.Errorf("unexpected recovery request: %s %s", r.Method, r.URL)
		}
		_, _ = io.WriteString(w, `{"status":"green","timed_out":false}`)
	})
	a.ConfirmFunc = func(string) error { t.Error("unchanged source required new approval"); return nil }
	t.Chdir(root)
	output.Reset()
	diagnostic.Reset()
	if code := a.Execute(context.Background(), []string{"up", "--json"}); code != 0 {
		t.Fatalf("recovery exit=%d\n%s\n%s", code, output, diagnostic)
	}
	p, err = state.Load(root)
	if err != nil || p.Stack().RuntimeStatus != "ready" || p.Stack().Platforms["opensearch"] == nil || p.Stack().Platforms["opensearch"].Owned {
		t.Fatal("recovered runtime/attached platform state incorrect", err)
	}
	if healthChecks != 1 || *requests != 1 || len(p.Stack().Operations) != 0 || len(p.Stack().Services) != 0 {
		t.Fatal("up repeated download or launched teaching work")
	}
	invocations, _ := os.ReadFile(trace)
	if strings.Count(string(invocations), "arg=venv\n") != 1 || strings.Count(string(invocations), "arg=pip\n") != 2 {
		t.Fatal("up failed to reuse venv/retry dependency installation")
	}
	if _, _, err := manifest.Load(root); err != nil {
		t.Fatal("recovery damaged the lab manifest", err)
	}
}
