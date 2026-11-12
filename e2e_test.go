package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// harness builds the shim once and provides a fake "real op" whose invocations
// are counted, so tests can assert that a cache hit never shells out.
type harness struct {
	t        *testing.T
	bin      string // the op-cache binary under test
	fakeOp   string // stand-in for the real 1Password CLI
	cacheDir string
	callLog  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()

	bin := filepath.Join(dir, "op")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building shim: %v\n%s", err, out)
	}

	h := &harness{
		t:        t,
		bin:      bin,
		fakeOp:   filepath.Join(dir, "fake-op"),
		cacheDir: filepath.Join(dir, "cache"),
		callLog:  filepath.Join(dir, "calls.log"),
	}

	// The fake op knows two secrets and fails on anything else, mimicking the
	// real CLI's error format and exit code.
	script := `#!/bin/sh
echo "$@" >> "` + h.callLog + `"
if [ -n "$FAKE_OP_DOWN" ]; then
  echo "[ERROR] 2026/01/01 00:00:00 connecting to desktop app: connection refused" >&2
  exit 1
fi
# Find the reference: the last argument.
for arg in "$@"; do ref="$arg"; done
case "$ref" in
  op://vault/item/password) printf 'super-secret-value' ;;
  op://vault/item/multiline) printf 'line1\nline2' ;;
  *)
    echo "[ERROR] 2026/01/01 00:00:00 could not read secret '$ref': not found" >&2
    exit 1 ;;
esac
`
	if err := os.WriteFile(h.fakeOp, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

// run invokes the shim with the fake op installed as the real CLI.
func (h *harness) run(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = append(os.Environ(),
		"OP_CACHE_REAL_OP="+h.fakeOp,
		"OP_CACHE_DIR="+h.cacheDir,
		"OP_ACCOUNT=",
		"OP_CACHE_DISABLE=",
		"OP_CACHE_DEPTH=",
	)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.Stdin = strings.NewReader("")
	err := cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		h.t.Fatalf("running shim: %v", err)
	}
	return out.String(), errb.String(), code
}

// realOpCalls counts how many times the fake real op was executed.
func (h *harness) realOpCalls() int {
	data, err := os.ReadFile(h.callLog)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"))
}

func (h *harness) resetCallLog() { os.Remove(h.callLog) }

func TestReadCachesAndAvoidsRealOp(t *testing.T) {
	h := newHarness(t)

	out, _, code := h.run("read", "op://vault/item/password")
	if code != 0 {
		t.Fatalf("first read exited %d", code)
	}
	if out != "super-secret-value\n" {
		t.Errorf("first read stdout = %q, want %q", out, "super-secret-value\n")
	}
	if n := h.realOpCalls(); n != 1 {
		t.Fatalf("first read called real op %d times, want 1", n)
	}

	// Second read must be served entirely from cache.
	h.resetCallLog()
	out, _, code = h.run("read", "op://vault/item/password")
	if code != 0 {
		t.Fatalf("cached read exited %d", code)
	}
	if out != "super-secret-value\n" {
		t.Errorf("cached read stdout = %q, want identical output", out)
	}
	if n := h.realOpCalls(); n != 0 {
		t.Errorf("cached read called real op %d times, want 0", n)
	}
}

// The whole point of the project: once warm, a broken 1Password still works.
func TestCachedReadWorksWhenRealOpIsDown(t *testing.T) {
	h := newHarness(t)
	if _, _, code := h.run("read", "op://vault/item/password"); code != 0 {
		t.Fatalf("warming read exited %d", code)
	}

	cmd := exec.Command(h.bin, "read", "op://vault/item/password")
	cmd.Env = append(os.Environ(),
		"OP_CACHE_REAL_OP="+h.fakeOp,
		"OP_CACHE_DIR="+h.cacheDir,
		"OP_ACCOUNT=",
		"OP_CACHE_DISABLE=",
		"FAKE_OP_DOWN=1",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read failed while op was down: %v", err)
	}
	if string(out) != "super-secret-value\n" {
		t.Errorf("stdout = %q, want the cached value", out)
	}
}

func TestReadNoNewline(t *testing.T) {
	h := newHarness(t)
	for _, pass := range []string{"cold", "warm"} {
		out, _, code := h.run("read", "-n", "op://vault/item/password")
		if code != 0 {
			t.Fatalf("%s: exit %d", pass, code)
		}
		if out != "super-secret-value" {
			t.Errorf("%s: stdout = %q, want no trailing newline", pass, out)
		}
	}
}

func TestReadMultilineSecretRoundTrips(t *testing.T) {
	h := newHarness(t)
	want := "line1\nline2"
	for _, pass := range []string{"cold", "warm"} {
		out, _, code := h.run("read", "-n", "op://vault/item/multiline")
		if code != 0 {
			t.Fatalf("%s: exit %d", pass, code)
		}
		if out != want {
			t.Errorf("%s: stdout = %q, want %q", pass, out, want)
		}
	}
}

func TestReadOutFile(t *testing.T) {
	h := newHarness(t)
	target := filepath.Join(t.TempDir(), "key.pem")

	if _, errOut, code := h.run("read", "-o", target, "op://vault/item/password"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "super-secret-value" {
		t.Errorf("file contents = %q", data)
	}
	info, _ := os.Stat(target)
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 600", mode)
	}

	// Without --force, an existing file must not be clobbered.
	if _, _, code := h.run("read", "-o", target, "op://vault/item/password"); code == 0 {
		t.Error("overwriting an existing file without --force succeeded, want failure")
	}
	// With --force, it should.
	if _, errOut, code := h.run("read", "-f", "-o", target, "op://vault/item/password"); code != 0 {
		t.Errorf("--force overwrite exit %d: %s", code, errOut)
	}
}

func TestReadErrorPassesThrough(t *testing.T) {
	h := newHarness(t)
	_, errOut, code := h.run("read", "op://vault/item/nonexistent")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "could not read secret") {
		t.Errorf("stderr = %q, want the real op's message", errOut)
	}
	// A failed read must not poison the cache.
	if out, _, _ := h.run("cache", "list"); strings.Contains(out, "nonexistent") {
		t.Error("a failed read was cached")
	}
}

func TestOtpIsNeverCached(t *testing.T) {
	h := newHarness(t)
	ref := "op://vault/item/one-time password?attribute=otp"
	h.run("read", ref)
	h.resetCallLog()
	h.run("read", ref)
	if n := h.realOpCalls(); n == 0 {
		t.Error("a one-time password was served from cache; it must always hit the real op")
	}
}

func TestNonReadCommandsPassThrough(t *testing.T) {
	h := newHarness(t)
	h.run("item", "list", "--format", "json")
	if n := h.realOpCalls(); n != 1 {
		t.Errorf("real op called %d times for 'item list', want 1", n)
	}
}

func TestCacheDisableBypassesCache(t *testing.T) {
	h := newHarness(t)
	h.run("read", "op://vault/item/password") // warm
	h.resetCallLog()

	cmd := exec.Command(h.bin, "read", "op://vault/item/password")
	cmd.Env = append(os.Environ(),
		"OP_CACHE_REAL_OP="+h.fakeOp,
		"OP_CACHE_DIR="+h.cacheDir,
		"OP_CACHE_DISABLE=1",
		"OP_ACCOUNT=",
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("disabled read failed: %v", err)
	}
	if n := h.realOpCalls(); n != 1 {
		t.Errorf("real op called %d times with OP_CACHE_DISABLE=1, want 1", n)
	}
}

func TestCacheSubcommands(t *testing.T) {
	h := newHarness(t)
	ref := "op://vault/item/password"
	h.run("read", ref)

	// list hides values by default, reveals on request
	out, _, _ := h.run("cache", "list")
	if !strings.Contains(out, ref) {
		t.Errorf("list output missing reference: %q", out)
	}
	if strings.Contains(out, "super-secret-value") {
		t.Error("list leaked the secret without --reveal")
	}
	out, _, _ = h.run("cache", "list", "--reveal")
	if !strings.Contains(out, "super-secret-value") {
		t.Errorf("list --reveal did not show the value: %q", out)
	}

	// get
	out, _, code := h.run("cache", "get", ref)
	if code != 0 || strings.TrimSpace(out) != "super-secret-value" {
		t.Errorf("cache get = %q (exit %d)", out, code)
	}

	// set a manual value, which read must then serve
	manual := "op://vault/item/manual"
	if _, errOut, code := h.run("cache", "set", manual, "typed-by-hand"); code != 0 {
		t.Fatalf("cache set exit %d: %s", code, errOut)
	}
	h.resetCallLog()
	out, _, code = h.run("read", manual)
	if code != 0 || out != "typed-by-hand\n" {
		t.Errorf("read of manual entry = %q (exit %d)", out, code)
	}
	if n := h.realOpCalls(); n != 0 {
		t.Errorf("read of manual entry called real op %d times", n)
	}

	// rm
	if _, _, code := h.run("cache", "rm", manual); code != 0 {
		t.Errorf("cache rm exit %d", code)
	}
	if out, _, _ := h.run("cache", "list"); strings.Contains(out, manual) {
		t.Error("entry still listed after rm")
	}

	// clear --force
	if _, _, code := h.run("cache", "clear", "--force"); code != 0 {
		t.Errorf("cache clear exit %d", code)
	}
	if out, _, _ := h.run("cache", "list"); !strings.Contains(out, "empty") {
		t.Errorf("cache not empty after clear: %q", out)
	}

	// status and path
	if out, _, code := h.run("cache", "status"); code != 0 || !strings.Contains(out, h.cacheDir) {
		t.Errorf("cache status = %q (exit %d)", out, code)
	}
	if out, _, code := h.run("cache", "path"); code != 0 || strings.TrimSpace(out) != h.cacheDir {
		t.Errorf("cache path = %q (exit %d)", out, code)
	}
}

func TestCacheWarm(t *testing.T) {
	h := newHarness(t)
	_, errOut, code := h.run("cache", "warm", "op://vault/item/password", "op://vault/item/multiline")
	if code != 0 {
		t.Fatalf("warm exit %d: %s", code, errOut)
	}
	h.resetCallLog()
	if out, _, _ := h.run("read", "op://vault/item/password"); out != "super-secret-value\n" {
		t.Errorf("warmed read = %q", out)
	}
	if n := h.realOpCalls(); n != 0 {
		t.Errorf("warmed read called real op %d times, want 0", n)
	}
}

func TestAccountScopedReads(t *testing.T) {
	h := newHarness(t)
	ref := "op://vault/item/password"

	if _, _, code := h.run("--account", "work", "read", ref); code != 0 {
		t.Fatal("account read failed")
	}
	h.resetCallLog()

	// Same account: cached.
	h.run("--account", "work", "read", ref)
	if n := h.realOpCalls(); n != 0 {
		t.Errorf("same-account read called real op %d times, want 0", n)
	}
	// Different account: must not reuse the other account's entry.
	h.resetCallLog()
	h.run("--account", "personal", "read", ref)
	if n := h.realOpCalls(); n != 1 {
		t.Errorf("different-account read called real op %d times, want 1", n)
	}
}

func TestRecursionGuard(t *testing.T) {
	h := newHarness(t)
	cmd := exec.Command(h.bin, "item", "list")
	cmd.Env = append(os.Environ(),
		"OP_CACHE_REAL_OP="+h.fakeOp,
		"OP_CACHE_DIR="+h.cacheDir,
		"OP_CACHE_DEPTH="+strconv.Itoa(maxDepth+1),
	)
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "recursion") {
		t.Errorf("deep invocation did not trip the guard: %q", out)
	}
}

func TestShimSkipsItselfOnPath(t *testing.T) {
	// With no OP_CACHE_REAL_OP, the shim must walk PATH past its own directory
	// and find the fake op, rather than re-executing itself.
	h := newHarness(t)
	realDir := t.TempDir()
	realOp := filepath.Join(realDir, "op")
	data, _ := os.ReadFile(h.fakeOp)
	if err := os.WriteFile(realOp, data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(h.bin, "read", "op://vault/item/password")
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Dir(h.bin)+string(os.PathListSeparator)+realDir,
		"OP_CACHE_DIR="+filepath.Join(t.TempDir(), "c"),
		"OP_CACHE_REAL_OP=",
		"OP_ACCOUNT=",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("PATH-based discovery failed: %v", err)
	}
	if string(out) != "super-secret-value\n" {
		t.Errorf("stdout = %q", out)
	}
}
