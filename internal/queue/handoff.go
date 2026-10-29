package queue

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// Rate-limit handoff (#674/#675). When a run hits a rate limit and the user has
// configured limit_handoff_provider, the processor does NOT wait out a backoff
// retry. It creates a follow-up task (priority 1) that carries the target
// provider and model as per-task overrides, so the same line of work continues
// immediately on another provider.
//
// The decision is a pure function so it can be unit tested without a daemon, a
// DB, or a live provider.

// handoffProgressChars bounds how much of the failed attempt's output is
// carried into the follow-up prompt. The tail matters most: it holds what the
// agent had actually done so far.
const handoffProgressChars = 4000

// limitHandoffDecision describes what the processor should do after a rate
// limit error.
type limitHandoffDecision struct {
	// Handoff is true when the processor must create a follow-up task on the
	// target provider instead of sleeping and retrying in place.
	Handoff bool
	// Provider / Model are the target overrides to attach to the follow-up.
	Provider string
	Model    string
	// Reason explains, for logs, why handoff was not chosen. Empty when
	// Handoff is true.
	Reason string
}

// DecideLimitHandoff picks between an immediate provider handoff and the plain
// backoff retry. Handoff requires ALL of:
//
//   - limit_handoff_provider is set and valid (config.LimitHandoffTarget);
//   - the target differs from the provider that just failed — handing a task
//     back to the same provider would hit the same limit;
//   - the target provider is enabled in settings;
//   - this task was not itself produced by a handoff. A task already carrying a
//     provider_override (or handoffDepth > 0) is a continuation, so a second
//     rate limit there falls back to the plain retry instead of chaining
//     handoffs forever (#674 requirement 5).
//
// currentProvider is the provider the failed attempt actually ran on (the
// sheep's column, or the task's override when one is set).
func DecideLimitHandoff(currentProvider string, taskHasProviderOverride bool, taskHandoffDepth int) limitHandoffDecision {
	target, model := config.LimitHandoffTarget()
	if target == "" {
		return limitHandoffDecision{Reason: "limit_handoff_provider is not set"}
	}
	if currentProvider == "auto" {
		currentProvider = "claude"
	}
	if target == currentProvider {
		return limitHandoffDecision{Reason: fmt.Sprintf("handoff target %q equals the current provider", target)}
	}
	if !config.IsProviderEnabled(target) {
		return limitHandoffDecision{Reason: fmt.Sprintf("handoff target %q is disabled in settings", target)}
	}
	if taskHasProviderOverride || taskHandoffDepth > 0 {
		return limitHandoffDecision{Reason: "task is already a handoff continuation; retrying instead of chaining"}
	}
	return limitHandoffDecision{Handoff: true, Provider: target, Model: model}
}

// BuildLimitHandoffPrompt composes the follow-up task prompt: an i18n header
// naming the new target, then the tail of the failed attempt's output so the
// continuation knows how far the previous run got. Older output is dropped with
// an explicit truncation marker.
func BuildLimitHandoffPrompt(originalPrompt, targetProvider, targetModel string, outputLines []string) string {
	target := targetProvider
	if targetModel != "" {
		target = targetProvider + "/" + targetModel
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(i18n.T().Handoff.PromptHeaderFmt, target))

	tail := tailOfOutput(outputLines, handoffProgressChars)
	if tail != "" {
		if len(strings.Join(outputLines, "")) > handoffProgressChars {
			b.WriteString(i18n.T().Handoff.ProgressTruncated)
		}
		b.WriteString(tail)
		b.WriteString("\n\n")
	}

	b.WriteString(originalPrompt)
	return b.String()
}

// tailOfOutput joins output lines from the end until the byte budget is used,
// then returns what it kept (oldest kept line first).
func tailOfOutput(lines []string, budget int) string {
	total := 0
	start := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		n := len(lines[i]) + 1
		if total+n > budget {
			break
		}
		total += n
		start = i
	}
	return strings.Join(lines[start:], "")
}

// tryRateLimitHandoff runs the #675 handoff path after ExecuteInteractive
// returned a rate limit error. Returns true when the follow-up was created and
// the original task was finished (the caller must stop retrying); false when
// any condition fails and the caller should fall through to the existing
// backoff retry.
//
// The original task is completed — not failed — so incrementConsecutiveFailures
// is never reached and a rate-limit storm cannot trip the circuit breaker. Its
// output (including the handoff notice line) is saved first; the follow-up is
// then created with priority 1, the target provider/model overrides, and the
// tail of this attempt's output prepended to its prompt as progress context.
// When the original task was linked to an issue, the follow-up inherits the
// issue edge so the partial unique index (one pending/running task per issue,
// #627) stays satisfied: the original row is already terminal by then.
func (p *Processor) tryRateLimitHandoff(sheepName, projectName string, taskID int, prompt string, outputLines []string, coalescer *worker.LineCoalescer, taskProviderOverride string) bool {
	current := taskProviderOverride
	if current == "" {
		if s, err := worker.GetProvider(sheepName); err == nil {
			current = string(s)
		}
	}

	taskDepth := GetTaskHandoffDepth(taskID)
	dec := DecideLimitHandoff(current, taskProviderOverride != "", taskDepth)
	if !dec.Handoff {
		log.Printf("[processor] task #%d: rate limit handoff skipped (%s); using backoff retry", taskID, dec.Reason)
		return false
	}

	s, err := worker.Get(sheepName)
	if err != nil {
		log.Printf("[processor] task #%d: rate limit handoff skipped (sheep lookup failed: %v); using backoff retry", taskID, err)
		return false
	}

	// The task's own project edge wins over the sheep's assignment (MCP-specified
	// projects, #629); the sheep's project is the fallback.
	projectID := 0
	if s.Edges.Project != nil {
		projectID = s.Edges.Project.ID
	}

	depth := GetTaskHandoffDepth(taskID)
	followUpPrompt := BuildLimitHandoffPrompt(prompt, dec.Provider, dec.Model, outputLines)

	// Create the follow-up FIRST (without its issue edge), so the notice line
	// below — which names the follow-up's id — lands in the parent's saved
	// output. CreateFollowUpTask never sets the issue edge, so a pending
	// follow-up with a NULL issue cannot collide with the still-running parent
	// under the partial unique index (#627). We link the issue only after the
	// parent goes terminal.
	followUp, err := CreateFollowUpTask(followUpPrompt, s.ID, projectID, depth+1, dec.Model, dec.Provider)
	if err != nil {
		log.Printf("[processor] task #%d: rate limit handoff skipped (follow-up creation failed: %v); using backoff retry", taskID, err)
		return false
	}

	// Emit the notice through the coalescer so byte accounting, AppendOutput,
	// and OnOutput all see it, then flush so it is a complete line. The
	// coalescer's callback appends to executeTask's own outputLines variable —
	// NOT to our parameter copy — so we also append the notice to the local
	// slice that gets saved into the parent row. The trailing '\n' guarantees
	// the coalescer emits it as exactly one line, keeping live and stored
	// output identical.
	notice := fmt.Sprintf(i18n.T().Handoff.NoticeFmt, handoffTargetLabel(dec.Provider, dec.Model), followUp.ID)
	coalescer.Flush()
	coalescer.Append(notice)
	coalescer.Flush()
	outputLines = append(outputLines, notice)

	// Complete — not fail — the parent so incrementConsecutiveFailures is never
	// reached (the circuit breaker stays untouched) and a linked issue moves to
	// testing rather than failed. Its full output (with the notice) is saved.
	if err := completeTaskForHandoff(taskID, followUp.ID, outputLines); err != nil {
		// Parent completion failed, but the follow-up already exists. Do not
		// retry the rate-limited provider on top of that: finish this attempt
		// and let the follow-up carry the work. Log loudly for operators.
		log.Printf("[processor] task #%d: rate limit handoff follow-up #%d created but completing parent failed: %v", taskID, followUp.ID, err)
		if p.OnStatusChange != nil {
			p.OnStatusChange(sheepName, "idle")
		}
		return true
	}

	// Inherit the issue link (#674): the continuation keeps working the same
	// issue. The parent row is now terminal, so the partial unique index that
	// allows at most one pending/running task per issue accepts the new link.
	if issueID := getTaskIssueID(taskID); issueID > 0 {
		linkTaskToIssue(followUp.ID, issueID)
	}

	log.Printf("[processor] task #%d: rate limit on %s → handoff follow-up #%d on %s (priority 1)", taskID, current, followUp.ID, dec.Provider)

	if p.OnStatusChange != nil {
		p.OnStatusChange(sheepName, "idle")
	}
	if p.OnTaskComplete != nil {
		p.OnTaskComplete(taskID, sheepName, projectName, fmt.Sprintf(i18n.T().Handoff.StoppedReasonFmt, followUp.ID))
	}
	return true
}

// handoffTargetLabel renders "provider" or "provider/model" for user-visible
// text.
func handoffTargetLabel(provider, model string) string {
	if model != "" {
		return provider + "/" + model
	}
	return provider
}

// completeTaskForHandoff finishes the rate-limited parent task as completed,
// preserving its output and recording the handoff as the summary. Completed (not
// failed) is deliberate: it keeps consecutive-failure counts and issue status
// sane while the follow-up continues the work.
func completeTaskForHandoff(taskID int, followUpID int, outputLines []string) error {
	return CompleteTaskWithOutput(
		taskID,
		fmt.Sprintf(i18n.T().Handoff.StoppedReasonFmt, followUpID),
		nil,
		outputLines,
	)
}

// getTaskIssueID returns the issue id linked to a task, or 0 when the task has
// no issue edge (plain queue work).
func getTaskIssueID(taskID int) int {
	ctx := context.Background()
	t, err := db.Client().Task.Query().
		Where(task.ID(taskID)).
		WithIssue().
		Only(ctx)
	if err != nil || t.Edges.Issue == nil {
		return 0
	}
	return t.Edges.Issue.ID
}

// linkTaskToIssue attaches an issue edge to a task (used to carry the parent's
// issue onto the handoff follow-up). Best effort — a failure only means the
// follow-up runs as plain queue work without auto issue-status updates.
func linkTaskToIssue(taskID, issueID int) {
	ctx := context.Background()
	if _, err := db.Client().Task.UpdateOneID(taskID).SetIssueID(issueID).Save(ctx); err != nil {
		log.Printf("[queue] handoff: link task #%d to issue #%d: %v", taskID, issueID, err)
	}
}
