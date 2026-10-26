// Package envcap captures the environment that shell setup scripts produce,
// so processes can be started with it directly instead of through a shell.
package envcap

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Capture sources the scripts in order in bash on top of the current
// environment and returns the result as KEY=VALUE pairs.
//
// The variables are printed with bash builtins: running /usr/bin/env would
// drop DYLD_* on macOS, because SIP strips them when a system binary starts.
func Capture(scripts ...string) ([]string, error) {
	if len(scripts) == 0 {
		return os.Environ(), nil
	}
	var sb strings.Builder
	sb.WriteString("set -e\n")
	for _, s := range scripts {
		if _, err := os.Stat(s); err != nil {
			return nil, fmt.Errorf("env script: %w", err)
		}
		fmt.Fprintf(&sb, ". %q >&2\n", s)
	}
	sb.WriteString(`for v in $(compgen -e); do printf '%s=%s\0' "$v" "${!v}"; done` + "\n")

	cmd := exec.Command("/bin/bash", "-c", sb.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sourcing %s: %w\n%s", strings.Join(scripts, ", "), err, stderr.String())
	}
	var env []string
	for _, kv := range bytes.Split(out, []byte{0}) {
		if len(kv) > 0 {
			env = append(env, string(kv))
		}
	}
	return env, nil
}

// Lookup returns the value of key in env.
func Lookup(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return env[i][len(prefix):]
		}
	}
	return ""
}

// With returns env with the given KEY=VALUE pairs set, replacing old values.
func With(env []string, kv ...string) []string {
	out := make([]string, 0, len(env)+len(kv))
	drop := map[string]bool{}
	for _, s := range kv {
		drop[s[:strings.IndexByte(s, '=')+1]] = true
	}
	for _, s := range env {
		if i := strings.IndexByte(s, '='); i < 0 || !drop[s[:i+1]] {
			out = append(out, s)
		}
	}
	return append(out, kv...)
}

// LookPath finds a program on the PATH from env, not the launcher's own PATH.
func LookPath(env []string, name string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	for _, dir := range strings.Split(Lookup(env, "PATH"), ":") {
		p := dir + "/" + name
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH", name)
}
