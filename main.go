package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const version = "0.1.0"

// maxDepth bounds how many nested op-cache processes we tolerate before
// concluding the shim is invoking itself.
const maxDepth = 8

// cacheDisabled reports whether OP_CACHE_DISABLE turns the shim into a
// straight passthrough. Any value except empty, "0", "false", or "no" enables it.
func cacheDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OP_CACHE_DISABLE"))) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// globalFlagsWithValue are op's global flags that consume a following argument.
var globalFlagsWithValue = map[string]bool{
	"--account":  true,
	"--config":   true,
	"--encoding": true,
	"--format":   true,
	"--session":  true,
}

// globalFlags holds the flags that appear before an op subcommand. We keep them
// verbatim so anything forwarded to the real op is byte-identical to what the
// user typed, and separately note the account, which is part of the cache key.
type globalFlags struct {
	raw     []string
	account string
}

// rebuild reassembles a full argv for the real op: globals, subcommand, rest.
func (g globalFlags) rebuild(sub string, rest []string) []string {
	out := make([]string, 0, len(g.raw)+1+len(rest))
	out = append(out, g.raw...)
	out = append(out, sub)
	return append(out, rest...)
}

// splitGlobals peels leading global flags off argv, returning them plus the
// remaining arguments starting at the subcommand.
func splitGlobals(args []string) (globalFlags, []string) {
	var g globalFlags
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		if a == "--" {
			break
		}
		name, inlineVal, hasInline := strings.Cut(a, "=")
		if name == "--account" {
			if hasInline {
				g.account = inlineVal
			} else if i+1 < len(args) {
				g.account = args[i+1]
			}
		}
		g.raw = append(g.raw, a)
		i++
		if globalFlagsWithValue[name] && !hasInline && i < len(args) {
			g.raw = append(g.raw, args[i])
			i++
		}
	}
	if g.account == "" {
		g.account = os.Getenv("OP_ACCOUNT")
	}
	return g, args[i:]
}

func main() {
	args := os.Args[1:]

	// Guard against a shim that resolves back to itself. Legitimate nesting
	// (op run -> a tool -> op read) is fine; runaway recursion is not.
	if d, err := strconv.Atoi(os.Getenv("OP_CACHE_DEPTH")); err == nil && d >= maxDepth {
		fatalf("op-cache recursion detected (depth %d): the shim keeps resolving to itself. Set OP_CACHE_REAL_OP to the real 1Password CLI.", d)
	}

	g, rest := splitGlobals(args)

	if len(rest) == 0 {
		passthrough(args)
	}

	// `op cache` is ours in every mode, including when caching is disabled:
	// it is how you inspect and fix the cache.
	if rest[0] == "cache" {
		cmdCache(g, rest[1:])
	}

	if cacheDisabled() {
		debugf("cache disabled via OP_CACHE_DISABLE")
		passthrough(args)
	}

	switch rest[0] {
	case "read":
		cmdRead(g, rest[1:])
	default:
		// Everything else is 1Password's business, not ours.
		passthrough(args)
	}
}

// --- output helpers -------------------------------------------------------

// opTimestamp matches the real CLI's log prefix format.
func opTimestamp() string { return time.Now().Format("2006/01/02 15:04:05") }

// fatalf prints an error in op's own format and exits 1.
func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[ERROR] %s %s\n", opTimestamp(), fmt.Sprintf(format, a...))
	os.Exit(1)
}

// warnf reports a cache problem that did not stop the command from working.
func warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[op-cache] warning: %s\n", fmt.Sprintf(format, a...))
}

// debugf traces cache decisions when OP_CACHE_DEBUG is set.
func debugf(format string, a ...any) {
	if os.Getenv("OP_CACHE_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[op-cache] %s\n", fmt.Sprintf(format, a...))
}
