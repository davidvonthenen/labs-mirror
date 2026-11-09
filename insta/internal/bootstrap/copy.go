package bootstrap

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CopyLab publishes one writable lab copy after prepare has added the tested
// overlay and initial state. Existing destinations are never merged or replaced.
// Source symlinks must remain within this lab; undeclared shared paths fail.
func CopyLab(snapshot *Snapshot, lab Lab, destination string, prepare func(stage string) error) error {
	if snapshot == nil || !safeRelative(lab.SourcePath) {
		return errors.New("invalid lab snapshot")
	}
	source := filepath.Join(snapshot.Root, filepath.FromSlash(lab.SourcePath))
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("source lab directory %q is missing", lab.SourcePath)
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("destination %s already exists; choose a new project directory", destination)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".insta-new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(source, stage); err != nil {
		return err
	}
	if prepare != nil {
		if err := prepare(stage); err != nil {
			return err
		}
	}
	// Both supported platforms expose an atomic no-replace rename. An existing
	// directory, including an empty one, is never replaced even under a race.
	if err := publishDirectory(stage, destination); err != nil {
		return fmt.Errorf("publish project: %w", err)
	}
	return os.Chmod(destination, 0755)
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if excluded(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		out := filepath.Join(destination, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode.IsDir() {
			return os.MkdirAll(out, 0755)
		}
		if mode&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			if !safeLink(filepath.ToSlash(rel), filepath.ToSlash(target)) || !within(source, resolved) {
				return fmt.Errorf("lab link %q requires a path outside the selected lab", rel)
			}
			return os.Symlink(target, out)
		}
		if !mode.IsRegular() {
			return fmt.Errorf("unsupported lab file %q", rel)
		}
		r, err := os.Open(p)
		if err != nil {
			return err
		}
		defer r.Close()
		perm := os.FileMode(0644)
		if mode.Perm()&0111 != 0 {
			perm = 0755
		}
		w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, r)
		closeErr := w.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
