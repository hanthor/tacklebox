package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var inFlatpak = sync.OnceValue(func() bool {
	_, err := os.Stat("/.flatpak-info")
	return err == nil
})

// Verbose toggles whether the "+ cmd args" trace and child stdout/stderr are
// streamed to the parent. Defaults to true to preserve existing behavior; the
// CLI lowers this when --verbose is not set.
var Verbose = true

func HostArgs(name string, args []string) (string, []string) {
	if inFlatpak() {
		return "flatpak-spawn", append([]string{"--host", name}, args...)
	}
	return name, args
}

func DefaultRun(stdin io.Reader, name string, args ...string) error {
	name, args = HostArgs(name, args)
	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}

	// Always capture stderr so failure messages have context, even in quiet mode.
	var stderrBuf bytes.Buffer
	if Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
		fmt.Fprintf(os.Stdout, "+ %s %s\n", name, strings.Join(args, " "))
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = &stderrBuf
	}

	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderrBuf.String())
		if len(tail) > 400 {
			tail = "..." + tail[len(tail)-400:]
		}
		if tail != "" {
			return fmt.Errorf("%s %s: %w\nstderr: %s", name, strings.Join(args, " "), err, tail)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

var RunFn = DefaultRun

func Run(name string, args ...string) error {
	return RunFn(nil, name, args...)
}

func RunWithStdin(stdin io.Reader, name string, args ...string) error {
	return RunFn(stdin, name, args...)
}

func Output(name string, args ...string) ([]byte, error) {
	name, args = HostArgs(name, args)
	cmd := exec.Command(name, args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	out, err := cmd.Output()
	if err != nil {
		tail := strings.TrimSpace(stderrBuf.String())
		if tail != "" {
			return nil, fmt.Errorf("%s %s: %w\nstderr: %s", name, strings.Join(args, " "), err, tail)
		}
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// RunCombined runs a command and returns combined stdout+stderr regardless of
// exit status, plus the error. Useful for commands like sgdisk where the
// caller wants to inspect output to decide whether a non-zero exit is fatal.
func RunCombined(name string, args ...string) ([]byte, error) {
	name, args = HostArgs(name, args)
	cmd := exec.Command(name, args...)
	if Verbose {
		fmt.Fprintf(os.Stdout, "+ %s %s\n", name, strings.Join(args, " "))
	}
	return cmd.CombinedOutput()
}
