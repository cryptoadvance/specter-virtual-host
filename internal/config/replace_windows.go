//go:build windows

package config

import (
	"errors"
	"os"
)

func replaceFile(source, destination string) error {
	err := os.Rename(source, destination)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
		return err
	}
	if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}
