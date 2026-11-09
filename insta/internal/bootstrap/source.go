package bootstrap

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	MaxArchiveBytes  = 128 << 20
	MaxExpandedBytes = 512 << 20
	MaxFileBytes     = 64 << 20
	MaxFiles         = 20000
)

var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Provenance describes the resolved commit and source bytes. A locally computed
// digest checks cache integrity; it is not an independent publisher signature.
type Provenance struct {
	Origin       string `json:"origin"`
	RefType      string `json:"ref_type"`
	Ref          string `json:"ref"`
	Commit       string `json:"commit"`
	SourcePath   string `json:"source_path"`
	SHA256       string `json:"sha256"`
	OfflineCache bool   `json:"offline_cache,omitempty"`
}

type Source struct {
	Repository Repository
	CacheDir   string
	HTTP       *http.Client
}

// Snapshot owns a temporary extracted tree. Close after CopyLab finishes.
type Snapshot struct {
	Root       string
	Provenance Provenance
	parent     string
}

func (s *Snapshot) Close() error {
	if s.parent == "" {
		return nil
	}
	return os.RemoveAll(s.parent)
}

func NewSource() (*Source, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("find user cache: %w", err)
	}
	return &Source{Repository: DefaultRepository, CacheDir: filepath.Join(base, "insta", "sources")}, nil
}

// reference returns the effective repository ref. A tag always wins, even when
// Branch also has a value.
func (r Repository) reference() (kind, name string) {
	if r.Tag != "" {
		return "tags", r.Tag
	}
	if r.Branch != "" {
		return "heads", r.Branch
	}
	return "heads", "main"
}

func validRef(ref string) bool {
	if len(ref) > 200 || !refPattern.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "." || part == "" || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func (r Repository) ownerRepo() (string, error) {
	u, err := url.Parse(r.Origin)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("catalog origin must be a GitHub HTTPS repository")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || !validRef(parts[0]) || !validRef(parts[1]) {
		return "", errors.New("catalog origin must contain a GitHub owner and repository")
	}
	return parts[0] + "/" + parts[1], nil
}

func (s *Source) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many source redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("source redirect must use HTTPS")
		}
		return nil
	}}
}

// Resolve checks GitHub for the current branch/tag commit on each call. Cached
// archives are keyed by ref but only reused when their commit matches. If the
// API is unreachable, a previously verified archive can be reused offline.
func (s *Source) Resolve(ctx context.Context, lab Lab) (*Snapshot, error) {
	if err := validateRepository(s.Repository); err != nil {
		return nil, err
	}
	if !safeRelative(lab.SourcePath) {
		return nil, errors.New("invalid lab source path")
	}
	if s.CacheDir == "" {
		return nil, errors.New("source cache directory is required")
	}
	if err := os.MkdirAll(s.CacheDir, 0700); err != nil {
		return nil, err
	}
	kind, ref := s.Repository.reference()
	key := sha256.Sum256([]byte(s.Repository.Origin + "\n" + kind + "\n" + ref))
	base := filepath.Join(s.CacheDir, hex.EncodeToString(key[:]))
	unlock, err := lockCache(ctx, base+".lock")
	if err != nil {
		return nil, err
	}
	defer unlock()
	archive, meta := base+".zip", base+".json"
	cached, cacheErr := s.cached(archive, meta, kind, ref)
	if cacheErr != nil && !errors.Is(cacheErr, os.ErrNotExist) {
		return nil, cacheErr // A corrupt cache must never be silently trusted.
	}
	commit, err := s.resolveCommit(ctx, kind, ref)
	var provenance Provenance
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, errRefNotFound) || cacheErr != nil {
			return nil, err
		}
		provenance = cached
		provenance.OfflineCache = true
	} else if cacheErr == nil && cached.Commit == commit {
		provenance = cached
	} else {
		provenance, err = s.download(ctx, archive, meta, kind, ref, commit)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parent, err := os.MkdirTemp(s.CacheDir, "snapshot-")
	if err != nil {
		return nil, err
	}
	root := filepath.Join(parent, "source")
	if err := s.extract(archive, root, provenance.Commit); err != nil {
		os.RemoveAll(parent)
		return nil, fmt.Errorf("invalid source archive: %w", err)
	}
	labPath := filepath.Join(root, filepath.FromSlash(lab.SourcePath))
	info, err := os.Lstat(labPath)
	if err != nil || !info.IsDir() {
		os.RemoveAll(parent)
		return nil, fmt.Errorf("source commit %s does not contain lab directory %q", provenance.Commit, lab.SourcePath)
	}
	provenance.SourcePath = lab.SourcePath
	return &Snapshot{Root: root, Provenance: provenance, parent: parent}, nil
}

var errRefNotFound = errors.New("GitHub branch or tag does not exist")

func (s *Source) resolveCommit(ctx context.Context, kind, ref string) (string, error) {
	ownerRepo, err := s.Repository.ownerRepo()
	if err != nil {
		return "", err
	}
	base := s.Repository.APIURL
	if base == "" {
		base = "https://api.github.com"
	}
	endpoint := strings.TrimRight(base, "/") + "/repos/" + ownerRepo + "/commits/" + kind + "/" + ref
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "insta-cli")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve GitHub %s %q: %w", kind, ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%w: %s %q", errRefNotFound, kind, ref)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve GitHub %s %q: HTTP %d", kind, ref, resp.StatusCode)
	}
	var result struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("parse GitHub commit for %s %q: %w", kind, ref, err)
	}
	if !commitPattern.MatchString(result.SHA) {
		return "", errors.New("GitHub returned an invalid commit ID")
	}
	return result.SHA, nil
}

func (s *Source) cached(archive, meta, kind, ref string) (Provenance, error) {
	var p Provenance
	b, err := os.ReadFile(meta)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("invalid source cache metadata; remove %s and retry", meta)
	}
	if p.Origin != s.Repository.Origin || p.RefType != kind || p.Ref != ref || !commitPattern.MatchString(p.Commit) || len(p.SHA256) != 64 {
		return p, errors.New("source cache provenance does not match catalog reference")
	}
	digest, err := archiveDigest(archive)
	if err != nil {
		return p, err
	}
	if digest != p.SHA256 {
		return p, fmt.Errorf("source cache checksum mismatch; remove %s and retry", archive)
	}
	p.OfflineCache = false
	return p, nil
}

func (s *Source) download(ctx context.Context, archive, meta, kind, ref, commit string) (Provenance, error) {
	p := Provenance{Origin: s.Repository.Origin, RefType: kind, Ref: ref, Commit: commit}
	archiveURL := s.Repository.ArchiveURL
	if archiveURL == "" {
		ownerRepo, err := s.Repository.ownerRepo()
		if err != nil {
			return p, err
		}
		archiveURL = "https://codeload.github.com/" + ownerRepo + "/zip/" + commit
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return p, err
	}
	req.Header.Set("User-Agent", "insta-cli")
	resp, err := s.client().Do(req)
	if err != nil {
		return p, fmt.Errorf("download source at commit %s: %w", commit, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("source archive returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxArchiveBytes {
		return p, errors.New("source archive exceeds download limit")
	}
	f, err := os.CreateTemp(s.CacheDir, ".download-")
	if err != nil {
		return p, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, MaxArchiveBytes+1))
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil {
		return p, fmt.Errorf("download interrupted: %w", copyErr)
	}
	if closeErr != nil {
		return p, closeErr
	}
	if n > MaxArchiveBytes {
		return p, errors.New("source archive exceeds download limit")
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return p, errors.New("incomplete source archive download")
	}
	checkDir, err := os.MkdirTemp(s.CacheDir, ".verify-")
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(checkDir)
	if err := s.extract(tmp, filepath.Join(checkDir, "source"), commit); err != nil {
		return p, fmt.Errorf("invalid source archive: %w", err)
	}
	p.SHA256 = hex.EncodeToString(h.Sum(nil))
	if err := os.Chmod(tmp, 0444); err != nil {
		return p, err
	}
	if err := os.Rename(tmp, archive); err != nil {
		return p, err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return p, err
	}
	if err := writeCacheMetadata(meta, b); err != nil {
		return p, err
	}
	return p, nil
}

func archiveDigest(file string) (string, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxArchiveBytes {
		return "", errors.New("source cache archive has invalid type or size")
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxArchiveBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeCacheMetadata(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".metadata-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0444); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func validateRepository(r Repository) error {
	if _, err := r.ownerRepo(); err != nil {
		return err
	}
	kind, ref := r.reference()
	if !validRef(ref) {
		return fmt.Errorf("invalid GitHub %s reference %q", kind, ref)
	}
	for _, addr := range []string{r.APIURL, r.ArchiveURL} {
		if addr == "" {
			continue
		}
		u, err := url.Parse(addr)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid GitHub source URL override")
		}
		loopback := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
		if u.Scheme != "https" && !(loopback && u.Scheme == "http") {
			return errors.New("source URL overrides must use HTTPS")
		}
	}
	return nil
}

func (s *Source) extract(archive, destination, commit string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	if len(z.File) == 0 {
		z.Close()
		return errors.New("empty source archive")
	}
	root := strings.SplitN(z.File[0].Name, "/", 2)[0]
	z.Close()
	ownerRepo, err := s.Repository.ownerRepo()
	if err != nil {
		return err
	}
	expected := path.Base(ownerRepo) + "-" + commit
	if root != expected {
		return fmt.Errorf("archive root does not match resolved commit %s", commit)
	}
	return ExtractZIP(archive, destination)
}
