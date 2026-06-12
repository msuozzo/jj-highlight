package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// startPager approximates jj's pager handling logic.
// If stdout is a TTY it pipes everything through a pager (default "less -FRX",
// overridable via $PAGER). It writes to stdout directly when stdout is not a
// TTY (so piping into other tools still works) or when pager explicitly
// disabled via noPager. It returns the writer to render into and a finalize to
// call once writing is done. For the pager path finalize closes the pipe and
// waits for the pager to exit.
func startPager(noPager bool) (w io.Writer, finalize func() error) {
	if noPager || !isTTY(os.Stdout) {
		return os.Stdout, func() error { return nil }
	}
	name, args := pagerSpec()
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return os.Stdout, func() error { return nil }
	}
	if err := cmd.Start(); err != nil {
		// Pager binary missing or unusable. Fall through to stdout.
		pipe.Close()
		return os.Stdout, func() error { return nil }
	}
	return pipe, func() error {
		pipe.Close()
		return cmd.Wait()
	}
}

// pagerSpec returns the pager command and args. $PAGER wins, shell-split by
// whitespace (quoted args aren't handled, matching most CLIs' expectation
// that $PAGER is a simple command). Otherwise jj's default "less -FRX": -F
// quits if everything fits on one screen, -R passes ANSI through, -X skips
// the screen-clear on exit.
func pagerSpec() (string, []string) {
	if p := strings.TrimSpace(os.Getenv("PAGER")); p != "" {
		fields := strings.Fields(p)
		return fields[0], fields[1:]
	}
	return "less", []string{"-FRX"}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
