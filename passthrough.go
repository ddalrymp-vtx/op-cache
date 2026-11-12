package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// realOpPath finds the genuine 1Password CLI, skipping this shim.
//
// OP_CACHE_REAL_OP wins if set. Otherwise we walk PATH and take the first
// executable named "op" that is not this binary, so dropping the shim into a
// directory earlier on PATH than Homebrew just works.
func realOpPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("OP_CACHE_REAL_OP")); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("OP_CACHE_REAL_OP=%s is not usable: %w", p, err)
		}
		return p, nil
	}

	self, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		cand := filepath.Join(dir, "op")
		info, err := os.Stat(cand)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		resolved := cand
		if r, err := filepath.EvalSymlinks(cand); err == nil {
			resolved = r
		}
		if self != "" && resolved == self {
			continue // this shim
		}
		return cand, nil
	}
	return "", errors.New("could not find the real 1Password CLI on PATH; set OP_CACHE_REAL_OP to its full path")
}

// childEnv is the environment for a process we hand off to, with the recursion
// counter bumped so a shim that resolves to itself is caught rather than
// forking forever.
func childEnv() []string {
	depth := 0
	if d, err := strconv.Atoi(os.Getenv("OP_CACHE_DEPTH")); err == nil && d > 0 {
		depth = d
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "OP_CACHE_DEPTH=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, fmt.Sprintf("OP_CACHE_DEPTH=%d", depth+1))
}

// exec replaces this process with the real op, so signals, exit codes, and all
// three stdio streams behave exactly as if the shim were never there.
func execRealOp(args []string) error {
	path, err := realOpPath()
	if err != nil {
		return err
	}
	argv := append([]string{path}, args...)
	return syscall.Exec(path, argv, childEnv())
}

// passthrough hands control to the real op and never returns.
func passthrough(args []string) {
	if err := execRealOp(args); err != nil {
		fatalf("%v", err)
	}
}

// captureRealOp runs the real op and collects stdout, for the cache-fill path
// where we need the value rather than just forwarding it.
func captureRealOp(args []string) (stdout string, stderr string, exitCode int, err error) {
	path, lookErr := realOpPath()
	if lookErr != nil {
		return "", "", 0, lookErr
	}
	cmd := exec.Command(path, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Stdin = os.Stdin
	cmd.Env = childEnv()

	runErr := cmd.Run()
	stdout, stderr = outBuf.String(), errBuf.String()

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		return stdout, stderr, 0, nil
	case errors.As(runErr, &exitErr):
		return stdout, stderr, exitErr.ExitCode(), nil
	default:
		return stdout, stderr, 0, runErr
	}
}
