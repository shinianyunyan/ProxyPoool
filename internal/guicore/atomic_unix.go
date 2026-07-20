//go:build !windows

package guicore

import "os"

func replaceFile(from, to string) error {
	return os.Rename(from, to)
}
