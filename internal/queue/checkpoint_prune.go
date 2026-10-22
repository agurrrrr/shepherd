package queue

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
)

// The daemon prunes once shortly after startup, then daily. Objects themselves
// are left to git's default gc policy (unreachable objects after two weeks).
const (
	checkpointPruneInitialDelay = time.Minute
	checkpointPruneInterval     = 24 * time.Hour
)

// StartCheckpointPruner runs PruneTaskCheckpoints once after the initial delay
// and then every 24 hours until stop is closed. It is safe to call from a
// goroutine during daemon startup.
func StartCheckpointPruner(stop <-chan struct{}) {
	delay := time.NewTimer(checkpointPruneInitialDelay)
	defer delay.Stop()

	select {
	case <-delay.C:
	case <-stop:
		return
	}

	prune := func() {
		days := config.GetInt("task_checkpoint_retention_days")
		if days <= 0 {
			return
		}
		if _, err := PruneTaskCheckpoints(context.Background(), days); err != nil {
			log.Printf("[checkpoint] prune: %v", err)
		}
	}

	prune()

	ticker := time.NewTicker(checkpointPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			prune()
		case <-stop:
			return
		}
	}
}

// PruneTaskCheckpoints deletes checkpoint refs for tasks that no longer exist
// or whose checkpoint lifetime (completed_at, or reverted_at after a revert)
// is older than retentionDays. A retention of 0 or less disables pruning.
// It returns the number of refs it deleted.
func PruneTaskCheckpoints(ctx context.Context, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	client := db.Client()
	rows, err := client.Task.Query().
		Select(
			entTask.FieldID,
			entTask.FieldCheckpointDir,
			entTask.FieldCompletedAt,
			entTask.FieldRevertedAt,
		).
		All(ctx)
	if err != nil {
		return 0, err
	}

	type taskAge struct {
		completed time.Time
		reverted  time.Time
	}
	byID := make(map[int]taskAge, len(rows))
	dirs := make(map[string]struct{})
	for _, t := range rows {
		byID[t.ID] = taskAge{completed: t.CompletedAt, reverted: t.RevertedAt}
		if t.CheckpointDir != "" {
			dirs[t.CheckpointDir] = struct{}{}
		}
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	total := 0
	var errs []error

	for dir := range dirs {
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		// Not a git repository (or unreadable): nothing we can prune here.
		if _, _, probeErr := checkpoint.Probe(ctx, dir); probeErr != nil {
			continue
		}

		refs, listErr := checkpoint.ListTaskRefs(ctx, dir)
		if listErr != nil {
			errs = append(errs, listErr)
			continue
		}

		var stale []int
		count := 0
		for id, taskRefs := range refs {
			age, exists := byID[id]
			if !exists {
				stale = append(stale, id)
				count += len(taskRefs)
				continue
			}
			// Unfinished tasks can still be reverted or inspected, so keep them.
			base := age.completed
			if !age.reverted.IsZero() {
				base = age.reverted
			}
			if base.IsZero() || !base.Before(cutoff) {
				continue
			}
			stale = append(stale, id)
			count += len(taskRefs)
		}

		if len(stale) == 0 {
			continue
		}
		if delErr := checkpoint.DeleteTaskRefs(ctx, dir, stale...); delErr != nil {
			errs = append(errs, delErr)
			continue
		}
		total += count
		log.Printf("[checkpoint] pruned %d refs in %s", count, dir)
	}

	return total, errors.Join(errs...)
}
