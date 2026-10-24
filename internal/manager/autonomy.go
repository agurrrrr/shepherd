// Shepherd autonomy tiers and limits (#601).
//
// The shepherd (목자) acts on its own only up to the configured tier:
//
//	report  → read and report only
//	plan    → + create/edit issues (issue_upsert)
//	execute → + enqueue issues (issue_execute, caps checked)  [default]
//	full    → + stop tasks (task_stop)
//
// Deleting an issue is never allowed directly at any tier — it only happens
// through a user-approved shepherd_request. A caller the daemon cannot
// identify (caller task id 0: a provider that does not pass SHEPHERD_TASK_ID
// down to the MCP child, or a plain CLI/web call) is not checked at all;
// those paths behave exactly as before #601.
//
// Two numeric limits gate issue_execute for shepherd callers:
//   - shepherd_max_active_delegations: how many watched-issue tasks may sit
//     pending or running at once (the "wait, you get woken when they finish"
//     cap);
//   - shepherd_daily_cost_limit_usd: the combined cost of today's shepherd
//     tasks + watched-issue tasks. Over the limit, waking stops too (wake.go
//     rule 7, patrol's SkipCostCap) and attention grows a cost_limit item.
package manager

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/discord"
	"github.com/agurrrrr/shepherd/internal/push"
)

// Autonomy tiers, ordered. The stored setting is one of these strings;
// anything unrecognizable falls back to execute (the 2026-10-23 decision:
// create+execute automatic, stop/delete need the user).
const (
	AutonomyReport  = "report"
	AutonomyPlan    = "plan"
	AutonomyExecute = "execute"
	AutonomyFull    = "full"
)

// maxActiveDelegationsDefault / dailyCostLimitDefault are fallbacks when the
// shepherd_max_active_delegations / shepherd_daily_cost_limit_usd keys are
// unset or non-positive (0 on the cost key means "no limit", matching the
// issue spec — so only negatives fall back).
const (
	maxActiveDelegationsDefault = 3
	dailyCostLimitDefault       = 0.0
)

// tierRank orders the tiers (higher = more allowed). Unknown tiers rank as
// execute through AutonomyTier's normalization, never here.
func tierRank(tier string) int {
	switch tier {
	case AutonomyReport:
		return 0
	case AutonomyPlan:
		return 1
	case AutonomyExecute:
		return 2
	case AutonomyFull:
		return 3
	default:
		return 2 // defensive: unknown behaves like execute
	}
}

// toolMinTier is the permission table (#601 §2). Tools absent from the map
// are unrestricted for shepherd callers (reads, task_start-style queueing of
// ordinary work stays as-is). issue DELETE has no MCP tool at all — it only
// runs through an approved request — so it never appears here.
var toolMinTier = map[string]string{
	"issue_upsert":  AutonomyPlan,
	"issue_execute": AutonomyExecute,
	"task_stop":     AutonomyFull,
}

// AutonomyTier resolves the shepherd_autonomy setting: unset/empty/unknown →
// execute. Reading it per call (not caching) keeps settings changes effective
// without a restart, matching PatrolInterval's rule.
func AutonomyTier() string {
	raw := strings.ToLower(strings.TrimSpace(config.GetString("shepherd_autonomy")))
	switch raw {
	case AutonomyReport, AutonomyPlan, AutonomyExecute, AutonomyFull:
		return raw
	default:
		return AutonomyExecute
	}
}

// maxActiveDelegations returns the concurrent pending+running cap for watched
// work (shepherd_max_active_delegations; <=0 → default 3).
func maxActiveDelegations() int {
	n := config.GetInt("shepherd_max_active_delegations")
	if n <= 0 {
		return maxActiveDelegationsDefault
	}
	return n
}

// MaxActiveDelegations is the exported form for the settings/status payloads:
// the effective cap after the <=0 → default rule, so the UI shows what wake.go,
// CheckShepherdExecuteLimits and patrol actually enforce instead of the raw key.
func MaxActiveDelegations() int { return maxActiveDelegations() }

// DailyCostLimitUSD is the exported form of the per-day cap (0 = no limit).
func DailyCostLimitUSD() float64 { return dailyCostLimitUSD() }

// dailyCostLimitUSD returns the per-day spend cap in USD (0 = no limit).
// Negative stored values fall back to the default (0), never to a negative.
func dailyCostLimitUSD() float64 {
	v := config.GetFloat("shepherd_daily_cost_limit_usd")
	if v < 0 {
		return dailyCostLimitDefault
	}
	return v
}

// RequestUserActionHint is appended to every refusal so the shepherd knows
// the next move instead of flailing (#601 §2: "거부할 때는 오류 문구에
// request_user_action으로 사용자 확인을 요청하라는 안내").
const RequestUserActionHint = "If you need this beyond your current permission tier, call request_user_action(kind, project_name, target_id, reason) and wait for the user's decision — approving wakes you again."

// CheckShepherdToolPermission enforces the permission table for MCP tool
// calls (#601 §2). It refuses ONLY when the caller is identifiable as a
// shepherd (manager-sheep) task; caller id 0 (unidentified provider, CLI,
// web) and non-shepherd sheep pass through untouched.
func CheckShepherdToolPermission(tool string, callerTaskID int) error {
	if callerTaskID <= 0 {
		return nil // provider without caller identification: no check (#597 wiki table)
	}
	if !IsShepherdCaller(callerTaskID) {
		return nil // ordinary sheep and users keep today's behaviour
	}
	minTier, ok := toolMinTier[tool]
	if !ok {
		return nil // unrestricted tool
	}
	tier := AutonomyTier()
	if tierRank(tier) >= tierRank(minTier) {
		return nil
	}
	return fmt.Errorf("autonomy tier %q does not allow %s (needs %q). %s", tier, tool, minTier, RequestUserActionHint)
}

// CheckShepherdExecuteLimits applies the two numeric caps to a shepherd's
// issue_execute (#601 §2): concurrent delegations and today's cost. Only
// shepherd callers are checked; the denial texts tell the shepherd to wait
// (waking is automatic) or to request instead of pushing.
func CheckShepherdExecuteLimits(callerTaskID int) error {
	if callerTaskID <= 0 || !IsShepherdCaller(callerTaskID) {
		return nil
	}

	maxActive := maxActiveDelegations()
	active, err := ActiveDelegations()
	if err != nil {
		// Fail open on the query (same spirit as wake.go's bundle check): a
		// DB hiccup must not block delegation. The cost query below still runs.
		log.Printf("[shepherd-autonomy] active-delegations count: %v", err)
	} else if active >= maxActive {
		return fmt.Errorf("delegation cap reached: %d/%d watched-issue tasks are already pending or running (shepherd_max_active_delegations=%d). You are woken automatically when they finish — wait for the wake instead of enqueueing more.", active, maxActive, maxActive)
	}

	limit := dailyCostLimitUSD()
	if limit > 0 {
		spent, err := DailyCostToday(time.Now())
		if err != nil {
			log.Printf("[shepherd-autonomy] daily cost query: %v", err)
		} else if spent >= limit {
			return fmt.Errorf("daily cost limit reached: $%.2f/$%.2f used today (shepherd tasks + watched-issue tasks combined). Enqueueing more work is refused. %s", spent, limit, RequestUserActionHint)
		}
	}
	return nil
}

// ActiveDelegations counts tasks that sit pending or running on shepherd-
// watched issues — the number both the MCP cap and the status card show.
// Shepherd tasks themselves are never linked to an issue, so they cannot be
// double-counted here.
func ActiveDelegations() (int, error) {
	ctx := context.Background()
	if db.Client() == nil {
		return 0, fmt.Errorf("database is not initialized")
	}
	return db.Client().Task.Query().
		Where(
			entTask.HasIssueWith(entIssue.ShepherdWatch(true)),
			entTask.StatusIn(entTask.StatusPending, entTask.StatusRunning),
		).
		Count(ctx)
}

// DailyCostToday sums cost_usd of tasks created since local midnight that are
// either shepherd tasks (shepherd_kind != "") or run on shepherd-watched
// issues — the combined window the daily limit bills against (#601 §1).
// The midnight window uses ent's CreatedAtGTE, the same comparison
// wakeupsToday (#597) relies on.
func DailyCostToday(now time.Time) (float64, error) {
	ctx := context.Background()
	if db.Client() == nil {
		return 0, fmt.Errorf("database is not initialized")
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var rows []struct {
		Cost float64 `json:"cost"`
	}
	err := db.Client().Task.Query().
		Where(
			entTask.CreatedAtGTE(midnight),
			entTask.Or(
				entTask.ShepherdKindNEQ(""),
				entTask.HasIssueWith(entIssue.ShepherdWatch(true)),
			),
		).
		Aggregate(ent.As(ent.Sum(entTask.FieldCostUsd), "cost")).
		Scan(ctx, &rows)
	if err != nil {
		return 0, fmt.Errorf("daily cost sum: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Cost, nil
}

// CostLimitStatus reports whether today's combined spend has reached the cap.
// A limit of 0 disables the gate entirely (no query).
func CostLimitStatus(now time.Time) (exceeded bool, spent float64, limit float64, err error) {
	limit = dailyCostLimitUSD()
	if limit <= 0 {
		return false, 0, limit, nil
	}
	spent, err = DailyCostToday(now)
	if err != nil {
		return false, 0, limit, err
	}
	return spent >= limit, spent, limit, nil
}

// ---- one-per-day cost-limit notification ---------------------------------

var (
	costNotifyMu  sync.Mutex
	costNotifyDay string                     // local date key ("2006-01-02") already notified
	costLimitSend = notifyCostLimitDefault   // seam for tests
	costLimitHook func(spent, limit float64) // seam for SSE broadcast (server wiring)
)

// MaybeNotifyCostLimit sends the web-push + Discord notice the first time the
// cost cap is hit on a given local day (#601 §3: "알림을 한 번 보냅니다").
// The once-guard is in-memory: a daemon restart may re-notify once, which is
// acceptable for a daily ceiling; the attention item itself (fingerprint
// cost_limit:<date>) stays visible regardless, and it disappears by itself
// after midnight when the fingerprint's date changes.
func MaybeNotifyCostLimit(now time.Time, spent, limit float64) bool {
	day := now.Format("2006-01-02")
	costNotifyMu.Lock()
	if costNotifyDay == day {
		costNotifyMu.Unlock()
		return false
	}
	costNotifyDay = day
	costNotifyMu.Unlock()

	if err := costLimitSend(spent, limit); err != nil {
		log.Printf("[shepherd-autonomy] cost-limit notify: %v", err)
	}
	if costLimitHook != nil {
		costLimitHook(spent, limit)
	}
	return true
}

// RegisterCostLimitHook lets the daemon broadcast an SSE event whenever the
// cost cap newly trips (the panel badge refreshes). Pass nil to clear.
func RegisterCostLimitHook(fn func(spent, limit float64)) {
	costNotifyMu.Lock()
	defer costNotifyMu.Unlock()
	costLimitHook = fn
}

// ResetCostLimitNotifyDay is a test seam: forgets the "notified today" mark.
func ResetCostLimitNotifyDay() {
	costNotifyMu.Lock()
	defer costNotifyMu.Unlock()
	costNotifyDay = ""
}

// notifyCostLimitDefault delivers the cost-limit alert through the existing
// web-push and Discord channels (#601 §4: "기존 웹 푸시와 Discord 알림 함수로
// 알립니다"). Both are config-gated internally, so a daemon without a webhook
// or subscriptions simply no-ops.
func notifyCostLimitDefault(spent, limit float64) error {
	title := "Shepherd · 하루 비용 상한 도달"
	body := fmt.Sprintf("오늘 목자+감시 작업 비용 $%.2f이 상한 $%.2f에 닿았습니다. 자동 깨우기와 위임이 멈춥니다.", spent, limit)

	if config.GetBool("webpush_enabled") {
		if _, err := push.SendToAll(push.Payload{
			Title:  title,
			Body:   body,
			URL:    "/shepherd",
			Tag:    "shepherd-cost-limit",
			Status: "warning",
		}, true); err != nil {
			return fmt.Errorf("web push: %w", err)
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
				return fmt.Errorf("discord: %w", err)
			}
		}
	}
	return nil
}
