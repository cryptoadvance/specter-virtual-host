//go:build !windows

package main

func runWindowsService(_ func(<-chan struct{}, chan<- error) error) (bool, error) {
	return false, nil
}
