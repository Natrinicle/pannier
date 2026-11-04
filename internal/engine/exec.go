package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
)

// ExecRunner runs argv[0] as a subprocess. Passwords never go to the log:
// -p / --password and --password= are redacted. Stderr is redacted the
// same way before it is wrapped into an error.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, stdin io.Reader, env []string, argv ...string) ([]byte, error) {
	return runExec(ctx, stdin, env, nil, argv...)
}

type extraFiler interface {
	RunWithExtraFiles(ctx context.Context, stdin io.Reader, env []string, extra []*os.File, argv ...string) ([]byte, error)
}

func (ExecRunner) RunWithExtraFiles(ctx context.Context, stdin io.Reader, env []string, extra []*os.File, argv ...string) ([]byte, error) {
	return runExec(ctx, stdin, env, extra, argv...)
}

func runExec(ctx context.Context, stdin io.Reader, env []string, extra []*os.File, argv ...string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, ErrEmptyArgv
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if len(extra) > 0 {
		cmd.ExtraFiles = extra
	}
	log.Printf("pannier exec: %s", strings.Join(RedactArgv(argv), " "))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, redactBytes(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

// RedactArgv copies argv and replaces password values. Use this anywhere
// argv might be logged, stored, or put in an error.
func RedactArgv(argv []string) []string {
	out := make([]string, len(argv))
	copy(out, argv)
	for i := 0; i < len(out); i++ {
		a := out[i]
		switch a {
		case "-p", "--password", "--passphrase", "--from-command",
			"--access-key", "--secret-access-key", "--access-key-id":
			if i+1 < len(out) {
				out[i+1] = "***"
				i++
			}
		default:
			for _, p := range []string{"--password=", "-p=", "--passphrase=",
				"--access-key=", "--secret-access-key=", "--access-key-id="} {
				if strings.HasPrefix(a, p) {
					out[i] = p + "***"
				}
			}
		}
	}
	return out
}

func redactBytes(b []byte) string {
	s := string(b)
	// Engine CLIs sometimes echo --password= in usage text; still scrub.
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		fields := strings.Fields(line)
		lines[i] = strings.Join(RedactArgv(fields), " ")
	}
	return strings.Join(lines, "\n")
}

func binary(r Repo, def string) string {
	if r.Binary != "" {
		return r.Binary
	}
	return def
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion-sort to stay in stdlib without pulling slices in every file
	for i := 1; i < len(keys); i++ {
		j := i
		for j > 0 && keys[j] < keys[j-1] {
			keys[j], keys[j-1] = keys[j-1], keys[j]
			j--
		}
	}
	return keys
}
