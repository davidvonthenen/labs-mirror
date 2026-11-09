package templates

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instaclustr/labs/insta/internal/bootstrap"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"gopkg.in/yaml.v3"
)

func TestOverlayIdempotentAndUnknownLab(t *testing.T) {
	for _, lab := range []string{"rag-retrieval", "agentic-rag"} {
		t.Run(lab, func(t *testing.T) {
			root := t.TempDir()
			if err := Apply(root, lab); err != nil {
				t.Fatal(err)
			}
			if err := Apply(root, lab); err != nil {
				t.Fatal("identical overlay not reusable", err)
			}
			for _, name := range []string{"insta.yaml", "insta.schema.json", "deploy-opensearch.json", "AGENTS.md", "INSTA.md"} {
				if _, err := os.Stat(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	if err := Apply(t.TempDir(), "other"); err == nil {
		t.Fatal("unknown lab accepted")
	}
}

func TestOverlayPreservesChangedUpstreamFiles(t *testing.T) {
	for _, name := range []string{"README.md", "AGENTS.md", "insta.yaml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			original := []byte("learner's existing work\n")
			os.WriteFile(filepath.Join(root, name), original, 0644)
			if err := Apply(root, "rag-retrieval"); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(root, name))
			if !bytes.Equal(data, original) {
				t.Fatal("changed original file")
			}
			if _, err := os.Stat(filepath.Join(root, "insta.schema.json")); err != nil {
				t.Fatal("missing integration was not installed", err)
			}
		})
	}
}

func TestOverlayRejectsSymlinkDestination(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "references"))
	if err := Apply(root, "rag-retrieval"); err == nil {
		t.Fatal("followed symlink parent")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside project")
	}
	root = t.TempDir()
	os.WriteFile(filepath.Join(outside, "manifest"), []byte("original"), 0600)
	os.Symlink(filepath.Join(outside, "manifest"), filepath.Join(root, "insta.yaml"))
	if err := Apply(root, "rag-retrieval"); err == nil {
		t.Fatal("followed symlink file")
	}
}

func TestOverlayContainsNoApplicationReplacement(t *testing.T) {
	err := fs.WalkDir(files, "overlays", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.Contains(name, "/src/") || strings.Contains(name, "/support/common/") {
			t.Errorf("application replacement in additive overlay: %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchemaAndBundledManifestStructure(t *testing.T) {
	b, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatal("schema dialect missing")
	}
	for _, id := range []string{"rag-retrieval", "agentic-rag"} {
		b, err := Manifest(id)
		if err != nil {
			t.Fatal(err)
		}
		var m manifest.Manifest
		d := yaml.NewDecoder(bytes.NewReader(b))
		d.KnownFields(true)
		if err := d.Decode(&m); err != nil {
			t.Fatal(id, err)
		}
		if m.ID != id || m.SchemaVersion != 1 || m.Runtime.Python != "3.12" {
			t.Fatal("manifest identity/runtime mismatch", id)
		}
		if _, ok := m.Config["INSTACLUSTR_PROVISION_API_KEY"]; ok {
			t.Fatal("management credential leaked into lab environment contract")
		}
		if id == "rag-retrieval" && m.Tasks["baseline-vector-rag"].Operations["query"].Mode != "oneshot" {
			t.Fatal("the built-in sample queries must run without a terminal")
		}
		if id == "agentic-rag" {
			if len(m.Tasks) != 5 {
				t.Fatal("agentic teaching task missing")
			}
			if _, ok := m.Config["OPENSEARCH_PASS"]; !ok {
				t.Fatal("actual agentic password key missing")
			}
			if m.Config["OPENSEARCH_INDEX"].Default != "" {
				t.Fatal("shared index override erased per-agent defaults")
			}
			if len(m.Lifecycle.Up) != 1 || m.Lifecycle.Up[0].Platform != "opensearch" {
				t.Fatal("up runs educational agent services")
			}
			for _, task := range []string{"news-agent", "financials-agent"} {
				if len(m.Tasks[task].Operations["ingest"].Requires.Services) != 0 {
					t.Fatal("ingestion requires query services")
				}
			}
		}
	}
}

func TestPinnedSourcesAndManifestsOptional(t *testing.T) {
	archive := os.Getenv("INSTA_TEST_SOURCE_ARCHIVE")
	if archive == "" {
		t.Skip("set INSTA_TEST_SOURCE_ARCHIVE to validate both overlays against pinned source")
	}
	root := filepath.Join(t.TempDir(), "source")
	if err := bootstrap.ExtractZIP(archive, root); err != nil {
		t.Fatal(err)
	}
	for _, lab := range bootstrap.Catalog() {
		t.Run(lab.ID, func(t *testing.T) {
			project := filepath.Join(t.TempDir(), "project")
			err := bootstrap.CopyLab(&bootstrap.Snapshot{Root: root}, lab, project, func(stage string) error { return Apply(stage, lab.ID) })
			if err != nil {
				t.Fatal(err)
			}
			m, _, err := manifest.Load(project)
			if err != nil {
				t.Fatal(err)
			}
			for _, service := range m.Services {
				checkPythonTarget(t, project, service.Cwd, service.Command)
			}
			for _, task := range m.Tasks {
				for _, op := range task.Operations {
					cwd := op.Cwd
					if cwd == "" {
						cwd = task.Source
					}
					checkPythonTarget(t, project, cwd, op.Command)
				}
			}
			// Every original application file/link must survive byte-for-byte.
			source := filepath.Join(root, lab.SourcePath)
			err = filepath.WalkDir(source, func(p string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(source, p)
				if !strings.Contains(filepath.ToSlash(rel), "/src/") && !strings.HasPrefix(filepath.ToSlash(rel), "support/common/") {
					return nil
				}
				info, err := os.Lstat(p)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					before, err := os.Readlink(p)
					if err != nil {
						return err
					}
					after, err := os.Readlink(filepath.Join(project, rel))
					if err != nil || before != after {
						t.Errorf("changed source link %s", rel)
					}
					return nil
				}
				before, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				after, err := os.ReadFile(filepath.Join(project, rel))
				if err != nil {
					return err
				}
				if !bytes.Equal(before, after) {
					t.Errorf("changed application %s", rel)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkPythonTarget(t *testing.T, root, cwd string, args []string) {
	t.Helper()
	if len(args) == 0 || args[0] != "python" {
		return
	}
	if len(args) < 2 {
		t.Fatal("python command has no target")
	}
	if args[1] == "-m" {
		if len(args) < 3 {
			t.Fatal("python -m has no module")
		}
		if args[2] == "compileall" {
			return
		}
		module := filepath.Join(root, cwd, filepath.FromSlash(strings.ReplaceAll(args[2], ".", "/")))
		if _, err := os.Stat(module + ".py"); err == nil {
			return
		}
		if _, err := os.Stat(filepath.Join(module, "__main__.py")); err == nil {
			return
		}
		t.Errorf("declared module %s missing in %s", args[2], cwd)
		return
	}
	if strings.HasPrefix(args[1], "-") {
		t.Errorf("unaudited Python target %v", args)
		return
	}
	if _, err := os.Stat(filepath.Join(root, cwd, args[1])); err != nil {
		t.Errorf("declared script missing: %v", err)
	}
}
