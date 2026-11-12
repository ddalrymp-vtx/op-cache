package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// readOpts mirrors the flags of `op read` that change its output behaviour.
type readOpts struct {
	ref       string
	outFile   string
	fileMode  string
	noNewline bool
	force     bool
}

// parseReadArgs recognises the subset of `op read` we can serve from cache.
// ok is false for anything unexpected (unknown flags, wrong arity, --help) so
// the caller forwards those to the real op untouched.
func parseReadArgs(args []string) (readOpts, bool) {
	var o readOpts
	o.fileMode = "0600"
	sawRef := false
	endOfFlags := false

	for i := 0; i < len(args); i++ {
		a := args[i]

		// value fetches the argument for a flag given as "--flag value".
		value := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}

		switch {
		case endOfFlags:
			if sawRef {
				return o, false
			}
			o.ref, sawRef = a, true

		case a == "--":
			endOfFlags = true

		case a == "-n" || a == "--no-newline":
			o.noNewline = true
		case a == "-f" || a == "--force":
			o.force = true

		case a == "-o" || a == "--out-file":
			v, ok := value()
			if !ok {
				return o, false
			}
			o.outFile = v
		case strings.HasPrefix(a, "--out-file="):
			o.outFile = strings.TrimPrefix(a, "--out-file=")
		case strings.HasPrefix(a, "-o="):
			o.outFile = strings.TrimPrefix(a, "-o=")

		case a == "--file-mode":
			v, ok := value()
			if !ok {
				return o, false
			}
			o.fileMode = v
		case strings.HasPrefix(a, "--file-mode="):
			o.fileMode = strings.TrimPrefix(a, "--file-mode=")

		case strings.HasPrefix(a, "-"):
			// Unknown flag, --help, or a global flag such as --account that the
			// caller has already stripped. Let the real op decide.
			return o, false

		default:
			if sawRef {
				return o, false // more than one reference: op rejects this
			}
			o.ref, sawRef = a, true
		}
	}

	if !sawRef {
		return o, false
	}
	return o, true
}

// cmdRead implements `op read` with a cache in front of the real CLI.
func cmdRead(g globalFlags, args []string) {
	opts, ok := parseReadArgs(args)
	if !ok {
		passthrough(g.rebuild("read", args))
	}

	ref, ok := ParseRef(opts.ref)
	if !ok || !ref.Cacheable() {
		passthrough(g.rebuild("read", args))
	}

	store, err := OpenStore()
	if err != nil {
		passthrough(g.rebuild("read", args))
	}

	// Cache hit: emit the stored value without ever starting the real op.
	if entry, err := store.Get(g.account, ref.Raw); err == nil && entry != nil {
		if secret, err := entry.Secret(); err == nil {
			debugf("hit %s", ref.Raw)
			emitSecret(secret, opts)
			os.Exit(0)
		}
	}
	debugf("miss %s -> real op", ref.Raw)

	// Cache miss. Ask the real op for the plain value on stdout, regardless of
	// what the caller asked for, then store it and render it ourselves. This
	// keeps --out-file from writing the file before we see the value.
	fetchArgs := g.rebuild("read", []string{"-n", "--", ref.Raw})
	stdout, stderrText, code, err := captureRealOp(fetchArgs)
	if err != nil {
		fatalf("%v", err)
	}
	if code != 0 {
		// Pass the real op's diagnosis through unchanged.
		os.Stderr.WriteString(stderrText)
		os.Exit(code)
	}
	// `op read -n` suppresses the trailing newline, but strip one defensively
	// so a future CLI change cannot corrupt cached values.
	secret := strings.TrimSuffix(stdout, "\n")

	if err := store.Put(newEntry(g.account, ref.Raw, secret, "op")); err != nil {
		warnf("could not write cache entry: %v", err)
	}
	if stderrText != "" {
		os.Stderr.WriteString(stderrText)
	}
	emitSecret(secret, opts)
	os.Exit(0)
}

// emitSecret writes the secret to stdout or --out-file, matching op's rules.
func emitSecret(secret string, opts readOpts) {
	if opts.outFile == "" {
		if opts.noNewline {
			fmt.Print(secret)
		} else {
			fmt.Println(secret)
		}
		return
	}

	mode := os.FileMode(0o600)
	if m, err := strconv.ParseUint(strings.TrimPrefix(opts.fileMode, "0o"), 8, 32); err == nil {
		mode = os.FileMode(m)
	}

	if _, err := os.Stat(opts.outFile); err == nil && !opts.force {
		// The real op prompts here; a shim that blocks on a TTY in a script is
		// worse than one that declines, so decline with a clear reason.
		fatalf("file %q already exists. Use --force to overwrite it.", opts.outFile)
	}
	// op writes the raw secret to the file, with no trailing newline.
	if err := os.WriteFile(opts.outFile, []byte(secret), mode); err != nil {
		fatalf("could not write %q: %v", opts.outFile, err)
	}
	if err := os.Chmod(opts.outFile, mode); err != nil {
		warnf("could not set mode on %q: %v", opts.outFile, err)
	}
}
