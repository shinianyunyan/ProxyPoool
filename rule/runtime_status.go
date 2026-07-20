package rule

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nadoo/glider/pkg/log"
)

const runtimeStatusEnv = "GLIDER_GUI_RUNTIME_STATUS_FILE"

type runtimeStatus struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Current     string             `json:"current,omitempty"`
	Forwarders  []runtimeForwarder `json:"forwarders"`
}

type runtimeForwarder struct {
	URL          string     `json:"url"`
	Address      string     `json:"address"`
	Enabled      bool       `json:"enabled"`
	Failures     uint32     `json:"failures"`
	LatencyMS    int64      `json:"latencyMs"`
	LastCheck    *time.Time `json:"lastCheck,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
	LastSelected *time.Time `json:"lastSelected,omitempty"`
}

var runtimeGroups = struct {
	sync.RWMutex
	groups  []*FwdrGroup
	started bool
	dirty   atomic.Bool
}{}

func registerRuntimeGroup(group *FwdrGroup) {
	path := os.Getenv(runtimeStatusEnv)
	if path == "" {
		return
	}

	runtimeGroups.Lock()
	runtimeGroups.groups = append(runtimeGroups.groups, group)
	runtimeGroups.dirty.Store(true)
	if runtimeGroups.started {
		runtimeGroups.Unlock()
		return
	}
	runtimeGroups.started = true
	runtimeGroups.Unlock()

	go writeRuntimeStatusLoop(path)
}

func writeRuntimeStatusLoop(path string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if runtimeGroups.dirty.Swap(false) {
			if err := writeRuntimeStatus(path, snapshotRuntimeGroups()); err != nil {
				runtimeGroups.dirty.Store(true)
				log.F("[runtime-status] write failed: %s", err)
			}
		}
		<-ticker.C
	}
}

func markRuntimeDirty() {
	runtimeGroups.dirty.Store(true)
}

func snapshotRuntimeGroups() runtimeStatus {
	runtimeGroups.RLock()
	groups := append([]*FwdrGroup(nil), runtimeGroups.groups...)
	runtimeGroups.RUnlock()

	return snapshotGroups(groups)
}

func snapshotGroups(groups []*FwdrGroup) runtimeStatus {
	status := runtimeStatus{
		GeneratedAt: time.Now(),
		Forwarders:  make([]runtimeForwarder, 0),
	}
	var latestSelection time.Time
	for _, group := range groups {
		for _, forwarder := range group.fwdrs {
			item := forwarder.runtimeStatus()
			if item.URL == "" {
				continue
			}
			status.Forwarders = append(status.Forwarders, item)
			if item.LastSelected != nil && item.LastSelected.After(latestSelection) {
				latestSelection = *item.LastSelected
				status.Current = item.URL
			}
		}
	}
	return status
}

func (f *Forwarder) runtimeStatus() runtimeForwarder {
	f.statusMu.RLock()
	lastCheck := f.lastCheck
	lastSelected := f.lastSelected
	lastError := f.lastError
	f.statusMu.RUnlock()

	item := runtimeForwarder{
		URL:       f.URL(),
		Address:   f.Addr(),
		Enabled:   f.Enabled(),
		Failures:  f.Failures(),
		LatencyMS: time.Duration(f.Latency()).Milliseconds(),
		LastError: lastError,
	}
	if !lastCheck.IsZero() {
		item.LastCheck = &lastCheck
	}
	if !lastSelected.IsZero() {
		item.LastSelected = &lastSelected
	}
	return item
}

func writeRuntimeStatus(path string, status runtimeStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".runtime-status-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tempPath, path)
}
