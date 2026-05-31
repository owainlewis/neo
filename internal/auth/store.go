package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store persists OAuth credentials to a JSON file (default ~/.neo/auth.json),
// keyed by provider id. The file holds bearer/refresh tokens, so it is written
// with 0600 permissions. Each mutation does a read-modify-write with an atomic
// rename; there is no cross-process lock, which is sufficient for a single
// interactive CLI.
type Store struct {
	path string
}

// NewStore returns a Store backed by path.
func NewStore(path string) *Store { return &Store{path: path} }

// DefaultStore returns the Store at ~/.neo/auth.json.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	return NewStore(filepath.Join(home, ".neo", "auth.json")), nil
}

// Path is the file backing this store.
func (s *Store) Path() string { return s.path }

// Get returns the credentials stored for key, and whether they were present.
func (s *Store) Get(key string) (Credentials, bool, error) {
	all, err := s.load()
	if err != nil {
		return Credentials{}, false, err
	}
	c, ok := all[key]
	return c, ok, nil
}

// Set stores creds under key, replacing any existing entry.
func (s *Store) Set(key string, creds Credentials) error {
	all, err := s.load()
	if err != nil {
		return err
	}
	all[key] = creds
	return s.save(all)
}

// Delete removes the entry for key. Removing a missing key is not an error.
func (s *Store) Delete(key string) error {
	all, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := all[key]; !ok {
		return nil
	}
	delete(all, key)
	return s.save(all)
}

func (s *Store) load() (map[string]Credentials, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Credentials{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if len(b) == 0 {
		return map[string]Credentials{}, nil
	}
	var all map[string]Credentials
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if all == nil {
		all = map[string]Credentials{}
	}
	return all, nil
}

func (s *Store) save(all map[string]Credentials) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(s.path), err)
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".auth-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}
