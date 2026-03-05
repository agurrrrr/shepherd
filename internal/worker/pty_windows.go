//go:build windows

package worker

import (
	"context"
)

// executeInteractiveWithPty is not supported on Windows.
// Falls back to streaming mode instead.
func executeInteractiveWithPty(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	// Windows does not support PTY — use streaming mode as fallback
	return executeWithStreaming(ctx, sheepName, projectPath, sessionID, prompt, opts, cancel)
}
