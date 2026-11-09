package bootstrap

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type archiveEntry struct {
	file *zip.File
	rel  string
	link string
}

// ExtractZIP extracts a GitHub archive's single repository root. It validates
// the complete path/type graph before writing, then materializes symlinks last.
// Destination must not exist; untrusted symlink parents can never redirect writes.
func ExtractZIP(archive, destination string) (retErr error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	if len(z.File) == 0 || len(z.File) > MaxFiles {
		return errors.New("archive contains no files or exceeds file-count limit")
	}
	entries := make([]archiveEntry, 0, len(z.File))
	names := map[string]os.FileMode{}
	root := ""
	var total uint64
	for _, f := range z.File {
		n := strings.TrimSuffix(f.Name, "/")
		if !safeRelative(n) {
			return fmt.Errorf("unsafe archive path %q", f.Name)
		}
		parts := strings.SplitN(n, "/", 2)
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] {
			return errors.New("archive must have one repository root")
		}
		if len(parts) == 1 {
			if !f.Mode().IsDir() {
				return errors.New("archive root must be a directory")
			}
			continue
		}
		rel := parts[1]
		mode := f.Mode()
		if !mode.IsDir() && !mode.IsRegular() && mode&os.ModeSymlink == 0 {
			return fmt.Errorf("unsupported archive entry type for %q", rel)
		}
		if _, ok := names[rel]; ok {
			return fmt.Errorf("duplicate archive path %q", rel)
		}
		names[rel] = mode
		if f.UncompressedSize64 > MaxFileBytes {
			return fmt.Errorf("archive file %q exceeds size limit", rel)
		}
		total += f.UncompressedSize64
		if total > MaxExpandedBytes {
			return errors.New("archive exceeds expanded-size limit")
		}
		entry := archiveEntry{file: f, rel: rel}
		if mode&os.ModeSymlink != 0 {
			if f.UncompressedSize64 > 4096 {
				return errors.New("archive symlink target exceeds size limit")
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			b, err := io.ReadAll(io.LimitReader(r, 4097))
			r.Close()
			if err != nil {
				return err
			}
			entry.link = string(b)
			if !safeLink(rel, entry.link) {
				return fmt.Errorf("archive symlink %q escapes the repository", rel)
			}
		}
		entries = append(entries, entry)
	}
	for _, entry := range entries {
		for parent := path.Dir(entry.rel); parent != "."; parent = path.Dir(parent) {
			if mode, ok := names[parent]; ok && !mode.IsDir() {
				return fmt.Errorf("archive entry %q has a non-directory parent", entry.rel)
			}
		}
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			os.RemoveAll(destination)
		}
	}()
	for _, entry := range entries {
		if excluded(entry.rel) || entry.link != "" {
			continue
		}
		out := filepath.Join(destination, filepath.FromSlash(entry.rel))
		mode := entry.file.Mode()
		if mode.IsDir() {
			if err := os.MkdirAll(out, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
			return err
		}
		if err := extractFile(entry.file, out); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if entry.link == "" || excluded(entry.rel) {
			continue
		}
		out := filepath.Join(destination, filepath.FromSlash(entry.rel))
		if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
			return err
		}
		if err := os.Symlink(entry.link, out); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if entry.link == "" || excluded(entry.rel) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(destination, filepath.FromSlash(entry.rel)))
		if err != nil {
			return fmt.Errorf("archive symlink %q has no safe target: %w", entry.rel, err)
		}
		if !within(destination, resolved) {
			return fmt.Errorf("archive symlink %q escapes the repository", entry.rel)
		}
	}
	return nil
}

func extractFile(f *zip.File, destination string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	mode := os.FileMode(0644)
	if f.Mode().Perm()&0111 != 0 {
		mode = 0755
	}
	w, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(w, io.LimitReader(r, MaxFileBytes+1))
	closeErr := w.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > MaxFileBytes || uint64(n) != f.UncompressedSize64 {
		return fmt.Errorf("archive file %q has invalid expanded size", f.Name)
	}
	return nil
}

func safeRelative(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00:") || strings.HasPrefix(p, "/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func safeLink(rel, target string) bool {
	if target == "" || strings.ContainsAny(target, "\\\x00:") || strings.HasPrefix(target, "/") {
		return false
	}
	resolved := path.Clean(path.Join(path.Dir(rel), target))
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Keep instruction directories, including .agents and .codex, but not their
// authentication material. Templates are retained; live .env files are not.
func excluded(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		switch part {
		case ".git", ".venv", "venv", ".insta", "__pycache__", ".pytest_cache", ".ruff_cache", "node_modules", ".cache", ".aws", ".ssh", "logs", ".DS_Store":
			return true
		}
	}
	base := path.Base(rel)
	if base == ".env" || (strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".example")) {
		return true
	}
	switch base {
	case "auth.json", "credentials.json", "credentials", ".npmrc":
		return true
	}
	for _, suffix := range []string{".pyc", ".pyo", ".log", ".pem", ".key"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}
