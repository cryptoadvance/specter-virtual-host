//go:build !windows

package activity

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
