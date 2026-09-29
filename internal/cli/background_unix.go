//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

func startBackground() error {
	executable := os.Getenv("APPIMAGE")
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	command := exec.Command(executable, "serve", "--headless", "--background-child")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return command.Start()
}
