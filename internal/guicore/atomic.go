package guicore

import (
	"fmt"
	"os"
	"path/filepath"
)

func atomicWriteFile(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()

	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = replaceFile(tempPath, path); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	return nil
}
