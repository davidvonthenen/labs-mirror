package bootstrap

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureFile struct {
	name, body string
	mode       os.FileMode
}

func makeArchive(t *testing.T, files ...fixtureFile) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, file := range files {
		mode := file.mode
		if mode == 0 {
			mode = 0644
		}
		h := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func extractFixture(t *testing.T, files ...fixtureFile) (string, error) {
	t.Helper()
	dir := t.TempDir()
	z := filepath.Join(dir, "source.zip")
	if err := os.WriteFile(z, makeArchive(t, files...), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	return dest, ExtractZIP(z, dest)
}

func TestExtractPreservesHiddenInstructionsModesAndSafeLinks(t *testing.T) {
	root, err := extractFixture(t,
		fixtureFile{"repo/rag/AGENTS.md", "instructions", 0644},
		fixtureFile{"repo/rag/.agents/skills/example/SKILL.md", "skill", 0644},
		fixtureFile{"repo/rag/support/common/config.py", "configured=True", 0644},
		fixtureFile{"repo/rag/scripts/task/src/common", "../../../support/common", os.ModeSymlink | 0777},
		fixtureFile{"repo/rag/run.sh", "#!/bin/sh\nexit 0\n", 0755},
		fixtureFile{"repo/rag/.env", "TOKEN=do-not-copy", 0644},
		fixtureFile{"repo/rag/.env.example", "TOKEN=", 0644},
		fixtureFile{"repo/rag/.codex/auth.json", "private", 0600},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"rag/AGENTS.md", "rag/.agents/skills/example/SKILL.md", "rag/.env.example"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "rag/scripts/task/src/common/config.py"))
	if err != nil || string(b) != "configured=True" {
		t.Fatalf("safe link: %s %v", b, err)
	}
	info, err := os.Lstat(filepath.Join(root, "rag/scripts/task/src/common"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was not preserved", err)
	}
	info, err = os.Stat(filepath.Join(root, "rag/run.sh"))
	if err != nil || info.Mode().Perm()&0111 == 0 {
		t.Fatal("executable mode missing", err)
	}
	for _, p := range []string{"rag/.env", "rag/.codex/auth.json"} {
		if _, err := os.Lstat(filepath.Join(root, p)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("credential copied: %s", p)
		}
	}
}

func TestRejectUnsafeArchives(t *testing.T) {
	cases := map[string][]fixtureFile{
		"traversal":            {{"repo/../escape", "bad", 0644}},
		"absolute":             {{"/repo/escape", "bad", 0644}},
		"backslash":            {{"repo/a\\..\\escape", "bad", 0644}},
		"drive":                {{"repo/C:/escape", "bad", 0644}},
		"multiple roots":       {{"repo/a", "a", 0644}, {"other/b", "b", 0644}},
		"symlink escape":       {{"repo/a", "../../escape", os.ModeSymlink | 0777}},
		"symlink absolute":     {{"repo/a", "/etc/passwd", os.ModeSymlink | 0777}},
		"symlink parent":       {{"repo/a", "target", os.ModeSymlink | 0777}, {"repo/a/file", "bad", 0644}, {"repo/target/file", "ok", 0644}},
		"symlink chain escape": {{"repo/a", "b", os.ModeSymlink | 0777}, {"repo/b", "../../escape", os.ModeSymlink | 0777}},
		"symlink cycle":        {{"repo/a", "b", os.ModeSymlink | 0777}, {"repo/b", "a", os.ModeSymlink | 0777}},
		"dangling symlink":     {{"repo/a", "missing", os.ModeSymlink | 0777}},
		"duplicate":            {{"repo/a", "one", 0644}, {"repo/a", "two", 0644}},
		"file parent":          {{"repo/a", "file", 0644}, {"repo/a/b", "child", 0644}},
		"device":               {{"repo/device", "", os.ModeDevice | 0600}},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dest, err := extractFixture(t, files...)
			if err == nil {
				t.Fatal("accepted unsafe archive")
			}
			if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed extraction left a tree", err)
			}
		})
	}
}

func TestRejectDeclaredOversizedArchive(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "repo/a", Method: zip.Store, UncompressedSize64: MaxFileBytes + 1, CompressedSize64: 0}
	h.SetMode(0644)
	f, err := w.CreateRaw(h)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(nil)
	w.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "large.zip")
	os.WriteFile(p, b.Bytes(), 0600)
	if err := ExtractZIP(p, filepath.Join(dir, "out")); err == nil {
		t.Fatal("accepted oversized file")
	}
}

func TestCopyLabAtomicAndSeparate(t *testing.T) {
	root, err := extractFixture(t, fixtureFile{"repo/rag/AGENTS.md", "source", 0644}, fixtureFile{"repo/rag/common/config.py", "shared", 0644}, fixtureFile{"repo/rag/src/common", "../common", os.ModeSymlink | 0777})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &Snapshot{Root: root}
	lab := Lab{ID: "rag", SourcePath: "rag"}
	dest := filepath.Join(t.TempDir(), "my-rag")
	if err := CopyLab(snapshot, lab, dest, func(stage string) error {
		return os.WriteFile(filepath.Join(stage, "insta.yaml"), []byte("id: rag"), 0644)
	}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dest, "AGENTS.md"), []byte("edited"), 0644)
	b, _ := os.ReadFile(filepath.Join(root, "rag/AGENTS.md"))
	if string(b) != "source" {
		t.Fatal("workspace mutated source")
	}
	info, _ := os.Lstat(filepath.Join(dest, "src/common"))
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("copy lost link")
	}
	if err := CopyLab(snapshot, lab, dest, nil); err == nil {
		t.Fatal("overwrote existing project")
	}
	if err := CopyLab(snapshot, lab, filepath.Join(t.TempDir(), "failed"), func(string) error { return errors.New("overlay rejected") }); err == nil {
		t.Fatal("ignored overlay failure")
	}
	outside, err := extractFixture(t, fixtureFile{"repo/rag/x", "../other", os.ModeSymlink | 0777}, fixtureFile{"repo/other/y", "shared", 0644})
	if err != nil {
		t.Fatal(err)
	}
	if err := CopyLab(&Snapshot{Root: outside}, lab, filepath.Join(t.TempDir(), "unsafe"), nil); err == nil {
		t.Fatal("copied undeclared shared dependency")
	}
}

func fixtureSource(t *testing.T, handler http.HandlerFunc) (*Source, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/test/labs/commits/") {
			fmt.Fprintf(w, `{"sha":"%s"}`, strings.Repeat("a", 40))
			return
		}
		handler(w, r)
	}))
	s := &Source{
		Repository: Repository{Origin: "https://github.com/test/labs", Branch: "main", APIURL: server.URL, ArchiveURL: server.URL + "/archive"},
		CacheDir:   t.TempDir(), HTTP: server.Client(),
	}
	return s, server
}
func pinnedArchive(t *testing.T) []byte {
	return makeArchive(t, fixtureFile{"labs-" + strings.Repeat("a", 40) + "/rag-retrieval/AGENTS.md", "pinned", 0644})
}

func TestDownloadCacheReuseAndTamper(t *testing.T) {
	archive := pinnedArchive(t)
	var requests atomic.Int32
	s, server := fixtureSource(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(archive) })
	lab, _ := Lookup("rag-retrieval")
	snap, err := s.Resolve(context.Background(), lab)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Provenance.Commit != strings.Repeat("a", 40) || snap.Provenance.RefType != "heads" || snap.Provenance.Ref != "main" || snap.Provenance.SHA256 == "" || snap.Provenance.SourcePath != lab.SourcePath {
		t.Fatal("missing provenance")
	}
	snap.Close()
	server.Close()
	snap, err = s.Resolve(context.Background(), lab)
	if err != nil {
		t.Fatal("offline verified cache failed", err)
	}
	if !snap.Provenance.OfflineCache {
		t.Fatal("offline reuse did not mark stale-ref provenance")
	}
	snap.Close()
	if requests.Load() != 1 {
		t.Fatal("cache caused download")
	}
	paths, _ := filepath.Glob(filepath.Join(s.CacheDir, "*.zip"))
	os.Chmod(paths[0], 0600)
	os.WriteFile(paths[0], []byte("tampered"), 0600)
	if _, err := s.Resolve(context.Background(), lab); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatal("tampered archive accepted", err)
	}
}

func TestDownloadFailuresNeverPublishCache(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http error":  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
		"bad archive": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not a zip")) },
		"interrupted": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10000")
			w.Write([]byte("partial"))
		},
		"oversized response": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(MaxArchiveBytes+1))
		},
		"wrong revision": func(w http.ResponseWriter, r *http.Request) {
			w.Write(makeArchive(t, fixtureFile{"labs-main/rag-retrieval/AGENTS.md", "moving source", 0644}))
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			s, server := fixtureSource(t, h)
			defer server.Close()
			lab, _ := Lookup("rag-retrieval")
			if _, err := s.Resolve(context.Background(), lab); err == nil {
				t.Fatal("download unexpectedly succeeded")
			}
			paths, _ := filepath.Glob(filepath.Join(s.CacheDir, "*.json"))
			if len(paths) != 0 {
				t.Fatal("published failed cache")
			}
		})
	}
}

func TestCanceledDownloadAndCacheLock(t *testing.T) {
	s, server := fixtureSource(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	lab, _ := Lookup("rag-retrieval")
	if _, err := s.Resolve(ctx, lab); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancellation lost", err)
	}
	lock := filepath.Join(t.TempDir(), "lock")
	unlock, err := lockCache(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := lockCache(ctx, lock); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock cancellation lost", err)
	}
}

func TestConcurrentResolveDownloadsOnce(t *testing.T) {
	archive := pinnedArchive(t)
	var requests atomic.Int32
	s, server := fixtureSource(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(archive) })
	defer server.Close()
	lab, _ := Lookup("rag-retrieval")
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, err := s.Resolve(context.Background(), lab)
			if snap != nil {
				snap.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("downloads=%d", requests.Load())
	}
}

func TestCatalogAndInvalidRef(t *testing.T) {
	if len(Catalog()) != 2 {
		t.Fatal("catalog drift")
	}
	if _, err := Lookup("rag-retrieve"); err == nil {
		t.Fatal("accepted old alias")
	}
	s := &Source{Repository: DefaultRepository, CacheDir: t.TempDir()}
	s.Repository.Branch = "bad..branch"
	if _, err := s.Resolve(context.Background(), Catalog()[0]); err == nil {
		t.Fatal("accepted invalid GitHub branch")
	}
	s.Repository.Branch = "invalid branch"
	s.Repository.Tag = "v1.0.0"
	if err := validateRepository(s.Repository); err != nil {
		t.Fatal("tag should take precedence over the unused branch", err)
	}
	s.Repository.Tag = "bad?tag"
	if err := validateRepository(s.Repository); err == nil {
		t.Fatal("accepted invalid GitHub tag")
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDefaultMainDownloadsResolvedCommit(t *testing.T) {
	commit := strings.Repeat("a", 40)
	archive := makeArchive(t, fixtureFile{"labs-" + commit + "/rag-retrieval/AGENTS.md", "source", 0644})
	requests := []string{}
	client := &http.Client{Transport: fixtureTransport(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		var data []byte
		switch req.URL.String() {
		case "https://api.github.com/repos/test/labs/commits/heads/main":
			data = []byte(`{"sha":"` + commit + `"}`)
		case "https://codeload.github.com/test/labs/zip/" + commit:
			data = archive
		default:
			return nil, fmt.Errorf("unexpected HTTP request: %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Header: make(http.Header), Request: req}, nil
	})}
	s := &Source{Repository: Repository{Origin: "https://github.com/test/labs"}, CacheDir: t.TempDir(), HTTP: client}
	lab, _ := Lookup("rag-retrieval")
	snap, err := s.Resolve(context.Background(), lab)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if snap.Provenance.RefType != "heads" || snap.Provenance.Ref != "main" || snap.Provenance.Commit != commit || len(requests) != 2 {
		t.Fatal("default branch or resolved archive URL incorrect", snap.Provenance, requests)
	}
}

func TestBranchMovesAndTagWins(t *testing.T) {
	first, second := strings.Repeat("a", 40), strings.Repeat("b", 40)
	var current atomic.Value
	current.Store(first)
	var archiveRequests atomic.Int32
	var apiPaths []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/test/labs/commits/") {
			mu.Lock()
			apiPaths = append(apiPaths, r.URL.Path)
			mu.Unlock()
			fmt.Fprintf(w, `{"sha":"%s"}`, current.Load().(string))
			return
		}
		archiveRequests.Add(1)
		w.Write(makeArchive(t, fixtureFile{"labs-" + current.Load().(string) + "/rag-retrieval/AGENTS.md", "safe", 0644}))
	}))
	defer server.Close()
	s := &Source{Repository: Repository{Origin: "https://github.com/test/labs", Branch: "feature/demo", APIURL: server.URL, ArchiveURL: server.URL + "/archive"}, CacheDir: t.TempDir(), HTTP: server.Client()}
	lab, _ := Lookup("rag-retrieval")
	for _, want := range []string{first, second, second} {
		current.Store(want)
		snap, err := s.Resolve(context.Background(), lab)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Provenance.Commit != want || snap.Provenance.RefType != "heads" || snap.Provenance.Ref != "feature/demo" || snap.Provenance.OfflineCache {
			t.Fatal("resolved branch provenance mismatch", snap.Provenance)
		}
		snap.Close()
	}
	if archiveRequests.Load() != 2 {
		t.Fatalf("moving branch should be downloaded twice, got %d", archiveRequests.Load())
	}
	mu.Lock()
	for _, p := range apiPaths {
		if p != "/repos/test/labs/commits/heads/feature/demo" {
			t.Fatalf("unexpected branch lookup: %s", p)
		}
	}
	mu.Unlock()

	// The branch is intentionally malformed: selection/validation must ignore it
	// once Tag is defined.
	s.Repository.Branch = "invalid branch"
	s.Repository.Tag = "v2.0.0"
	snap, err := s.Resolve(context.Background(), lab)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Provenance.RefType != "tags" || snap.Provenance.Ref != "v2.0.0" || snap.Provenance.Commit != second {
		t.Fatal("tag did not win", snap.Provenance)
	}
	snap.Close()
	mu.Lock()
	last := apiPaths[len(apiPaths)-1]
	mu.Unlock()
	if last != "/repos/test/labs/commits/tags/v2.0.0" {
		t.Fatal("tag endpoint was not selected", last)
	}
}

func TestMissingTagDoesNotUseCachedSource(t *testing.T) {
	sha := strings.Repeat("a", 40)
	var missing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/test/labs/commits/") {
			if missing.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprintf(w, `{"sha":"%s"}`, sha)
			return
		}
		io.WriteString(w, string(makeArchive(t, fixtureFile{"labs-" + sha + "/rag-retrieval/AGENTS.md", "safe", 0644})))
	}))
	defer server.Close()
	s := &Source{Repository: Repository{Origin: "https://github.com/test/labs", Tag: "v1", APIURL: server.URL, ArchiveURL: server.URL + "/archive"}, CacheDir: t.TempDir(), HTTP: server.Client()}
	lab, _ := Lookup("rag-retrieval")
	snap, err := s.Resolve(context.Background(), lab)
	if err != nil {
		t.Fatal(err)
	}
	snap.Close()
	missing.Store(true)
	if _, err := s.Resolve(context.Background(), lab); !errors.Is(err, errRefNotFound) {
		t.Fatalf("deleted tag used stale cache: %v", err)
	}
}

// This opt-in check verifies the downloaded maintainer source without contacting
// any live service. The archive remains outside the source release package.
func TestPinnedSourceArchiveOptional(t *testing.T) {
	archive := os.Getenv("INSTA_TEST_SOURCE_ARCHIVE")
	if archive == "" {
		t.Skip("set INSTA_TEST_SOURCE_ARCHIVE to audit the pinned GitHub ZIP")
	}
	source := &Source{Repository: DefaultRepository}
	root := filepath.Join(t.TempDir(), "source")
	if err := source.extract(archive, root, "c321a65c9586706d8696645327e28b07f6b78b77"); err != nil {
		t.Fatal(err)
	}
	for _, lab := range Catalog() {
		t.Run(lab.ID, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "project")
			if err := CopyLab(&Snapshot{Root: root}, lab, dest, nil); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"README.md", "support/requirements.txt", "tasks", "scripts"} {
				if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
