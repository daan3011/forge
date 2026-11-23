package slashcmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// SkillsRel is where skills live under an --add-dir root.
const SkillsRel = ".claude/skills"

// ManifestFile records, under the root, the files forge wrote.
const ManifestFile = ".forge-skills.json"

const manifestVersion = 1

type manifest struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"` // slash path under root → sha256
}

// Result is what a sync did; paths are relative to the root.
type Result struct {
	Written []string
	Removed []string
	// Kept are files forge wrote once that were changed since; they are
	// left alone (and stay in the manifest).
	Kept []string
}

// Install writes cmds, rendered with vals, as <root>/.claude/skills/<name>/
// SKILL.md. Unchanged files aren't rewritten. Files forge wrote before that
// are no longer wanted are removed, with their folders when empty. Nothing
// that isn't in forge's manifest is ever changed or removed.
func Install(root string, cmds []Command, vals map[string]string) (Result, error) {
	var res Result
	old, err := readManifest(root)
	if err != nil {
		return res, err
	}
	next := manifest{Version: manifestVersion, Files: map[string]string{}}
	want := map[string]bool{}
	var errs []error
	for _, c := range cmds {
		if err := ValidName(c.Name); err != nil {
			errs = append(errs, err)
			continue
		}
		rel := path.Join(SkillsRel, c.Name, "SKILL.md")
		want[rel] = true
		data := Render(c, vals)
		abs := filepath.Join(root, filepath.FromSlash(rel))
		cur, readErr := os.ReadFile(abs)
		switch {
		case readErr == nil && bytes.Equal(cur, data):
			next.Files[rel] = sum(data)
			continue
		case readErr == nil:
			if h, ours := old.Files[rel]; !ours || h != sum(cur) {
				// Not forge's file, or changed since forge wrote it.
				res.Kept = append(res.Kept, rel)
				if ours {
					next.Files[rel] = h
				}
				continue
			}
		case !errors.Is(readErr, os.ErrNotExist):
			errs = append(errs, readErr)
			continue
		}
		if err := writeAtomic(abs, data); err != nil {
			errs = append(errs, err)
			continue
		}
		next.Files[rel] = sum(data)
		res.Written = append(res.Written, rel)
	}
	for rel, h := range old.Files {
		if want[rel] {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		cur, err := os.ReadFile(abs)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			errs = append(errs, err)
			next.Files[rel] = h
			continue
		case sum(cur) != h:
			res.Kept = append(res.Kept, rel)
			next.Files[rel] = h
			continue
		}
		if err := os.Remove(abs); err != nil {
			errs = append(errs, err)
			next.Files[rel] = h
			continue
		}
		res.Removed = append(res.Removed, rel)
		pruneEmpty(filepath.Dir(abs), filepath.Join(root, filepath.FromSlash(SkillsRel)))
	}
	if err := writeManifest(root, next); err != nil {
		errs = append(errs, err)
	}
	sort.Strings(res.Written)
	sort.Strings(res.Removed)
	sort.Strings(res.Kept)
	return res, errors.Join(errs...)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// pruneEmpty removes dir and its parents up to (not including) stop while
// they are empty.
func pruneEmpty(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop+string(filepath.Separator)) {
		if os.Remove(dir) != nil { // not empty, or gone
			return
		}
		dir = filepath.Dir(dir)
	}
}

func readManifest(root string) (manifest, error) {
	m := manifest{Files: map[string]string{}}
	data, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("slashcmd: %s: %w", ManifestFile, err)
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	return m, nil
}

func writeManifest(root string, m manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(root, ManifestFile), append(data, '\n'))
}

// writeAtomic writes data to path through a temp file and a rename.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
