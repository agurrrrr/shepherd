// Package taskchanges builds the change summary of a task from its start and
// end checkpoint snapshots. It is shared by the REST API, the CLI, and the MCP
// get_task_detail tool so all three report the same numbers.
package taskchanges

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/config"
)

// Reasons reported when a change summary cannot be produced. They are shown
// verbatim in the UI, CLI, and MCP output.
const (
	ReasonDisabled  = "checkpoint disabled"
	ReasonNotGit    = "not a git repository"
	ReasonTooLarge  = "too large"
	ReasonNoEnd     = "no end snapshot"
	ReasonPruned    = "snapshot pruned"
	ReasonRunning   = "running"
	liveWarningText = "작업이 끝난 뒤의 변경도 섞여 있습니다"
)

// File is one changed path between two snapshots.
type File struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

// Commit is a commit made in the repository while the task was running.
type Commit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

// ChangeSet is the response body of GET /api/tasks/:id/changes.
type ChangeSet struct {
	Available             bool     `json:"available"`
	Reason                string   `json:"reason"`
	Live                  bool     `json:"live"`
	Dir                   string   `json:"dir"`
	Start                 string   `json:"start"`
	End                   string   `json:"end"`
	HeadStart             string   `json:"head_start"`
	HeadEnd               string   `json:"head_end"`
	Commits               []Commit `json:"commits_during_task"`
	Files                 []File   `json:"files"`
	Additions             int      `json:"additions"`
	Deletions             int      `json:"deletions"`
	FilesModifiedMismatch []string `json:"files_modified_mismatch"`
	CheckpointError       string   `json:"checkpoint_error,omitempty"`
	Warning               string   `json:"warning,omitempty"`
}

// Build resolves a task's checkpoint snapshots and diffs them. When live is
// true and the end snapshot is missing (a running or interrupted task), it
// snapshots the current working tree without writing a ref and compares the
// start snapshot to it.
func Build(ctx context.Context, t *ent.Task, live bool) *ChangeSet {
	cs := &ChangeSet{
		Commits:               []Commit{},
		Files:                 []File{},
		FilesModifiedMismatch: []string{},
	}
	if t == nil {
		cs.Reason = ReasonDisabled
		return cs
	}
	cs.Dir = t.CheckpointDir
	cs.Start = t.CheckpointStart
	cs.End = t.CheckpointEnd
	cs.CheckpointError = t.CheckpointError

	enabled := config.GetBool("task_checkpoint_enabled")

	if t.CheckpointDir == "" {
		if enabled {
			cs.Reason = reasonFromText(t.CheckpointError, t.Status)
		} else {
			cs.Reason = ReasonDisabled
		}
		return cs
	}
	if t.CheckpointStart == "" {
		if !enabled {
			cs.Reason = ReasonDisabled
		} else {
			cs.Reason = ReasonPruned
			if r := reasonFromErrorText(t.CheckpointError); r != "" {
				cs.Reason = r
			}
		}
		return cs
	}
	if !checkpoint.CommitExists(ctx, t.CheckpointDir, t.CheckpointStart) {
		cs.Reason = ReasonPruned
		return cs
	}

	end := t.CheckpointEnd
	if end == "" {
		if !live {
			if t.Status == task.StatusRunning {
				cs.Reason = ReasonRunning
			} else {
				cs.Reason = ReasonNoEnd
			}
			return cs
		}
		snap, err := checkpoint.Take(ctx, t.CheckpointDir, "",
			fmt.Sprintf("shepherd checkpoint task #%d live", t.ID))
		if err != nil {
			cs.Reason = reasonFromError(err)
			cs.CheckpointError = appendError(cs.CheckpointError, err.Error())
			return cs
		}
		end = snap.Commit
		cs.Live = true
		if t.Status != task.StatusRunning {
			cs.Warning = liveWarningText
		}
	} else if !checkpoint.CommitExists(ctx, t.CheckpointDir, end) {
		cs.Reason = ReasonPruned
		return cs
	}
	cs.End = end

	changes, err := checkpoint.Changes(ctx, t.CheckpointDir, t.CheckpointStart, end)
	if err != nil {
		cs.Reason = reasonFromError(err)
		cs.CheckpointError = appendError(cs.CheckpointError, err.Error())
		return cs
	}

	for _, change := range changes {
		f := File{
			Path:      change.Path,
			OldPath:   change.OldPath,
			Status:    change.Status,
			Additions: change.Additions,
			Deletions: change.Deletions,
			Binary:    change.Binary,
		}
		cs.Files = append(cs.Files, f)
		cs.Additions += change.Additions
		cs.Deletions += change.Deletions
	}

	cs.HeadStart = checkpoint.Parent(ctx, t.CheckpointDir, t.CheckpointStart)
	cs.HeadEnd = checkpoint.Parent(ctx, t.CheckpointDir, end)
	if cs.HeadStart != "" && cs.HeadEnd != "" && cs.HeadStart != cs.HeadEnd {
		commits, commitErr := checkpoint.CommitsBetween(ctx, t.CheckpointDir, cs.HeadStart, cs.HeadEnd, 50)
		if commitErr == nil {
			for _, commit := range commits {
				cs.Commits = append(cs.Commits, Commit{Hash: commit.Hash, Subject: commit.Subject})
			}
		}
	}

	cs.FilesModifiedMismatch = mismatchedFiles(ctx, t.CheckpointDir, t.FilesModified, changes)
	cs.Available = true
	cs.Reason = ""
	return cs
}

// mismatchedFiles returns the reported files that do not appear in the actual
// diff. Reported paths may be absolute, repository-relative, or relative to
// the project directory, so they are normalized to the repository top level
// before comparing.
func mismatchedFiles(ctx context.Context, dir string, reported []string, changes []checkpoint.FileChange) []string {
	if len(reported) == 0 {
		return []string{}
	}
	toplevel, prefix, err := checkpoint.Probe(ctx, dir)
	if err != nil {
		return []string{}
	}

	actual := make(map[string]bool, len(changes)*2)
	for _, change := range changes {
		if change.Path != "" {
			actual[change.Path] = true
		}
		if change.OldPath != "" {
			actual[change.OldPath] = true
		}
	}

	mismatch := []string{}
	seen := map[string]bool{}
	for _, raw := range reported {
		normalized := normalizeReportedPath(raw, toplevel, prefix)
		if normalized == "" || actual[normalized] || seen[normalized] {
			continue
		}
		seen[normalized] = true
		mismatch = append(mismatch, normalized)
	}
	return mismatch
}

func normalizeReportedPath(raw, toplevel, prefix string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(toplevel, filepath.Clean(p))
		if err != nil || strings.HasPrefix(rel, "..") {
			return ""
		}
		return filepath.ToSlash(rel)
	}

	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || strings.HasPrefix(p, "../") {
		return ""
	}
	if prefix != "" {
		if strings.HasPrefix(p, prefix+"/") {
			return p
		}
		return prefix + "/" + p
	}
	return p
}

func reasonFromText(text string, status task.Status) string {
	if r := reasonFromErrorText(text); r != "" {
		return r
	}
	if !config.GetBool("task_checkpoint_enabled") {
		return ReasonDisabled
	}
	if status == task.StatusRunning {
		return ReasonRunning
	}
	return ReasonNoEnd
}

func reasonFromErrorText(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "not a git repository"), strings.Contains(lower, "not inside a git working tree"):
		return ReasonNotGit
	case strings.Contains(lower, "too large"):
		return ReasonTooLarge
	case strings.Contains(lower, "snapshot pruned"):
		return ReasonPruned
	case strings.Contains(lower, "no end snapshot"):
		return ReasonNoEnd
	}
	return ""
}

func reasonFromError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, checkpoint.ErrNotGitRepo):
		return ReasonNotGit
	case errors.Is(err, checkpoint.ErrTooLarge):
		return ReasonTooLarge
	}
	if r := reasonFromErrorText(err.Error()); r != "" {
		return r
	}
	return err.Error()
}

func appendError(existing, message string) string {
	if existing == "" {
		return message
	}
	if message == "" || strings.Contains(existing, message) {
		return existing
	}
	return existing + "; " + message
}
