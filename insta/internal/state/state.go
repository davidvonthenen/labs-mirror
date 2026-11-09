// Package state owns durable project metadata, short-lived locks, and local secrets.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Project struct {
	Version       int               `json:"version"`
	ID            string            `json:"id"`
	Lab           string            `json:"lab"`
	Source        map[string]string `json:"source"`
	SelectedStack string            `json:"selected_stack"`
	Stacks        map[string]*Stack `json:"stacks"`
}
type Stack struct {
	Config           map[string]string    `json:"config"`
	Outputs          map[string]string    `json:"outputs"`
	Platforms        map[string]*Platform `json:"platforms"`
	Services         map[string]*Service  `json:"services"`
	Operations       map[string]Operation `json:"operations"`
	ActiveTask       string               `json:"active_task,omitempty"`
	RuntimeDigest    string               `json:"runtime_digest,omitempty"`
	RuntimeStatus    string               `json:"runtime_status"`
	ApprovedManifest string               `json:"approved_manifest,omitempty"`
}
type Platform struct {
	Provider      string    `json:"provider"`
	ID            string    `json:"id,omitempty"`
	Name          string    `json:"name,omitempty"`
	Owned         bool      `json:"owned"`
	Status        string    `json:"status"`
	RequestDigest string    `json:"request_digest,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	Error         string    `json:"error,omitempty"`
}
type Service struct {
	Token     string    `json:"token"`
	Socket    string    `json:"socket"`
	Log       string    `json:"log"`
	PID       int       `json:"pid,omitempty"`
	Owner     string    `json:"owner"`
	StartedAt time.Time `json:"started_at"`
}
type Operation struct {
	Task         string     `json:"task"`
	Name         string     `json:"operation"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Status       string     `json:"status"`
	ExitCode     *int       `json:"exit_code,omitempty"`
	Verification string     `json:"verification"`
	Log          string     `json:"log,omitempty"`
	OutputTail   string     `json:"output_tail,omitempty"`
}

func New(lab string, source map[string]string) (*Project, error) {
	id, e := Token()
	if e != nil {
		return nil, e
	}
	return &Project{Version: 1, ID: id, Lab: lab, Source: source, SelectedStack: "dev", Stacks: map[string]*Stack{"dev": {
		Config: map[string]string{}, Outputs: map[string]string{}, Platforms: map[string]*Platform{}, Services: map[string]*Service{}, Operations: map[string]Operation{}, RuntimeStatus: "pending",
	}}}, nil
}
func Token() (string, error) {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func Find(start string) (string, error) {
	p, e := filepath.Abs(start)
	if e != nil {
		return "", e
	}
	for {
		if info, e := os.Stat(filepath.Join(p, ".insta", "project.json")); e == nil && !info.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return "", errors.New("no insta project in this directory or its ancestors; run insta new <project> --lab rag-retrieval")
}
func Load(root string) (*Project, error) {
	b, e := os.ReadFile(filepath.Join(root, ".insta", "project.json"))
	if e != nil {
		return nil, e
	}
	var p Project
	if e = json.Unmarshal(b, &p); e != nil {
		return nil, fmt.Errorf("invalid project state: %w", e)
	}
	if p.Version != 1 || p.ID == "" || p.Stacks[p.SelectedStack] == nil {
		return nil, errors.New("invalid project state version, identity, or selected stack")
	}
	for _, s := range p.Stacks {
		if s.Config == nil {
			s.Config = map[string]string{}
		}
		if s.Outputs == nil {
			s.Outputs = map[string]string{}
		}
		if s.Platforms == nil {
			s.Platforms = map[string]*Platform{}
		}
		if s.Services == nil {
			s.Services = map[string]*Service{}
		}
		if s.Operations == nil {
			s.Operations = map[string]Operation{}
		}
	}
	return &p, nil
}
func (p *Project) Stack() *Stack { return p.Stacks[p.SelectedStack] }
func Save(root string, p *Project) error {
	return WriteJSON(filepath.Join(root, ".insta", "project.json"), p)
}

// WriteJSON fsyncs before atomic replacement; callers serialize changes with Lock.
func WriteJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	return WriteFile(path, b, 0600)
}
func WriteFile(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e == nil {
		defer d.Close()
		return d.Sync()
	}
	return e
}

type LockFile struct{ file *os.File }

func Lock(root string) (*LockFile, error) {
	dir := filepath.Join(root, ".insta")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("another insta command is changing this project; retry when it finishes")
	}
	return &LockFile{file: f}, nil
}
func (l *LockFile) Close() error {
	if l == nil {
		return nil
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

func UserDir() (string, error) {
	d, e := os.UserConfigDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "insta"), nil
}
func SecretPath(project, stack string) (string, error) {
	if strings.ContainsAny(project+stack, "/\\.") {
		return "", errors.New("invalid secret store identity")
	}
	d, e := UserDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "secrets", project, stack+".json"), nil
}
func ReadSecrets(project, stack string) (map[string]string, error) {
	p, e := SecretPath(project, stack)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if e != nil {
		return nil, e
	}
	info, e := os.Stat(p)
	if e != nil {
		return nil, e
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("secret store permissions are too broad; set its permissions to 0600")
	}
	var values map[string]string
	e = json.Unmarshal(b, &values)
	if values == nil {
		values = map[string]string{}
	}
	return values, e
}
func SaveSecrets(project, stack string, values map[string]string) error {
	p, e := SecretPath(project, stack)
	if e != nil {
		return e
	}
	return WriteJSON(p, values)
}
