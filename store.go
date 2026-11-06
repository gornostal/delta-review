package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const viewedTTL = 90 * 24 * time.Hour

// stateDir returns $XDG_STATE_HOME/delta-review (default ~/.local/state/delta-review).
func stateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" || !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "delta-review")
}

// ViewedStore persists hashes of file diffs that were marked as viewed.
type ViewedStore struct {
	path   string
	viewed map[string]int64 // hash -> unix time marked
}

type viewedFile struct {
	Viewed map[string]int64 `json:"viewed"`
}

func OpenViewedStore() *ViewedStore {
	s := &ViewedStore{path: filepath.Join(stateDir(), "viewed.json")}
	s.viewed = s.read()
	return s
}

func (s *ViewedStore) read() map[string]int64 {
	m := map[string]int64{}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return m
	}
	var vf viewedFile
	if json.Unmarshal(data, &vf) == nil && vf.Viewed != nil {
		m = vf.Viewed
	}
	return m
}

func (s *ViewedStore) IsViewed(hash string) bool {
	_, ok := s.viewed[hash]
	return ok
}

// Set marks or unmarks a hash and writes the store. The file is re-read
// first so concurrent sessions don't clobber each other's marks.
func (s *ViewedStore) Set(hash string, viewed bool) error {
	now := time.Now()
	m := s.read()
	if viewed {
		m[hash] = now.Unix()
		s.viewed[hash] = now.Unix()
	} else {
		delete(m, hash)
		delete(s.viewed, hash)
	}
	cutoff := now.Add(-viewedTTL).Unix()
	for h, t := range m {
		if t < cutoff {
			delete(m, h)
		}
	}
	data, err := json.MarshalIndent(viewedFile{Viewed: m}, "", " ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
