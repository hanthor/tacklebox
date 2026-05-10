package runner

import (
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Fprintf(os.Stdout, "+ %s %s\n", name, strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
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
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}
