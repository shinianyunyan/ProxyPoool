package guicore

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

const maxRuntimeStatusSize int64 = 8 << 20

type RuntimeStatus struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Current     string             `json:"current,omitempty"`
	Forwarders  []RuntimeForwarder `json:"forwarders"`
}

type RuntimeForwarder struct {
	URL          string     `json:"url"`
	Address      string     `json:"address"`
	Enabled      bool       `json:"enabled"`
	Failures     uint32     `json:"failures"`
	LatencyMS    int64      `json:"latencyMs"`
	LastCheck    *time.Time `json:"lastCheck,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
	LastSelected *time.Time `json:"lastSelected,omitempty"`
}

func (m *Manager) RuntimeStatus() RuntimeStatus {
	if !m.Status().Running {
		return RuntimeStatus{Forwarders: []RuntimeForwarder{}}
	}
	status, err := loadRuntimeStatus(m.runtimeStatusPath)
	if err != nil {
		return RuntimeStatus{Forwarders: []RuntimeForwarder{}}
	}
	return status
}

func loadRuntimeStatus(path string) (RuntimeStatus, error) {
	file, err := os.Open(path)
	if err != nil {
		return RuntimeStatus{}, err
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, maxRuntimeStatusSize+1))
	var status RuntimeStatus
	if err := decoder.Decode(&status); err != nil {
		return RuntimeStatus{}, err
	}
	if status.Forwarders == nil {
		status.Forwarders = []RuntimeForwarder{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RuntimeStatus{}, errors.New("runtime status contains trailing JSON")
	}
	return status, nil
}
