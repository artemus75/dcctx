//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
)

// execReplace startet den Zielprozess mit durchgereichtem Terminal und
// beendet dcctx mit dessen Exit-Code (Windows kennt kein exec(2)).
func execReplace(path string, argv []string) error {
	cmd := exec.Command(path, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
