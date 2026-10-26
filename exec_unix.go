//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execReplace ersetzt den aktuellen Prozess durch den Zielprozess,
// damit die Shell im Container direkt an das Terminal gebunden ist.
func execReplace(path string, argv []string) error {
	return syscall.Exec(path, argv, os.Environ())
}
