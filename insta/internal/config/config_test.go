package config

import (
	"strings"
	"testing"

	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/state"
)

func TestPrecedenceAndSecretViews(t *testing.T) {
	m := &manifest.Manifest{Config: map[string]manifest.Definition{"OPENSEARCH_HOST": {Default: "default.example"}, "OPENSEARCH_PASSWORD": {Secret: true}}}
	p, _ := state.New("rag-retrieval", nil)
	p.Stack().Outputs["OPENSEARCH_HOST"] = "provider.example"
	p.Stack().Config["OPENSEARCH_HOST"] = "stack.example"
	secrets := map[string]string{"provider.OPENSEARCH_PASSWORD": "provider-secret", "OPENSEARCH_PASSWORD": "stored-secret", "INSTACLUSTR_PROVISION_API_KEY": "forbidden-stored"}
	env := map[string]string{"OPENSEARCH_HOST": "shell.example", "OPENSEARCH_PASSWORD": "shell-secret"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	v, view, e := Resolve(m, p, secrets, lookup)
	if e != nil {
		t.Fatal(e)
	}
	if v["OPENSEARCH_HOST"] != "shell.example" || v["OPENSEARCH_PASSWORD"] != "shell-secret" || view["OPENSEARCH_PASSWORD"].Value != "[redacted]" {
		t.Fatal("shell/secret precedence failed")
	}
	if v["INSTACLUSTR_PROVISION_API_KEY"] != "" {
		t.Fatal("stored provisioning key was accepted")
	}
	delete(env, "OPENSEARCH_HOST")
	delete(env, "OPENSEARCH_PASSWORD")
	v, _, e = Resolve(m, p, secrets, lookup)
	if e != nil {
		t.Fatal(e)
	}
	if v["OPENSEARCH_HOST"] != "stack.example" || v["OPENSEARCH_PASSWORD"] != "stored-secret" {
		t.Fatal("stored precedence failed")
	}
	delete(p.Stack().Config, "OPENSEARCH_HOST")
	delete(secrets, "OPENSEARCH_PASSWORD")
	v, view, e = Resolve(m, p, secrets, lookup)
	if e != nil {
		t.Fatal(e)
	}
	if v["OPENSEARCH_HOST"] != "provider.example" || v["OPENSEARCH_PASSWORD"] != "provider-secret" || view["OPENSEARCH_PASSWORD"].Source != "provider secret store" {
		t.Fatal("provider outputs precedence failed")
	}
	delete(p.Stack().Outputs, "OPENSEARCH_HOST")
	v, _, _ = Resolve(m, p, secrets, lookup)
	if v["OPENSEARCH_HOST"] != "default.example" {
		t.Fatal("manifest default missing")
	}
}

func TestTaskEnvironmentPreservesShellAndSourceDefaults(t *testing.T) {
	t.Setenv("INSTACLUSTR_PROVISION_API_KEY", "provision-secret")
	t.Setenv("INSTA_ASSISTANT_API_KEY", "assistant-secret")
	t.Setenv("CUSTOM_SHELL_VALUE", "keep-me")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/tmp/web-identity-token")
	m := &manifest.Manifest{Config: map[string]manifest.Definition{"AWS_PROFILE": {}, "OPENSEARCH_INDEX": {}, "OPENAI_API_KEY": {Secret: true}}}
	entries := TaskEnvironment(m, map[string]string{"AWS_PROFILE": "", "OPENSEARCH_INDEX": "", "OPENAI_API_KEY": "app-secret"}, "/project", "/project/.insta/venvs/dev")
	env := map[string]string{}
	for _, entry := range entries {
		k, v, _ := strings.Cut(entry, "=")
		env[k] = v
	}
	if _, ok := env["INSTACLUSTR_PROVISION_API_KEY"]; ok {
		t.Fatal("management credential forwarded")
	}
	if _, ok := env["INSTA_ASSISTANT_API_KEY"]; ok {
		t.Fatal("assistant credential forwarded")
	}
	if _, ok := env["OPENSEARCH_INDEX"]; ok {
		t.Fatal("empty value overwrote per-task source defaults")
	}
	if env["CUSTOM_SHELL_VALUE"] != "keep-me" || env["OPENAI_API_KEY"] != "app-secret" || env["AWS_WEB_IDENTITY_TOKEN_FILE"] != "/tmp/web-identity-token" {
		t.Fatal("expected shell or application environment missing")
	}
	if !strings.HasPrefix(env["PATH"], "/project/.insta/venvs/dev/bin:") {
		t.Fatal("managed interpreter not first on PATH")
	}
}

func TestTypesConditionsAndMissingValues(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		def        Definition
	}{{"PORT", "abc", Definition{Definition: manifest.Definition{Type: "integer"}}}, {"SSL", "1", Definition{Definition: manifest.Definition{Type: "boolean"}}}, {"LLM_PROVIDER", "unknown", Definition{Definition: manifest.Definition{Enum: []string{"openai", "bedrock"}}}}, {"OPENSEARCH_HOST", "https://host", Definition{}}} {
		if Validate(tc.key, tc.value, tc.def) == nil {
			t.Errorf("invalid %s accepted", tc.key)
		}
	}
	e := manifest.Environment{Required: []string{"OPENSEARCH_HOST"}, Conditional: []manifest.Conditional{{Operations: []string{"query"}, When: map[string]string{"LLM_PROVIDER": "openai"}, Required: []string{"OPENAI_API_KEY"}}}}
	v := map[string]string{"OPENSEARCH_HOST": "configured", "LLM_PROVIDER": "openai"}
	if Require(v, manifest.Required(e, "ingest", v)...) != nil {
		t.Fatal("ingest incorrectly required LLM key")
	}
	if Require(v, manifest.Required(e, "query", v)...) == nil {
		t.Fatal("query missing key accepted")
	}
}
