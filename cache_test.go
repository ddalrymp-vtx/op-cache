package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return &Store{dir: t.TempDir()}
}

func TestStoreRoundTrip(t *testing.T) {
	s := testStore(t)

	if e, err := s.Get("", "op://v/i/f"); err != nil || e != nil {
		t.Fatalf("empty store: got %v, %v; want nil, nil", e, err)
	}

	if err := s.Put(newEntry("", "op://v/i/f", "hunter2", "op")); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get("", "op://v/i/f")
	if err != nil || e == nil {
		t.Fatalf("after Put: got %v, %v", e, err)
	}
	if v, _ := e.Secret(); v != "hunter2" {
		t.Errorf("value = %q, want %q", v, "hunter2")
	}
}

func TestStoreAccountScoping(t *testing.T) {
	s := testStore(t)
	mustPut(t, s, newEntry("work", "op://v/i/f", "work-secret", "op"))
	mustPut(t, s, newEntry("personal", "op://v/i/f", "personal-secret", "op"))

	for _, c := range []struct{ account, want string }{
		{"work", "work-secret"},
		{"personal", "personal-secret"},
	} {
		e, err := s.Get(c.account, "op://v/i/f")
		if err != nil || e == nil {
			t.Fatalf("account %q: %v, %v", c.account, e, err)
		}
		if v, _ := e.Secret(); v != c.want {
			t.Errorf("account %q: value = %q, want %q", c.account, v, c.want)
		}
	}
	// A different account must not pick up either entry.
	if e, _ := s.Get("other", "op://v/i/f"); e != nil {
		t.Errorf("unrelated account got an entry: %+v", e)
	}
}

func TestStoreValuePreservation(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"trailing spaces": "secret   ",
		"multiline":       "-----BEGIN KEY-----\nabc\ndef\n-----END KEY-----\n",
		"unicode":         "pässwörd-日本語-🔑",
		"binary":          "\x00\x01\x02\xff\xfe",
		"json-ish":        `{"a":"b","c":["d"]}`,
		"quotes":          `he said "hi" and 'bye' and ` + "`tick`",
	}
	s := testStore(t)
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			ref := "op://v/" + name + "/f"
			mustPut(t, s, newEntry("", ref, want, "op"))
			e, err := s.Get("", ref)
			if err != nil || e == nil {
				t.Fatalf("get: %v, %v", e, err)
			}
			got, err := e.Secret()
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("value = %q, want %q", got, want)
			}
		})
	}
}

func TestStorePutPreservesCreatedAt(t *testing.T) {
	s := testStore(t)
	first := newEntry("", "op://v/i/f", "one", "op")
	mustPut(t, s, first)

	second := newEntry("", "op://v/i/f", "two", "manual")
	mustPut(t, s, second)

	e, _ := s.Get("", "op://v/i/f")
	if v, _ := e.Secret(); v != "two" {
		t.Errorf("value = %q, want updated value", v)
	}
	if !e.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt = %v, want preserved %v", e.CreatedAt, first.CreatedAt)
	}
	if e.UpdatedAt.Before(e.CreatedAt) {
		t.Errorf("UpdatedAt %v is before CreatedAt %v", e.UpdatedAt, e.CreatedAt)
	}
}

func TestStoreDeleteAndClear(t *testing.T) {
	s := testStore(t)
	mustPut(t, s, newEntry("", "op://v/a/f", "1", "op"))
	mustPut(t, s, newEntry("", "op://v/b/f", "2", "op"))

	removed, err := s.Delete("", "op://v/a/f")
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v; want true, nil", removed, err)
	}
	if removed, _ := s.Delete("", "op://v/a/f"); removed {
		t.Error("second Delete reported a removal")
	}

	entries, _ := s.List()
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}

	n, err := s.Clear()
	if err != nil || n != 1 {
		t.Fatalf("Clear = %v, %v; want 1, nil", n, err)
	}
	if entries, _ := s.List(); len(entries) != 0 {
		t.Errorf("List after Clear returned %d entries", len(entries))
	}
}

func TestStoreCorruptEntryIsMiss(t *testing.T) {
	s := testStore(t)
	mustPut(t, s, newEntry("", "op://v/i/f", "ok", "op"))
	if err := os.WriteFile(s.pathFor("", "op://v/i/f"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get("", "op://v/i/f")
	if err != nil {
		t.Fatalf("corrupt entry returned error %v, want a clean miss", err)
	}
	if e != nil {
		t.Errorf("corrupt entry returned %+v, want nil", e)
	}
}

func TestStoreFilePermissions(t *testing.T) {
	s := testStore(t)
	mustPut(t, s, newEntry("", "op://v/i/f", "secret", "op"))

	info, err := os.Stat(s.pathFor("", "op://v/i/f"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("entry mode = %o, want 600", mode)
	}
	dirInfo, err := os.Stat(s.entriesDir())
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("entries dir mode = %o, want 700", mode)
	}
}

func TestStoreNoTempFilesLeftBehind(t *testing.T) {
	s := testStore(t)
	for i := 0; i < 5; i++ {
		mustPut(t, s, newEntry("", "op://v/i/f", "v", "op"))
	}
	files, _ := os.ReadDir(s.entriesDir())
	for _, f := range files {
		if filepath.Ext(f.Name()) != ".json" {
			t.Errorf("leftover file in cache dir: %s", f.Name())
		}
	}
}

func TestDefaultCacheDirHonoursEnv(t *testing.T) {
	t.Setenv("OP_CACHE_DIR", "/tmp/custom-op-cache")
	dir, err := DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/custom-op-cache" {
		t.Errorf("dir = %q, want /tmp/custom-op-cache", dir)
	}

	t.Setenv("OP_CACHE_DIR", "")
	home, _ := os.UserHomeDir()
	dir, err = DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".op-cache"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}

func TestCacheDisabled(t *testing.T) {
	for val, want := range map[string]bool{
		"": false, "0": false, "false": false, "no": false,
		"1": true, "true": true, "yes": true, "anything": true,
	} {
		t.Setenv("OP_CACHE_DISABLE", val)
		if got := cacheDisabled(); got != want {
			t.Errorf("OP_CACHE_DISABLE=%q: got %v, want %v", val, got, want)
		}
	}
}

func mustPut(t *testing.T, s *Store, e *Entry) {
	t.Helper()
	if err := s.Put(e); err != nil {
		t.Fatal(err)
	}
}
