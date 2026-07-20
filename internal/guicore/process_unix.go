//go:build !windows

package guicore

import (
	"os"
	"os/exec"
)

func prepareCommand(_ *exec.Cmd) {}

func interruptProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}
