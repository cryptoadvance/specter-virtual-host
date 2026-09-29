//go:build windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

func startBackground() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "serve", "--headless", "--background-child")
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008,
		HideWindow:    true,
	}
	return command.Start()
}
