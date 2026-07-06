package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/agurrrrr/shepherd/internal/magi"
)

// magiExecutor mirrors embeddedExecutor: injected from the server package
// to avoid import cycles (see SetEmbeddedExecutor).
//
// Unlike the embedded executor, there is no injectCh parameter — Phase 1
// (advisory deliberation) does not support mid-execution prompt injection.
var magiExecutor func(
	ctx context.Context,
	sheepName, projectPath string,
	prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
) (*ExecuteResult, error)

// SetMagiExecutor registers the magi executor function.
// Must be called once during application initialization.
func SetMagiExecutor(fn func(
	ctx context.Context,
	sheepName, projectPath string,
	prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
) (*ExecuteResult, error)) {
	magiExecutor = fn
}

func executeWithMagi(
	ctx context.Context,
	sheepName, projectPath string,
	prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
) (*ExecuteResult, error) {
	if magiExecutor == nil {
		return nil, fmt.Errorf("magi executor not initialized")
	}

	// Register in the running-task registry so StopTask can find and cancel
	// this work. Magi runs have no subprocess (Cmd == nil); killProcessGroup
	// already guards against nil, so this is safe. The identity token prevents
	// a late-finishing task from clobbering a newer task's entry.
	rt := registerRunningTask(sheepName, cancel, nil)
	defer unregisterRunningTask(sheepName, rt)

	result, err := magiExecutor(ctx, sheepName, projectPath, prompt, opts, cancel)

	// Fewer than 2 proposers answered — fall back to a single embedded run
	// (design §5.1). executeWithEmbedded registers its own running-task entry,
	// so unregister ours first to avoid a duplicate-entry race. The deferred
	// unregisterRunningTask is a no-op now (self-guard: token no longer matches).
	if errors.Is(err, magi.ErrInsufficientProposers) {
		unregisterRunningTask(sheepName, rt)

		if opts.OnOutput != nil {
			opts.OnOutput("🔶 단일 임베디드 실행으로 폴백\n")
		}
		return executeWithEmbedded(ctx, sheepName, projectPath, prompt, opts, cancel)
	}

	return result, err
}
