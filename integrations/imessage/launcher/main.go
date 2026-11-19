// Command launcher runs the iMessage connector from a stable, signed app
// bundle, so macOS privacy permissions (Full Disk Access for the Messages
// database, Automation for sending) can be granted to this one small app
// instead of to python3 or a terminal.
//
// It starts its arguments as a child and stays its parent: macOS attributes
// the child's file and Apple Event access to this app. Replacing itself with
// exec would hand that identity to the interpreter instead. Signals are
// passed on, and the child's exit status is returned so launchd can restart.
//
// It changes rarely on purpose: macOS ties permissions to the exact signed
// program, and connector updates should not need permissions granted again.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: launcher PROGRAM [ARGS...]")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "launcher:", err)
		os.Exit(1)
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for s := range sigs {
			cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			os.Exit(128 + int(status.Signal()))
		}
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "launcher:", err)
		os.Exit(1)
	}
}
