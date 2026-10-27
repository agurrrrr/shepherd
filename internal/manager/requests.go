// User-confirmation requests for the shepherd (#601).
//
// Anything the autonomy tier refuses (stopping tasks below `full`, deleting
// issues at ANY tier, executing under a cost cap) can still be asked for: the
// shepherd calls request_user_action, a row lands in shepherd_requests, and
// the user approves or rejects it on the Shepherd screen's needs-attention
// panel. Approving runs the action server-side with the same code paths the
// web buttons use, stores the outcome in `result`, and enqueues a shepherd
// `review` task carrying the decision (coalesced into a pending review task
// per #597's merge rule). Rejecting records the note and runs nothing.
//
// Requests are edge-free plain-id rows: deleting a target never cascades, and
// an approval against a vanished target fails softly into `result`.
package manager

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entreq "github.com/agurrrrr/shepherd/ent/shepherdrequest"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/discord"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/issue"
	"github.com/agurrrrr/shepherd/internal/push"
	"github.com/agurrrrr/shepherd/internal/queue"
)

// Request kinds (#601 §4). The kind decides what approval does.
const (
	RequestStopTask     = "stop_task"
	RequestExecuteIssue = "execute_issue"
	RequestDeleteIssue  = "delete_issue"
	RequestOther        = "other"
)

// Request statuses.
const (
	RequestPending  = "pending"
	RequestApproved = "approved"
	RequestRejected = "rejected"
)

// Result codes (#665). The `result` column must never hold a display
// language: approve/reject store a machine-readable code and every reader
// (web panel, review prompt) renders it through i18n at response time. The
// grammar is `req:<code>[:<id>][: <detail>]`; a free-text user note instead
// rides verbatim behind `note:`.
const (
	resultCodePrefix = "req:"
	noteResultPrefix = "note:"

	RequestResultRejected      = "rejected"
	RequestResultStopFailed    = "stop_failed"
	RequestResultCancelFailed  = "cancel_failed"
	RequestResultCancelDone    = "cancel_done"
	RequestResultStopDone      = "stop_done"
	RequestResultExecuteFailed = "execute_failed"
	RequestResultExecuteDone   = "execute_done"
	RequestResultDeleteFailed  = "delete_failed"
	RequestResultDeleteDone    = "delete_done"
	RequestResultOtherApproved = "other_approved"
	RequestResultUnknownKind   = "unknown_kind"
)

// requestCode builds one stored result code. Empty id/detail segments are
// kept as empty fields so RenderRequestResult can split the string back.
func requestCode(code string, id int, detail string) string {
	out := resultCodePrefix + code
	out += ":"
	if id > 0 {
		out += strconv.Itoa(id)
	}
	if detail != "" {
		out += ": " + detail
	} else {
		out += ":"
	}
	return out
}

// RenderRequestResult turns a stored result into display text. Unknown or
// legacy (pre-#665, already localized) values pass through untouched.
func RenderRequestResult(result string) string {
	result = strings.TrimSpace(result)
	if strings.HasPrefix(result, noteResultPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(result, noteResultPrefix))
	}
	if !strings.HasPrefix(result, resultCodePrefix) {
		return result
	}

	fields := strings.SplitN(strings.TrimPrefix(result, resultCodePrefix), ":", 3)
	code := fields[0]
	id, _ := strconv.Atoi(fields[1])
	detail := ""
	if len(fields) > 2 {
		detail = strings.TrimSpace(fields[2])
	}

	t := i18n.T().Requests
	switch code {
	case RequestResultRejected:
		return t.RejectedDefault
	case RequestResultStopFailed:
		return fmt.Sprintf(t.StopFailedFmt, detail)
	case RequestResultCancelFailed:
		return fmt.Sprintf(t.CancelFailedFmt, detail)
	case RequestResultCancelDone:
		return fmt.Sprintf(t.CancelDoneFmt, id)
	case RequestResultStopDone:
		return fmt.Sprintf(t.StopDoneFmt, id)
	case RequestResultExecuteFailed:
		return fmt.Sprintf(t.ExecuteFailedFmt, detail)
	case RequestResultExecuteDone:
		return fmt.Sprintf(t.ExecuteDoneFmt, id, detail)
	case RequestResultDeleteFailed:
		return fmt.Sprintf(t.DeleteFailedFmt, detail)
	case RequestResultDeleteDone:
		return fmt.Sprintf(t.DeleteDoneFmt, id)
	case RequestResultOtherApproved:
		return t.OtherApproved
	case RequestResultUnknownKind:
		return fmt.Sprintf(t.UnknownKindFmt, detail)
	default:
		return result
	}
}

// HasRequestResultCode reports whether a stored result is one of this
// package's codes (used by tests that assert the DB holds no display text).
func HasRequestResultCode(result string) bool {
	return strings.HasPrefix(strings.TrimSpace(result), resultCodePrefix) ||
		strings.HasPrefix(strings.TrimSpace(result), noteResultPrefix)
}

// ValidRequestKind reports whether kind is one of the four request kinds.
func ValidRequestKind(kind string) bool {
	switch kind {
	case RequestStopTask, RequestExecuteIssue, RequestDeleteIssue, RequestOther:
		return true
	}
	return false
}

// ShepherdRequestRecord is the API shape of one shepherd_requests row. The
// requester (task + sheep) rides along so the panel can show WHO asked — the
// spec requires showing the requesting task and sheep even for non-shepherd
// callers.
type ShepherdRequestRecord struct {
	ID              int        `json:"id"`
	Kind            string     `json:"kind"`
	ProjectName     string     `json:"project_name,omitempty"`
	TargetID        int        `json:"target_id,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	Status          string     `json:"status"`
	RequestedByTask int        `json:"requested_by_task_id,omitempty"`
	RequesterSheep  string     `json:"requester_sheep,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	Result          string     `json:"result,omitempty"`
}

// requestNotifyHook is the daemon's SSE broadcast seam (registered in
// main.go). It fires after a request is created, approved, or rejected so
// open panels refresh.
var requestNotifyHook func(rec *ShepherdRequestRecord, event string)

// RegisterRequestNotifyHook connects the SSE hub; pass nil to clear.
func RegisterRequestNotifyHook(fn func(rec *ShepherdRequestRecord, event string)) {
	requestNotifyHook = fn
}

// CreateShepherdRequest stores a new pending request and notifies (web push +
// Discord + SSE). Every caller — shepherd or not — may create one; the panel
// labels the requesting task and sheep either way.
func CreateShepherdRequest(kind, projectName string, targetID int, reason string, callerTaskID int) (*ShepherdRequestRecord, error) {
	kind = strings.TrimSpace(kind)
	if !ValidRequestKind(kind) {
		return nil, fmt.Errorf("kind must be one of stop_task, execute_issue, delete_issue, other (got %q)", kind)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("reason is required")
	}
	projectName = strings.TrimSpace(projectName)

	// Target sanity: task/issue kinds need their target, and issue kinds need
	// the project so approval can scope the lookup.
	switch kind {
	case RequestStopTask:
		if targetID <= 0 {
			return nil, fmt.Errorf("target_id (the task id) is required for stop_task")
		}
	case RequestExecuteIssue, RequestDeleteIssue:
		if targetID <= 0 {
			return nil, fmt.Errorf("target_id (the issue id) is required for %s", kind)
		}
		if projectName == "" {
			return nil, fmt.Errorf("project_name is required for %s", kind)
		}
	}

	ctx := context.Background()
	row, err := db.Client().ShepherdRequest.Create().
		SetKind(kind).
		SetProjectName(projectName).
		SetTargetID(targetID).
		SetReason(reason).
		SetStatus(RequestPending).
		SetRequestedByTaskID(callerTaskID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create shepherd request: %w", err)
	}

	rec := toRequestRecord(row)
	notifyRequestCreated(rec)
	broadcastRequest(rec, "shepherd_request_new")
	return rec, nil
}

// ListShepherdRequests returns requests newest-first, optionally filtered by
// status. limit<=0 → default 50 (max 200).
func ListShepherdRequests(status string, limit int) ([]ShepherdRequestRecord, error) {
	ctx := context.Background()
	if db.Client() == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	q := db.Client().ShepherdRequest.Query()
	if s := strings.TrimSpace(status); s != "" && s != "all" {
		q = q.Where(entreq.Status(s))
	}
	rows, err := q.Order(ent.Desc(entreq.FieldCreatedAt), ent.Desc(entreq.FieldID)).Limit(limit).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list shepherd requests: %w", err)
	}

	recs := make([]ShepherdRequestRecord, 0, len(rows))
	for _, r := range rows {
		recs = append(recs, *toRequestRecord(r))
	}
	return recs, nil
}

// toRequestRecord maps a row and resolves the requester's sheep name (one
// query per row; lists are small and only rendered in the panel).
func toRequestRecord(r *ent.ShepherdRequest) *ShepherdRequestRecord {
	rec := &ShepherdRequestRecord{
		ID:              r.ID,
		Kind:            r.Kind,
		ProjectName:     r.ProjectName,
		TargetID:        r.TargetID,
		Reason:          r.Reason,
		Status:          r.Status,
		RequestedByTask: r.RequestedByTaskID,
		CreatedAt:       r.CreatedAt,
		DecidedAt:       r.DecidedAt,
		Result:          r.Result,
	}
	if r.RequestedByTaskID > 0 {
		if t, err := queue.GetTask(r.RequestedByTaskID); err == nil && t != nil && t.Edges.Sheep != nil {
			rec.RequesterSheep = t.Edges.Sheep.Name
		}
	}
	return rec
}

// ApproveShepherdRequest runs the kind's action, stores the outcome, and
// wakes a shepherd review task with the decision. A vanished/invalid target
// is recorded in `result` — the approval itself still counts as decided, so a
// stale request cannot wedge the panel.
func ApproveShepherdRequest(id int) (*ShepherdRequestRecord, error) {
	ctx := context.Background()
	row, err := db.Client().ShepherdRequest.Query().Where(entreq.ID(id)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("request #%d not found", id)
		}
		return nil, fmt.Errorf("load shepherd request: %w", err)
	}
	if row.Status != RequestPending {
		return nil, fmt.Errorf("request #%d is already %s", id, row.Status)
	}

	result := runRequestAction(row)

	now := time.Now()
	updated, err := db.Client().ShepherdRequest.UpdateOneID(row.ID).
		SetStatus(RequestApproved).
		SetDecidedAt(now).
		SetResult(result).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("store approval: %w", err)
	}

	rec := toRequestRecord(updated)
	enqueueDecisionReview(rec, RequestApproved)
	broadcastRequest(rec, "shepherd_request_decided")
	return rec, nil
}

// RejectShepherdRequest records the rejection (with the user's note) and runs
// NO action. The shepherd still wakes: knowing the user said no is what lets
// it change course instead of waiting forever.
func RejectShepherdRequest(id int, note string) (*ShepherdRequestRecord, error) {
	ctx := context.Background()
	row, err := db.Client().ShepherdRequest.Query().Where(entreq.ID(id)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("request #%d not found", id)
		}
		return nil, fmt.Errorf("load shepherd request: %w", err)
	}
	if row.Status != RequestPending {
		return nil, fmt.Errorf("request #%d is already %s", id, row.Status)
	}

	// The user's note is free text and rides verbatim behind a `note:` marker;
	// an empty note stores the language-neutral `rejected` code so the DB never
	// holds a display language (#665).
	result := strings.TrimSpace(note)
	if result == "" {
		result = requestCode(RequestResultRejected, 0, "")
	} else {
		result = noteResultPrefix + result
	}

	now := time.Now()
	updated, err := db.Client().ShepherdRequest.UpdateOneID(row.ID).
		SetStatus(RequestRejected).
		SetDecidedAt(now).
		SetResult(result).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("store rejection: %w", err)
	}

	rec := toRequestRecord(updated)
	enqueueDecisionReview(rec, RequestRejected)
	broadcastRequest(rec, "shepherd_request_decided")
	return rec, nil
}

// runRequestAction executes the approved action. Return text always lands in
// `result` (success or failure) — an approval never returns a bare error to
// the UI for target problems, only for infrastructure failures above.
func runRequestAction(row *ent.ShepherdRequest) string {
	switch row.Kind {
	case RequestStopTask:
		// #626: one user-facing "작업 중지" request covers both queue states.
		// A pending row has no provider process to kill, so approval runs the
		// same cancel path as POST /api/tasks/:id/cancel (queue.CancelPending-
		// Task); a running row keeps queue.StopTaskByID (= POST /api/tasks/:id/
		// stop). The UI and the request kind stay unchanged.
		t, err := queue.GetTask(row.TargetID)
		if err != nil {
			return requestCode(RequestResultStopFailed, 0, err.Error())
		}
		if t.Status == entTask.StatusPending {
			if err := queue.CancelPendingTask(row.TargetID); err != nil {
				return requestCode(RequestResultCancelFailed, 0, err.Error())
			}
			return requestCode(RequestResultCancelDone, row.TargetID, "")
		}
		if err := queue.StopTaskByID(row.TargetID, "stopped by shepherd request"); err != nil {
			return requestCode(RequestResultStopFailed, 0, err.Error())
		}
		return requestCode(RequestResultStopDone, row.TargetID, "")

	case RequestExecuteIssue:
		res, err := issue.Execute(issue.ExecuteInput{
			Project: row.ProjectName,
			IssueID: row.TargetID,
			Watch:   true, // an approved delegation is watched work (#597)
		})
		if err != nil {
			return requestCode(RequestResultExecuteFailed, 0, err.Error())
		}
		if wakeProcessor != nil {
			wakeProcessor.ProcessPendingNow()
		}
		return requestCode(RequestResultExecuteDone, row.TargetID, fmt.Sprint(res.TaskIDs))

	case RequestDeleteIssue:
		// Deletion is ONLY reachable through this approved path — no MCP tool
		// deletes issues at any autonomy tier (#601 §1).
		if err := issue.Delete(row.ProjectName, row.TargetID); err != nil {
			return requestCode(RequestResultDeleteFailed, 0, err.Error())
		}
		return requestCode(RequestResultDeleteDone, row.TargetID, "")

	case RequestOther:
		return requestCode(RequestResultOtherApproved, 0, "")

	default:
		// The verb in UnknownKindFmt adds the quoting itself.
		return requestCode(RequestResultUnknownKind, 0, row.Kind)
	}
}

const decisionReviewHeader = `[Shepherd request decision]
The user decided on a request you (or another sheep) raised. Follow up per the review procedure; do NOT re-create the same request.`

// enqueueDecisionReview wakes the shepherd with the decision, merged into a
// pending review task when one exists (#597 coalescing rule).
func enqueueDecisionReview(rec *ShepherdRequestRecord, decision string) {
	block := fmt.Sprintf("- request #%d (%s): %s — target: %s, project: %s\n- reason given: %s\n- user result: %s",
		rec.ID, kindLabel(rec.Kind), decision, requestTargetLabel(rec), orDash(rec.ProjectName), rec.Reason,
		RenderRequestResult(rec.Result))

	appendMu.Lock()
	appended := queue.AppendToPendingReviewTaskPrompt(decisionReviewHeader + "\n\n" + block)
	appendMu.Unlock()

	if appended {
		log.Printf("[shepherd-request] #%d decision merged into a pending review task", rec.ID)
		return
	}

	t, err := EnqueueShepherdTask(KindReview, decisionReviewHeader+"\n\n"+block, 0)
	if err != nil {
		log.Printf("[shepherd-request] enqueue review for request #%d: %v", rec.ID, err)
		return
	}
	log.Printf("[shepherd-request] #%d %s → enqueued shepherd review task #%d", rec.ID, decision, t.ID)

	if wakeProcessor != nil {
		wakeProcessor.ProcessPendingNow()
	}
}

func kindLabel(kind string) string {
	switch kind {
	case RequestStopTask:
		return i18n.T().Requests.KindStopTask
	case RequestExecuteIssue:
		return i18n.T().Requests.KindExecuteIssue
	case RequestDeleteIssue:
		return i18n.T().Requests.KindDeleteIssue
	default:
		return i18n.T().Requests.KindOther
	}
}

func requestTargetLabel(rec *ShepherdRequestRecord) string {
	switch rec.Kind {
	case RequestStopTask:
		return fmt.Sprintf("task #%d", rec.TargetID)
	case RequestExecuteIssue, RequestDeleteIssue:
		return fmt.Sprintf("issue #%d", rec.TargetID)
	default:
		if rec.TargetID > 0 {
			return fmt.Sprintf("#%d", rec.TargetID)
		}
		return i18n.T().Requests.NoTarget
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// ---- notifications ---------------------------------------------------------

// notifyRequestCreated sends the web-push + Discord notice for a new pending
// request (#601 §4: "기존 웹 푸시와 Discord 알림 함수로 알립니다"). The push
// payload is built inline because push.Payload's task-shaped helpers do not
// fit requests; both channels stay config-gated.
func notifyRequestCreated(rec *ShepherdRequestRecord) {
	title := fmt.Sprintf(i18n.T().Push.RequestTitleFmt, rec.ID)
	body := fmt.Sprintf(i18n.T().Push.RequestAwaitingFmt, kindLabel(rec.Kind), firstLine(rec.Reason, i18n.T().Requests.NoReason))

	if config.GetBool("webpush_enabled") {
		if _, err := push.SendToAll(push.Payload{
			Title:  title,
			Body:   body,
			URL:    "/shepherd?tab=attention", // the request waits in the 확인 필요 tab (#623)
			Tag:    fmt.Sprintf("shepherd-request-%d", rec.ID),
			Status: "warning",
		}, true); err != nil {
			log.Printf("[shepherd-request] web push #%d: %v", rec.ID, err)
		}
	}

	if config.GetBool("discord_notifications_enabled") {
		webhookURL := config.GetString("discord_webhook_url")
		if webhookURL != "" {
			if err := discord.New().Send(webhookURL, "", []discord.Embed{{
				Title:       title,
				Description: body,
				Color:       0xFFA500,
			}}); err != nil {
				log.Printf("[shepherd-request] discord #%d: %v", rec.ID, err)
			}
		}
	}
}

// broadcastRequest fires the daemon's SSE hook (panel badge refresh). Safe
// when no hook is registered (CLI, tests).
func broadcastRequest(rec *ShepherdRequestRecord, event string) {
	if requestNotifyHook != nil {
		requestNotifyHook(rec, event)
	}
}
