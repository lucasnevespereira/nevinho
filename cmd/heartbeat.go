package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lucasnevespereira/nevinho/logger"
	"github.com/lucasnevespereira/nevinho/safeio"
)

const heartbeatFile = "last_alive.json"

type heartbeatState struct {
	LastAlive time.Time `json:"last_alive"`
	Clean     bool      `json:"clean_shutdown"`
}

type crashHeartbeat struct {
	configDir string
	stop      chan struct{}
	done      chan struct{}
}

func heartbeatPath(configDir string) string {
	return filepath.Join(configDir, heartbeatFile)
}

func readHeartbeat(configDir string) (heartbeatState, bool, error) {
	data, err := os.ReadFile(heartbeatPath(configDir))
	if errors.Is(err, os.ErrNotExist) {
		return heartbeatState{}, false, nil
	}
	if err != nil {
		return heartbeatState{}, false, err
	}
	var state heartbeatState
	if err := json.Unmarshal(data, &state); err != nil {
		return heartbeatState{}, false, fmt.Errorf("decode heartbeat: %w", err)
	}
	if state.LastAlive.IsZero() {
		return heartbeatState{}, false, fmt.Errorf("heartbeat has no last_alive timestamp")
	}
	return state, true, nil
}

func writeHeartbeat(configDir string, clean bool) error {
	data, err := json.Marshal(heartbeatState{LastAlive: time.Now().UTC(), Clean: clean})
	if err != nil {
		return err
	}
	return safeio.WriteFile(heartbeatPath(configDir), data, 0o600)
}

// startCrashHeartbeat marks this run active, then reports an unclean prior
// exit. The notifier is called only once on boot, never on periodic ticks.
func startCrashHeartbeat(configDir string, notify func(string) error, interval time.Duration) (*crashHeartbeat, error) {
	previous, exists, err := readHeartbeat(configDir)
	if err != nil {
		logger.Err(fmt.Errorf("read crash heartbeat: %w", err))
	}
	if err := writeHeartbeat(configDir, false); err != nil {
		return nil, fmt.Errorf("start crash heartbeat: %w", err)
	}
	if err == nil && exists && !previous.Clean && notify != nil {
		msg := fmt.Sprintf("Nevinho restarted after an unclean stop. Last heartbeat: %s.", previous.LastAlive.Format(time.RFC3339))
		if err := notify(msg); err != nil {
			logger.Err(fmt.Errorf("send restart notice: %w", err))
		}
	}
	if interval <= 0 {
		interval = time.Minute
	}
	h := &crashHeartbeat{configDir: configDir, stop: make(chan struct{}), done: make(chan struct{})}
	go h.loop(interval)
	return h, nil
}

func (h *crashHeartbeat) loop(interval time.Duration) {
	defer close(h.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			if err := writeHeartbeat(h.configDir, false); err != nil {
				logger.Err(fmt.Errorf("update crash heartbeat: %w", err))
			}
		}
	}
}

func (h *crashHeartbeat) Stop() error {
	close(h.stop)
	<-h.done
	return writeHeartbeat(h.configDir, true)
}
