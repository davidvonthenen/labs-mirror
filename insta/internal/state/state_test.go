package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicStateLockAndAncestorDiscovery(t *testing.T) {
	root := t.TempDir()
	p, e := New("rag-retrieval", map[string]string{"commit": "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	if e = Save(root, p); e != nil {
		t.Fatal(e)
	}
	child := filepath.Join(root, "scripts", "task")
	os.MkdirAll(child, 0755)
	found, e := Find(child)
	if e != nil || found != root {
		t.Fatalf("ancestor lookup: %s %v", found, e)
	}
	l, e := Lock(root)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := Lock(root); e == nil {
		other.Close()
		t.Fatal("concurrent lock acquired")
	}
	l.Close()
	l, e = Lock(root)
	if e != nil {
		t.Fatal("lock not released", e)
	}
	l.Close()
	p.Stack().RuntimeStatus = "ready"
	if e = Save(root, p); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(root)
	if e != nil || loaded.Stack().RuntimeStatus != "ready" {
		t.Fatal("atomic state replacement failed", e)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".insta", "project.json"))
	if !json.Valid(b) {
		t.Fatal("invalid state JSON")
	}
	info, _ := os.Stat(filepath.Join(root, ".insta", "project.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("state must be private")
	}
}

func TestSecretsStayOutsideProjectAndRejectLoosePermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, _ := New("rag-retrieval", nil)
	root := t.TempDir()
	Save(root, p)
	if e := SaveSecrets(p.ID, "dev", map[string]string{"OPENAI_API_KEY": "private-value"}); e != nil {
		t.Fatal(e)
	}
	path, e := SecretPath(p.ID, "dev")
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Dir(path) == root {
		t.Fatal("secret store in project")
	}
	s, e := ReadSecrets(p.ID, "dev")
	if e != nil || s["OPENAI_API_KEY"] != "private-value" {
		t.Fatal("secret round trip", e)
	}
	os.Chmod(path, 0644)
	if _, e := ReadSecrets(p.ID, "dev"); e == nil {
		t.Fatal("broad secret permissions accepted")
	}
	if _, e := SecretPath("../escape", "dev"); e == nil {
		t.Fatal("secret path traversal accepted")
	}
}

func TestNoRememberedWorkspaceFallback(t *testing.T) {
	if _, e := Find(t.TempDir()); e == nil {
		t.Fatal("outside-project command found unrelated state")
	}
	if _, e := Load(t.TempDir()); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
