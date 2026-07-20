package guicore

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultStopTimeout = 5 * time.Second
	logTailCapacity    = 256 * 1024
)

var ErrAlreadyRunning = errors.New("glider is already running")

// Status is an immutable snapshot of a managed glider process.
type Status struct {
	Running   bool      `json:"running"`
	PID       int       `json:"pid"`
	StartTime time.Time `json:"startTime"`
	ExitError string    `json:"exitError,omitempty"`
}

// Manager starts and stops one glider process.
type Manager struct {
	mu                sync.Mutex
	executable        string
	configPath        string
	runtimeStatusPath string
	cmd               *exec.Cmd
	done              chan struct{}
	status            Status
	stopping          bool
	logs              *rollingLog
}

// NewManager manages the current executable using configPath.
func NewManager(configPath string) (*Manager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find current executable: %w", err)
	}
	return NewManagerWithExecutable(executable, configPath), nil
}

// NewManagerWithExecutable is like NewManager but uses an explicit executable.
func NewManagerWithExecutable(executable, configPath string) *Manager {
	return &Manager{
		executable:        executable,
		configPath:        configPath,
		runtimeStatusPath: filepath.Join(filepath.Dir(configPath), "runtime-status.json"),
		logs:              newRollingLog(filepath.Join(filepath.Dir(configPath), "glider.log")),
	}
}

// Start launches glider with "-config <managed config>".
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.status.Running {
		return ErrAlreadyRunning
	}
	if m.executable == "" {
		return errors.New("executable path must not be empty")
	}
	if m.configPath == "" {
		return errors.New("config path must not be empty")
	}

	if err := m.logs.Open(); err != nil {
		return fmt.Errorf("open glider log: %w", err)
	}
	_, _ = fmt.Fprintf(m.logs, "\n--- glider started at %s ---\n", time.Now().Format(time.RFC3339))
	_ = os.Remove(m.runtimeStatusPath)
	cmd := exec.Command(m.executable, "-config", m.configPath)
	cmd.Env = append(os.Environ(), "GLIDER_GUI_RUNTIME_STATUS_FILE="+m.runtimeStatusPath)
	prepareCommand(cmd)
	cmd.Stdout = m.logs
	cmd.Stderr = m.logs
	if err := cmd.Start(); err != nil {
		_ = m.logs.Close()
		return fmt.Errorf("start glider: %w", err)
	}

	done := make(chan struct{})
	m.cmd = cmd
	m.done = done
	m.stopping = false
	m.status = Status{
		Running:   true,
		PID:       cmd.Process.Pid,
		StartTime: time.Now(),
	}
	go m.wait(cmd, done)
	return nil
}

// Stop requests graceful termination, killing the process after timeout.
func (m *Manager) Stop(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}

	m.mu.Lock()
	if !m.status.Running || m.cmd == nil {
		m.mu.Unlock()
		return nil
	}
	cmd := m.cmd
	done := m.done
	m.stopping = true
	m.mu.Unlock()

	_ = interruptProcess(cmd)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill glider after timeout: %w", err)
	}
	<-done
	return nil
}

// Status returns a race-free process status snapshot.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Logs returns the newest captured stdout and stderr from the rolling log file.
func (m *Manager) Logs() string {
	return m.logs.Tail(logTailCapacity)
}

func (m *Manager) wait(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	_, _ = fmt.Fprintf(m.logs, "\n--- glider stopped at %s ---\n", time.Now().Format(time.RFC3339))
	_ = m.logs.Close()

	m.mu.Lock()
	if m.cmd == cmd {
		m.status.Running = false
		m.status.PID = 0
		if err != nil && !m.stopping {
			m.status.ExitError = err.Error()
		} else {
			m.status.ExitError = ""
		}
		m.cmd = nil
		m.done = nil
		m.stopping = false
	}
	close(done)
	m.mu.Unlock()
}
