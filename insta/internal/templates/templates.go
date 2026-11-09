// Package templates contains additive lab integration files.
package templates

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// all: preserves the original hidden instruction and Skill files.
//
//go:embed all:overlays insta.schema.json upstream-hashes.json
var files embed.FS

type change struct {
	path string
	data []byte
}

// Apply adds the integration to a freshly staged lab. Files that changed on
// the selected branch/tag are preserved; only known upstream revisions are
// replaced with bundled versions. Existing manifests are validated by the caller.
func Apply(root, labID string) error {
	if labID != "rag-retrieval" && labID != "agentic-rag" {
		return fmt.Errorf("no bundled integration for lab %q", labID)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("template destination must be a real directory")
	}
	var expected map[string]string
	b, err := files.ReadFile("upstream-hashes.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &expected); err != nil {
		return err
	}
	prefix := "overlays/" + labID
	var changes []change
	err = fs.WalkDir(files, prefix, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(name, prefix+"/")
		data, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		changes = append(changes, change{path: rel, data: data})
		return nil
	})
	if err != nil {
		return err
	}
	schema, err := files.ReadFile("insta.schema.json")
	if err != nil {
		return err
	}
	changes = append(changes, change{path: "insta.schema.json", data: schema})
	sort.Slice(changes, func(i, j int) bool { return changes[i].path < changes[j].path })
	var install []change
	for _, item := range changes {
		if err := checkParents(root, item.path); err != nil {
			return err
		}
		destination := filepath.Join(root, filepath.FromSlash(item.path))
		info, err := os.Lstat(destination)
		if errors.Is(err, os.ErrNotExist) {
			install = append(install, item)
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("template destination is not a regular file: %s", item.path)
		}
		data, err := os.ReadFile(destination)
		if err != nil {
			return err
		}
		if bytes.Equal(data, item.data) {
			continue
		}
		h := sha256.Sum256(data)
		if expected[labID+"/"+item.path] == hex.EncodeToString(h[:]) {
			install = append(install, item)
		}
		// Unknown upstream content belongs to the selected branch/tag.
	}
	for _, item := range install {
		destination := filepath.Join(root, filepath.FromSlash(item.path))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		if err := writeFile(destination, item.data); err != nil {
			return err
		}
	}
	return nil
}

func checkParents(root, relative string) error {
	for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(parent)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("template path has an unsafe parent: %s", relative)
		}
	}
	return nil
}

func writeFile(destination string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(destination), ".insta-overlay-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(f.Name(), destination)
}

func Manifest(labID string) ([]byte, error) {
	if labID != "rag-retrieval" && labID != "agentic-rag" {
		return nil, fmt.Errorf("unknown lab %q", labID)
	}
	return files.ReadFile("overlays/" + labID + "/insta.yaml")
}

func Schema() ([]byte, error) { return files.ReadFile("insta.schema.json") }

// Digest identifies the complete bundled overlay, including its schema. Record
// it alongside source provenance so additive document changes are attributable.
func Digest(labID string) (string, error) {
	if labID != "rag-retrieval" && labID != "agentic-rag" {
		return "", fmt.Errorf("unknown lab %q", labID)
	}
	h := sha256.New()
	prefix := "overlays/" + labID
	err := fs.WalkDir(files, prefix, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", strings.TrimPrefix(name, prefix+"/"), len(data))
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	schema, err := Schema()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(h, "insta.schema.json\x00%d\x00", len(schema))
	h.Write(schema)
	return hex.EncodeToString(h.Sum(nil)), nil
}
