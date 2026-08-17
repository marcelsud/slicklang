//go:build aix || js || plan9 || wasip1

package compiler

import (
	"errors"
	"os"
)

func tryPackageFileLock(_ *os.File) (bool, error) {
	return false, errors.New("advisory package locking is unsupported on this platform")
}

func unlockPackageFile(_ *os.File) error {
	return nil
}
