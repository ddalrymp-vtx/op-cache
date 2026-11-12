package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

const cacheUsage = `Manage the local op-cache.

This is not a 1Password command. It manages the plaintext cache that op-cache
keeps in ~/.op-cache so 'op read' keeps working when the 1Password desktop
integration will not connect.

Usage:  op cache <command> [flags]

Commands:
  list              List cached references (values hidden unless --reveal)
  get <ref>         Print the cached value for a reference
  set <ref> [value] Store a value by hand; reads stdin when value is omitted
  warm <ref>...     Fetch references from 1Password now and cache them
  rm <ref>...       Remove cached references
  clear             Remove every cached entry
  path              Print the cache directory
  status            Show cache location, entry count, and the real op in use

Flags:
      --reveal      Show secret values in 'list' output
      --json        Machine-readable output for 'list' and 'status'
  -f, --force       Do not prompt for confirmation ('clear')

Environment:
  OP_CACHE_DIR      Cache location (default ~/.op-cache)
  OP_CACHE_REAL_OP  Path to the real 1Password CLI
  OP_CACHE_DISABLE  Set to 1 to bypass the cache entirely
  OP_CACHE_DEBUG    Set to 1 to trace hits and misses on stderr
`

func cmdCache(g globalFlags, args []string) {
	if len(args) == 0 {
		fmt.Print(cacheUsage)
		os.Exit(0)
	}

	sub, rest := args[0], args[1:]

	// Pull the cache-local flags out of the remaining arguments.
	var reveal, asJSON, force bool
	var positional []string
	for _, a := range rest {
		switch a {
		case "--reveal":
			reveal = true
		case "--json":
			asJSON = true
		case "-f", "--force":
			force = true
		case "-h", "--help":
			fmt.Print(cacheUsage)
			os.Exit(0)
		default:
			positional = append(positional, a)
		}
	}

	store, err := OpenStore()
	if err != nil {
		fatalf("%v", err)
	}

	switch sub {
	case "list", "ls":
		cacheList(store, reveal, asJSON)
	case "get":
		cacheGet(store, g, positional)
	case "set":
		cacheSet(store, g, positional)
	case "warm", "fetch":
		cacheWarm(store, g, positional)
	case "rm", "remove", "delete", "invalidate":
		cacheRemove(store, g, positional)
	case "clear", "purge":
		cacheClear(store, force)
	case "path", "dir":
		fmt.Println(store.Dir())
	case "status", "info":
		cacheStatus(store, asJSON)
	case "help", "-h", "--help":
		fmt.Print(cacheUsage)
	default:
		fatalf("unknown cache command %q. Run 'op cache help'.", sub)
	}
	os.Exit(0)
}

func cacheList(s *Store, reveal, asJSON bool) {
	entries, err := s.List()
	if err != nil {
		fatalf("%v", err)
	}
	if asJSON {
		type row struct {
			Reference string    `json:"reference"`
			Account   string    `json:"account,omitempty"`
			Value     string    `json:"value,omitempty"`
			Source    string    `json:"source"`
			UpdatedAt time.Time `json:"updated_at"`
		}
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			r := row{Reference: e.Reference, Account: e.Account, Source: e.Source, UpdatedAt: e.UpdatedAt}
			if reveal {
				v, _ := e.Secret()
				r.Value = v
			}
			rows = append(rows, r)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			fatalf("%v", err)
		}
		return
	}

	if len(entries) == 0 {
		fmt.Println("Cache is empty.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "REFERENCE\tACCOUNT\tSOURCE\tUPDATED\tVALUE")
	for _, e := range entries {
		shown := "<hidden>"
		if reveal {
			v, _ := e.Secret()
			shown = strings.ReplaceAll(v, "\n", "\\n")
		}
		account := e.Account
		if account == "" {
			account = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			e.Reference, account, e.Source, humanAge(e.UpdatedAt), shown)
	}
	w.Flush()
}

func cacheGet(s *Store, g globalFlags, args []string) {
	if len(args) != 1 {
		fatalf("usage: op cache get <reference>")
	}
	e, err := s.Get(g.account, args[0])
	if err != nil {
		fatalf("%v", err)
	}
	if e == nil {
		fatalf("%q is not cached", args[0])
	}
	v, err := e.Secret()
	if err != nil {
		fatalf("%v", err)
	}
	fmt.Println(v)
}

func cacheSet(s *Store, g globalFlags, args []string) {
	if len(args) == 0 || len(args) > 2 {
		fatalf("usage: op cache set <reference> [value]   (value is read from stdin when omitted)")
	}
	ref := args[0]
	if _, ok := ParseRef(ref); !ok {
		fatalf("%q is not a valid secret reference (expected op://vault/item/field)", ref)
	}

	var value string
	if len(args) == 2 {
		value = args[1]
	} else {
		data, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			fatalf("could not read value from stdin: %v", err)
		}
		// A trailing newline is almost always the shell's, not the secret's.
		value = strings.TrimSuffix(string(data), "\n")
	}

	if err := s.Put(newEntry(g.account, ref, value, "manual")); err != nil {
		fatalf("%v", err)
	}
	fmt.Fprintf(os.Stderr, "Cached %s\n", ref)
}

func cacheWarm(s *Store, g globalFlags, refs []string) {
	if len(refs) == 0 {
		fatalf("usage: op cache warm <reference>...")
	}
	failed := 0
	for _, ref := range refs {
		parsed, ok := ParseRef(ref)
		if !ok {
			fmt.Fprintf(os.Stderr, "skip  %s (not a valid secret reference)\n", ref)
			failed++
			continue
		}
		if !parsed.Cacheable() {
			fmt.Fprintf(os.Stderr, "skip  %s (one-time passwords are never cached)\n", ref)
			continue
		}
		stdout, stderrText, code, err := captureRealOp(g.rebuild("read", []string{"-n", "--", ref}))
		if err != nil {
			fmt.Fprintf(os.Stderr, "fail  %s (%v)\n", ref, err)
			failed++
			continue
		}
		if code != 0 {
			fmt.Fprintf(os.Stderr, "fail  %s (%s)\n", ref, strings.TrimSpace(firstLine(stderrText)))
			failed++
			continue
		}
		if err := s.Put(newEntry(g.account, ref, strings.TrimSuffix(stdout, "\n"), "op")); err != nil {
			fmt.Fprintf(os.Stderr, "fail  %s (%v)\n", ref, err)
			failed++
			continue
		}
		fmt.Fprintf(os.Stderr, "ok    %s\n", ref)
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func cacheRemove(s *Store, g globalFlags, refs []string) {
	if len(refs) == 0 {
		fatalf("usage: op cache rm <reference>...")
	}
	missing := 0
	for _, ref := range refs {
		removed, err := s.Delete(g.account, ref)
		if err != nil {
			fatalf("%v", err)
		}
		if removed {
			fmt.Fprintf(os.Stderr, "Removed %s\n", ref)
		} else {
			fmt.Fprintf(os.Stderr, "Not cached: %s\n", ref)
			missing++
		}
	}
	if missing == len(refs) {
		os.Exit(1)
	}
}

func cacheClear(s *Store, force bool) {
	entries, err := s.List()
	if err != nil {
		fatalf("%v", err)
	}
	if len(entries) == 0 {
		fmt.Println("Cache is already empty.")
		return
	}
	if !force {
		fmt.Fprintf(os.Stderr, "Remove all %d cached entries from %s? [y/N] ", len(entries), s.Dir())
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			os.Exit(1)
		}
	}
	n, err := s.Clear()
	if err != nil {
		fatalf("%v", err)
	}
	fmt.Fprintf(os.Stderr, "Removed %d cached entries.\n", n)
}

func cacheStatus(s *Store, asJSON bool) {
	entries, _ := s.List()
	realOp, realErr := realOpPath()
	realDesc := realOp
	if realErr != nil {
		realDesc = "not found: " + realErr.Error()
	}
	disabled := cacheDisabled()

	if asJSON {
		out := map[string]any{
			"version":   version,
			"cache_dir": s.Dir(),
			"entries":   len(entries),
			"real_op":   realDesc,
			"disabled":  disabled,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "op-cache version:\t%s\n", version)
	fmt.Fprintf(w, "Cache directory:\t%s\n", s.Dir())
	fmt.Fprintf(w, "Cached entries:\t%d\n", len(entries))
	fmt.Fprintf(w, "Real 1Password CLI:\t%s\n", realDesc)
	if disabled {
		fmt.Fprintf(w, "Status:\tDISABLED (OP_CACHE_DISABLE is set)\n")
	} else {
		fmt.Fprintf(w, "Status:\tactive\n")
	}
	w.Flush()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func humanAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
