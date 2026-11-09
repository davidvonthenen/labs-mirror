// Package config resolves shell > stack > provider outputs > lab defaults.
package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/state"
)

type Definition struct {
	manifest.Definition
	Env       string
	ShellOnly bool
}
type Value struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Secret bool   `json:"secret"`
}

func Definitions(m *manifest.Manifest) map[string]Definition {
	d := map[string]Definition{}
	for k, v := range m.Config {
		d[k] = Definition{Definition: v, Env: k}
	}
	d["INSTACLUSTR_PROVISION_USERNAME"] = Definition{Definition: manifest.Definition{Description: "Instaclustr provisioning username; export in the invoking shell"}, Env: "INSTACLUSTR_PROVISION_USERNAME", ShellOnly: true}
	d["INSTACLUSTR_PROVISION_API_KEY"] = Definition{Definition: manifest.Definition{Secret: true, Description: "Instaclustr provisioning API key; export in the invoking shell"}, Env: "INSTACLUSTR_PROVISION_API_KEY", ShellOnly: true}
	d["opensearch.mode"] = Definition{Definition: manifest.Definition{Default: "provision", Enum: []string{"provision", "attach"}, Description: "Provision an owned cluster, or attach to the configured OPENSEARCH_HOST"}, Env: "INSTA_OPENSEARCH_MODE"}
	d["agent.backend"] = Definition{Definition: manifest.Definition{Default: "codex", Enum: []string{"codex", "claude-code"}, Description: "Coding CLI for lab prompts and interactive sessions (claude-code is an unimplemented stub)"}, Env: "INSTA_AGENT_BACKEND"}
	d["assistant.api_key"] = Definition{Definition: manifest.Definition{Secret: true, Description: "Separate OpenAI API credential for optional limited assistance"}, Env: "INSTA_ASSISTANT_API_KEY"}
	d["assistant.model"] = Definition{Definition: manifest.Definition{Description: "OpenAI Responses model for optional limited assistance"}, Env: "INSTA_ASSISTANT_MODEL"}
	return d
}

func Resolve(m *manifest.Manifest, p *state.Project, secrets map[string]string, lookup func(string) (string, bool)) (map[string]string, map[string]Value, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	values := map[string]string{}
	view := map[string]Value{}
	s := p.Stack()
	defs := Definitions(m)
	for _, k := range manifest.Keys(defs) {
		d := defs[k]
		v, source := d.Default, "default"
		if out, ok := s.Outputs[k]; ok {
			v, source = out, "provider"
		}
		if out, ok := secrets["provider."+k]; ok {
			v, source = out, "provider secret store"
		}
		if saved, ok := s.Config[k]; ok {
			v, source = saved, "stack"
		}
		if saved, ok := secrets[k]; ok {
			v, source = saved, "secret store"
		}
		if d.ShellOnly {
			v, source = "", "unset"
		}
		if shell, ok := lookup(d.Env); ok {
			v, source = shell, "shell"
		}
		if v == "" {
			source = "unset"
		}
		if e := Validate(k, v, d); e != nil {
			return nil, nil, e
		}
		values[k] = v
		display := v
		if d.Secret && v != "" {
			display = "[redacted]"
		}
		view[k] = Value{Key: k, Value: display, Source: source, Secret: d.Secret}
	}
	return values, view, nil
}
func Validate(key, v string, d Definition) error {
	if strings.ContainsAny(v, "\x00\r\n") {
		return fmt.Errorf("%s cannot contain NUL or line breaks", key)
	}
	if v == "" {
		return nil
	}
	if len(d.Enum) > 0 {
		ok := false
		for _, x := range d.Enum {
			if v == x {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("%s must be one of: %s", key, strings.Join(d.Enum, ", "))
		}
	}
	switch d.Type {
	case "integer":
		if _, e := strconv.Atoi(v); e != nil {
			return fmt.Errorf("%s requires an integer", key)
		}
	case "number":
		if _, e := strconv.ParseFloat(v, 64); e != nil {
			return fmt.Errorf("%s requires a number", key)
		}
	case "boolean":
		if v != "true" && v != "false" {
			return fmt.Errorf("%s requires true or false", key)
		}
	}
	if key == "OPENSEARCH_HOST" && (strings.Contains(v, "://") || strings.ContainsAny(v, "/@?# ")) {
		return fmt.Errorf("OPENSEARCH_HOST must be a hostname without scheme, path, or credentials")
	}
	return nil
}
func Require(values map[string]string, keys ...string) error {
	missing := []string{}
	seen := map[string]bool{}
	for _, k := range keys {
		if strings.TrimSpace(values[k]) == "" && !seen[k] {
			missing = append(missing, k)
			seen[k] = true
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required configuration: %s; export the values in your shell or use insta config", strings.Join(missing, ", "))
	}
	return nil
}

// TaskEnvironment strips management credentials, inherits ordinary shell settings,
// and injects only manifest-declared application settings plus the managed runtime.
func TaskEnvironment(m *manifest.Manifest, values map[string]string, root, venv string) []string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "INSTACLUSTR_PROVISION_") || strings.HasPrefix(k, "INSTA_") {
			continue
		}
		env[k] = v
	}
	// Do not manufacture empty variables: Python's os.getenv otherwise loses
	// its per-task defaults, and empty AWS_PROFILE breaks the SDK credential chain.
	for k := range m.Config {
		if v, ok := values[k]; ok && v != "" {
			env[k] = v
		}
	}
	env["VIRTUAL_ENV"] = venv
	env["PATH"] = venv + string(os.PathSeparator) + "bin" + string(os.PathListSeparator) + env["PATH"]
	env["PYTHONUNBUFFERED"] = "1"
	delete(env, "PYTHONOPTIMIZE")
	out := make([]string, 0, len(env))
	for _, k := range manifest.Keys(env) {
		out = append(out, k+"="+env[k])
	}
	return out
}

func SecretValues(m *manifest.Manifest, values map[string]string) []string {
	out := []string{}
	for k, d := range Definitions(m) {
		if d.Secret && values[k] != "" {
			out = append(out, values[k])
		}
	}
	return out
}
