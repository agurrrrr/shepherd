package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agurrrrr/shepherd/internal/config"
)

func pidFilePath() string {
	return filepath.Join(config.GetConfigDir(), "shepherd.pid")
}

// WritePID writes the current process PID to the PID file.
func WritePID() error {
	pid := os.Getpid()
	return os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)), 0644)
}

// RemovePID removes the PID file.
func RemovePID() error {
	return os.Remove(pidFilePath())
}

// ReadPID reads the PID from the PID file.
func ReadPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID file: %w", err)
	}
	return pid, nil
}
