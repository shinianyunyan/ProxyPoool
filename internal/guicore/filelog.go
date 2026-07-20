package guicore

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	logFileCapacity = 5 * 1024 * 1024
	logBackupCount  = 3
)

type rollingLog struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	backups int
	file    *os.File
	size    int64
}

func newRollingLog(path string) *rollingLog {
	return &rollingLog{
		path:    path,
		maxSize: logFileCapacity,
		backups: logBackupCount,
	}
}

func (l *rollingLog) Open() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	if info, err := os.Stat(l.path); err == nil && info.Size() >= l.maxSize {
		if err := l.rotateLocked(); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_ = file.Chmod(0o600)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	l.file = file
	l.size = info.Size()
	return nil
}

func (l *rollingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return 0, os.ErrClosed
	}
	originalLength := len(p)
	if int64(len(p)) > l.maxSize {
		p = p[len(p)-int(l.maxSize):]
	}
	if l.size > 0 && l.size+int64(len(p)) > l.maxSize {
		if err := l.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return originalLength, err
}

func (l *rollingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	l.size = 0
	return err
}

func (l *rollingLog) Tail(maxBytes int64) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.Open(l.path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - maxBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	return string(data)
}

func (l *rollingLog) rotateLocked() error {
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return err
		}
		l.file = nil
	}
	for index := l.backups; index >= 1; index-- {
		source := l.path + "." + strconv.Itoa(index)
		destination := l.path + "." + strconv.Itoa(index+1)
		_ = os.Remove(destination)
		if err := os.Rename(source, destination); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_ = os.Remove(l.path + ".1")
	if err := os.Rename(l.path, l.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_ = file.Chmod(0o600)
	l.file = file
	l.size = 0
	return nil
}
