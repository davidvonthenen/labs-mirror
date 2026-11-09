package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/instaclustr/labs/insta/internal/config"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/state"
	"github.com/spf13/cobra"
)

func (a *App) addConfig(root *cobra.Command) {
	cmd := &cobra.Command{Use: "config", Short: "Configure declared settings; shell values always take precedence", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if a.JSON {
			return failure("usage", 2, errors.New("interactive config does not support --json; use config list, get, set, or remove"))
		}
		return a.withSession(cmd.Context(), true, func(s *session) error { return s.configure() })
	}}
	cmd.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), false, func(s *session) error {
			items := []config.Value{}
			lines := []string{"KEY\tVALUE\tSOURCE"}
			for _, key := range manifest.Keys(s.View) {
				v := s.View[key]
				items = append(items, v)
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", key, v.Value, v.Source))
			}
			return a.Result(items, strings.Join(lines, "\n"))
		})
	}})
	cmd.AddCommand(&cobra.Command{Use: "get <key>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), false, func(s *session) error {
			v, ok := s.View[args[0]]
			if !ok {
				return unknownKey(args[0])
			}
			return a.Result(v, fmt.Sprintf("%s = %s (%s)", v.Key, v.Value, v.Source))
		})
	}})
	cmd.AddCommand(&cobra.Command{Use: "set <key> [value]", Short: "Set a declared value; secrets are prompted without echo", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			d, ok := config.Definitions(s.Manifest)[args[0]]
			if !ok {
				return unknownKey(args[0])
			}
			if d.ShellOnly {
				return failure("configuration", 2, fmt.Errorf("%s must be exported in the invoking shell; it cannot be stored by insta", args[0]))
			}
			value := ""
			if d.Secret && len(args) > 1 {
				return failure("usage", 2, fmt.Errorf("%s is secret; omit its value to use the hidden prompt, or export %s in your shell", args[0], d.Env))
			}
			if len(args) > 1 {
				value = args[1]
			} else {
				var e error
				value, e = a.Ask(args[0]+": ", d.Secret)
				if e != nil {
					return e
				}
			}
			if e := s.setValue(args[0], value, d); e != nil {
				return e
			}
			message := "Stored " + args[0]
			if s.View[args[0]].Source == "shell" {
				message += "; the current shell value still takes precedence"
			}
			return a.Result(map[string]string{"key": args[0], "status": "stored"}, message)
		})
	}})
	cmd.AddCommand(&cobra.Command{Use: "remove <key>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			d, ok := config.Definitions(s.Manifest)[args[0]]
			if !ok {
				return unknownKey(args[0])
			}
			if d.ShellOnly {
				return failure("configuration", 2, fmt.Errorf("unset %s in the invoking shell", d.Env))
			}
			delete(s.Project.Stack().Config, args[0])
			delete(s.Secrets, args[0])
			if e := state.SaveSecrets(s.Project.ID, s.Project.SelectedStack, s.Secrets); e != nil {
				return e
			}
			if e := s.Save(); e != nil {
				return e
			}
			return a.Result(map[string]string{"key": args[0]}, "Removed stored setting; shell values and defaults still apply.")
		})
	}})
	root.AddCommand(cmd)
}
func unknownKey(key string) error {
	return failure("configuration", 2, fmt.Errorf("unknown setting %q; use insta config list for declared keys", key))
}
func (s *session) setValue(key, value string, d config.Definition) error {
	if e := config.Validate(key, value, d); e != nil {
		return failure("configuration", 2, e)
	}
	if d.Secret {
		s.Secrets[key] = value
		if e := state.SaveSecrets(s.Project.ID, s.Project.SelectedStack, s.Secrets); e != nil {
			return e
		}
	} else {
		s.Project.Stack().Config[key] = value
	}
	return s.Save()
}
func (s *session) configure() error {
	defs := config.Definitions(s.Manifest)
	mode := s.Values["opensearch.mode"]
	if s.View["opensearch.mode"].Source == "default" {
		if s.Values["OPENSEARCH_HOST"] != "" {
			mode = "attach"
		}
		answer, e := s.App.Ask("OpenSearch mode [provision/attach] ("+mode+"): ", false)
		if e != nil {
			return e
		}
		if answer != "" {
			mode = answer
		}
		if e = s.setValue("opensearch.mode", mode, defs["opensearch.mode"]); e != nil {
			return e
		}
		if e = s.Refresh(); e != nil {
			return e
		}
	}
	if mode == "provision" {
		if e := config.Require(s.Values, "INSTACLUSTR_PROVISION_USERNAME", "INSTACLUSTR_PROVISION_API_KEY"); e != nil {
			return failure("configuration", 2, fmt.Errorf("provisioning requires INSTACLUSTR_PROVISION_USERNAME and INSTACLUSTR_PROVISION_API_KEY in your current shell; export them and run insta config again"))
		}
	}
	keys := []string{}
	if mode == "attach" {
		keys = append(keys, "OPENSEARCH_HOST", "OPENSEARCH_USER")
		if _, ok := defs["OPENSEARCH_PASSWORD"]; ok {
			keys = append(keys, "OPENSEARCH_PASSWORD")
		} else {
			keys = append(keys, "OPENSEARCH_PASS")
		}
	}
	// Configure generation only when that provider is selected. Search/MCP keys
	// for later Agentic tasks are requested by their task preflight when needed.
	if s.Values["LLM_PROVIDER"] == "bedrock" {
		keys = append(keys, "BEDROCK_MODEL_ID")
	} else {
		keys = append(keys, "OPENAI_API_KEY", "OPENAI_MODEL")
	}
	keys = append(keys, "HF_TOKEN", "HUGGINGFACE_TOKEN")
	for _, key := range keys {
		d, ok := defs[key]
		if !ok || s.Values[key] != "" {
			continue
		}
		if d.Description != "" {
			fmt.Fprintln(s.App.Err, d.Description)
		}
		v, e := s.App.Ask(key+": ", d.Secret)
		if e != nil {
			return e
		}
		if v == "" {
			return failure("configuration", 2, fmt.Errorf("%s is required for the selected configuration", key))
		}
		if e = s.setValue(key, v, d); e != nil {
			return e
		}
		if e = s.Refresh(); e != nil {
			return e
		}
	}
	return s.App.Result(map[string]string{"stack": s.Project.SelectedStack}, "Configuration saved. Secrets use a private local file outside the project (permissions, not encryption). Run insta up.")
}
