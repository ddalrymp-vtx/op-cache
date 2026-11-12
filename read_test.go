package main

import "testing"

func TestParseReadArgs(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		wantOK bool
		want   readOpts
	}{
		{"plain", []string{"op://v/i/f"}, true, readOpts{ref: "op://v/i/f", fileMode: "0600"}},
		{"no newline short", []string{"-n", "op://v/i/f"}, true, readOpts{ref: "op://v/i/f", fileMode: "0600", noNewline: true}},
		{"no newline long", []string{"--no-newline", "op://v/i/f"}, true, readOpts{ref: "op://v/i/f", fileMode: "0600", noNewline: true}},
		{"out file separate", []string{"-o", "k.pem", "op://v/i/f"}, true, readOpts{ref: "op://v/i/f", outFile: "k.pem", fileMode: "0600"}},
		{"out file inline", []string{"--out-file=k.pem", "op://v/i/f"}, true, readOpts{ref: "op://v/i/f", outFile: "k.pem", fileMode: "0600"}},
		{"file mode", []string{"--file-mode", "0640", "-o", "k", "op://v/i/f"}, true, readOpts{ref: "op://v/i/f", outFile: "k", fileMode: "0640"}},
		{"force", []string{"-f", "op://v/i/f"}, true, readOpts{ref: "op://v/i/f", fileMode: "0600", force: true}},
		{"double dash", []string{"--", "op://v/i/f"}, true, readOpts{ref: "op://v/i/f", fileMode: "0600"}},
		{"ref before flag", []string{"op://v/i/f", "-n"}, true, readOpts{ref: "op://v/i/f", fileMode: "0600", noNewline: true}},

		{"no args", []string{}, false, readOpts{}},
		{"two refs", []string{"op://a/b/c", "op://d/e/f"}, false, readOpts{}},
		{"help", []string{"--help"}, false, readOpts{}},
		{"unknown flag", []string{"--totally-new", "op://v/i/f"}, false, readOpts{}},
		{"dangling value flag", []string{"op://v/i/f", "-o"}, false, readOpts{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseReadArgs(c.args)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestSplitGlobals(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantAccount string
		wantRaw     []string
		wantRest    []string
	}{
		{"none", []string{"read", "op://v/i/f"}, "", nil, []string{"read", "op://v/i/f"}},
		{"account separate", []string{"--account", "my.1password.com", "read", "op://v/i/f"}, "my.1password.com",
			[]string{"--account", "my.1password.com"}, []string{"read", "op://v/i/f"}},
		{"account inline", []string{"--account=work", "read", "x"}, "work",
			[]string{"--account=work"}, []string{"read", "x"}},
		{"boolean global", []string{"--no-color", "read", "x"}, "",
			[]string{"--no-color"}, []string{"read", "x"}},
		{"format consumes value", []string{"--format", "json", "item", "list"}, "",
			[]string{"--format", "json"}, []string{"item", "list"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("OP_ACCOUNT", "")
			g, rest := splitGlobals(c.args)
			if g.account != c.wantAccount {
				t.Errorf("account = %q, want %q", g.account, c.wantAccount)
			}
			if !equal(g.raw, c.wantRaw) {
				t.Errorf("raw = %v, want %v", g.raw, c.wantRaw)
			}
			if !equal(rest, c.wantRest) {
				t.Errorf("rest = %v, want %v", rest, c.wantRest)
			}
		})
	}
}

func TestRebuildRoundTrips(t *testing.T) {
	g, rest := splitGlobals([]string{"--account", "work", "--no-color", "read", "-n", "op://v/i/f"})
	got := g.rebuild(rest[0], rest[1:])
	want := []string{"--account", "work", "--no-color", "read", "-n", "op://v/i/f"}
	if !equal(got, want) {
		t.Errorf("rebuild = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
