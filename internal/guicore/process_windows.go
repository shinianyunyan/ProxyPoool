//go:build windows

package guicore

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func interruptProcess(cmd *exec.Cmd) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}
