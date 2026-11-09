package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/instaclustr/labs/insta/internal/bootstrap"
	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/state"
	"github.com/instaclustr/labs/insta/internal/templates"
	"github.com/spf13/cobra"
)

func (a *App) addCatalog(root *cobra.Command) {
	lab := &cobra.Command{Use: "lab", Short: "Select the template for a future project"}
	lab.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		items := bootstrap.Catalog()
		var lines []string
		lines = append(lines, "Welcome to the `insta` CLI!")
		lines = append(lines, "Would you like to explore or modify one of the Instaclustr labs?")
		lines = append(lines, "Please choose a lab from the list below:")
		lines = append(lines, " ")
		lines = append(lines, "Lab ID\t\tLab Description")
		lines = append(lines, "---\t\t---")
		for _, x := range items {
			lines = append(lines, x.ID+"\t"+x.Name)
		}
		lines = append(lines, "\n")
		return a.Result(items, strings.Join(lines, "\n"))
	}})
	lab.AddCommand(&cobra.Command{Use: "select <lab>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		l, e := bootstrap.Lookup(args[0])
		if e != nil {
			return failure("configuration", 2, e)
		}
		d, e := state.UserDir()
		if e != nil {
			return e
		}
		if e = state.WriteJSON(filepath.Join(d, "catalog.json"), map[string]string{"lab": l.ID}); e != nil {
			return e
		}
		return a.Result(map[string]string{"lab": l.ID}, "Default template: "+l.ID+" (existing projects are unchanged)")
	}})
	root.AddCommand(lab)
}
func defaultLab() string {
	d, e := state.UserDir()
	if e != nil {
		return ""
	}
	b, e := os.ReadFile(filepath.Join(d, "catalog.json"))
	if e != nil {
		return ""
	}
	var p map[string]string
	if json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p["lab"]
}

func (a *App) addNew(root *cobra.Command) {
	var labID string
	cmd := &cobra.Command{Use: "new <project>", Short: "Download a lab and prepare its managed Python environment", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		chosen := labID
		if chosen == "" {
			chosen = defaultLab()
		}
		if chosen == "" {
			answer, e := a.Ask("Lab [rag-retrieval / agentic-rag]: ", false)
			if e != nil {
				return failure("input_required", 2, errors.New("choose a lab with --lab rag-retrieval or --lab agentic-rag"))
			}
			chosen = answer
		}
		lab, e := bootstrap.Lookup(chosen)
		if e != nil {
			return failure("configuration", 2, e)
		}
		destination, e := projectRootPath(args[0])
		if e != nil {
			return failure("usage", 2, e)
		}
		if _, e := os.Lstat(destination); e == nil {
			return failure("destination_exists", 2, fmt.Errorf("destination %s already exists; choose a new project name", destination))
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		source := a.Source
		if source == nil {
			source, e = bootstrap.NewSource()
			if e != nil {
				return e
			}
		}
		fmt.Fprintln(a.Err, "Resolving the GitHub lab reference and verified source archive...")
		snapshot, e := source.Resolve(cmd.Context(), lab)
		if e != nil {
			return failure("download", 5, e)
		}
		defer snapshot.Close()
		if snapshot.Provenance.OfflineCache {
			fmt.Fprintf(a.Err, "GitHub lookup unavailable; using verified cached %s %q at commit %s.\n", snapshot.Provenance.RefType, snapshot.Provenance.Ref, snapshot.Provenance.Commit)
		}
		var p *state.Project
		e = bootstrap.CopyLab(snapshot, lab, destination, func(stage string) error {
			if e := templates.Apply(stage, lab.ID); e != nil {
				return e
			}
			m, h, e := manifest.Load(stage)
			if e != nil {
				return e
			}
			b, e := json.Marshal(snapshot.Provenance)
			if e != nil {
				return e
			}
			origin := map[string]string{}
			if e = json.Unmarshal(b, &origin); e != nil {
				return e
			}
			origin["cli_version"] = Version
			origin["overlay_sha256"], e = templates.Digest(lab.ID)
			if e != nil {
				return e
			}
			p, e = state.New(lab.ID, origin)
			if e != nil {
				return e
			}
			p.Stack().ApprovedManifest = h
			for k, d := range m.Config {
				if strings.HasPrefix(k, "OPENSEARCH_") && strings.HasSuffix(k, "INDEX") && d.Default != "" {
					p.Stack().Config[k] = d.Default + "-" + p.ID[:8] + "-dev"
				}
			}
			if e = state.Save(stage, p); e != nil {
				return e
			}
			if e = addIgnoreRules(stage); e != nil {
				return e
			}
			// The legacy readiness checker requires an .env file. Keep this
			// compatibility marker empty of settings; resolved values come from Go.
			f, e := os.OpenFile(filepath.Join(stage, ".env"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				if errors.Is(e, os.ErrExist) {
					return nil
				}
				return e
			}
			_, e = f.WriteString("# Configuration is supplied by insta from your shell and selected stack.\n# This compatibility marker contains no credentials.\n")
			ce := f.Close()
			if e != nil {
				return e
			}
			return ce
		})
		if e != nil {
			return failure("bootstrap", 3, e)
		}
		fmt.Fprintf(a.Err, "Project created at %s\nPreparing the managed Python runtime...\n", destination)
		var runtimeErr error
		e = a.atSession(cmd.Context(), destination, true, func(s *session) error { runtimeErr = s.EnsureRuntime(cmd.Context()); return nil })
		if e != nil {
			return e
		}
		adapter := a.CodingCLI
		if adapter == nil {
			adapter = coding.NewCodex()
		}
		ready, probeErr := adapter.Probe(cmd.Context())
		if probeErr != nil || !ready.Configured {
			fmt.Fprintln(a.Err, "Codex is unavailable or not configured; install it and sign in for the best guided learning and coding experience. Basic lab commands still work, and separately configured API assistance is available through insta exec -i.")
			if ready.Diagnostic != "" {
				fmt.Fprintln(a.Err, ready.Diagnostic)
			}
		}
		if runtimeErr != nil {
			return failure("bootstrap_incomplete", 3, fmt.Errorf("project was created, but runtime setup is incomplete: %w; cd %s and run insta config, then insta up to retry", runtimeErr, args[0]))
		}
		return a.Result(map[string]any{"project": destination, "lab": chosen, "stack": "dev", "source": snapshot.Provenance, "runtime": "ready"}, fmt.Sprintf("Created %s from %s\n\nNext:\n  cd %s\n  insta config\n  insta up\n  insta exec", args[0], chosen, args[0]))
	}}
	cmd.Flags().StringVar(&labID, "lab", "", "Lab template: rag-retrieval or agentic-rag")
	root.AddCommand(cmd)
}
func addIgnoreRules(root string) error {
	path := filepath.Join(root, ".gitignore")
	b, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	text := string(b)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "\n# insta managed state and local credentials\n"
	for _, rule := range []string{".insta/", ".venv/", ".env", ".env.*", "!.env.example"} {
		text += rule + "\n"
	}
	return os.WriteFile(path, []byte(text), 0644)
}
