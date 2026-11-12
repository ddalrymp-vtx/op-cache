package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Entry is one cached secret, stored as a single JSON file in the cache dir.
//
// Values are stored in the clear. This cache is explicitly not a security
// boundary: it exists so local work keeps moving when the 1Password desktop
// integration refuses to connect.
type Entry struct {
	Reference string    `json:"reference"`
	Account   string    `json:"account,omitempty"`
	Value     string    `json:"value"`
	Encoding  string    `json:"encoding"` // "plain" or "base64"
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Source    string    `json:"source"` // "op" (fetched) or "manual" (op cache set)
}

// Secret returns the decoded secret value.
func (e *Entry) Secret() (string, error) {
	switch e.Encoding {
	case "", "plain":
		return e.Value, nil
	case "base64":
		raw, err := base64.StdEncoding.DecodeString(e.Value)
		if err != nil {
			return "", fmt.Errorf("corrupt base64 value: %w", err)
		}
		return string(raw), nil
	default:
		return "", fmt.Errorf("unknown value encoding %q", e.Encoding)
	}
}

func newEntry(account, ref, value, source string) *Entry {
	now := time.Now()
	e := &Entry{
		Reference: ref,
		Account:   account,
		CreatedAt: now,
		UpdatedAt: now,
		Source:    source,
	}
	// Keep the file readable by humans when the secret is ordinary text.
	// Fall back to base64 only for binary that would not survive JSON.
	if utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') {
		e.Value, e.Encoding = value, "plain"
	} else {
		e.Value, e.Encoding = base64.StdEncoding.EncodeToString([]byte(value)), "base64"
	}
	return e
}

// Store is a directory of cache entries.
type Store struct {
	dir string
}

// DefaultCacheDir resolves the cache location: $OP_CACHE_DIR, else ~/.op-cache.
func DefaultCacheDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("OP_CACHE_DIR")); d != "" {
		return expandHome(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".op-cache"), nil
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/")), nil
	}
	return filepath.Abs(p)
}

func OpenStore() (*Store, error) {
	dir, err := DefaultCacheDir()
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Dir() string        { return s.dir }
func (s *Store) entriesDir() string { return filepath.Join(s.dir, "entries") }

func (s *Store) ensureDir() error {
	if err := os.MkdirAll(s.entriesDir(), 0o700); err != nil {
		return fmt.Errorf("cannot create cache dir %s: %w", s.entriesDir(), err)
	}
	return nil
}

// key derives the on-disk name for an (account, reference) pair. The account is
// part of the key because the same reference can resolve differently per account.
func key(account, ref string) string {
	sum := sha256.Sum256([]byte(account + "\x00" + ref))
	return hex.EncodeToString(sum[:])
}

func (s *Store) pathFor(account, ref string) string {
	return filepath.Join(s.entriesDir(), key(account, ref)+".json")
}

// Get returns the cached entry, or nil if there is no usable entry.
func (s *Store) Get(account, ref string) (*Entry, error) {
	data, err := os.ReadFile(s.pathFor(account, ref))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		// A corrupt entry is a miss, not a hard failure: fall through to real op.
		return nil, nil
	}
	return &e, nil
}

// Put writes an entry atomically, preserving CreatedAt when replacing one.
func (s *Store) Put(e *Entry) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	path := s.pathFor(e.Account, e.Reference)
	if old, err := s.Get(e.Account, e.Reference); err == nil && old != nil {
		e.CreatedAt = old.CreatedAt
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(s.entriesDir(), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Delete removes one entry. Reports whether an entry was present.
func (s *Store) Delete(account, ref string) (bool, error) {
	err := os.Remove(s.pathFor(account, ref))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// List returns every entry, sorted by reference then account.
func (s *Store) List() ([]*Entry, error) {
	files, err := os.ReadDir(s.entriesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Entry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.entriesDir(), f.Name()))
		if err != nil {
			continue
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		out = append(out, &e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Reference != out[j].Reference {
			return out[i].Reference < out[j].Reference
		}
		return out[i].Account < out[j].Account
	})
	return out, nil
}

// Clear removes every entry. Returns how many were removed.
func (s *Store) Clear() (int, error) {
	files, err := os.ReadDir(s.entriesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		if err := os.Remove(filepath.Join(s.entriesDir(), f.Name())); err == nil {
			n++
		}
	}
	return n, nil
}
