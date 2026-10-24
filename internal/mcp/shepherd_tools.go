// The two new MCP tools of stage #601.
//
// task_stop(task_id, reason) — allowed ONLY for shepherd callers at the
// `full` tier. It shares the daemon's stop implementation with
// POST /api/tasks/:id/stop via queue.StopTaskByID, so web, CLI, and MCP stop
// a task identically (issue #601 §2). Since #626 it also handles pending
// rows: a pending target is cancelled through queue.CancelPendingTask, the
// exact path POST /api/tasks/:id/cancel uses. The autonomy gate is unchanged
// — the same `task_stop` tier entry guards both branches, so this never
// becomes a way around the tier limit.
//
// request_user_action(kind, project_name, target_id, reason) — any caller may
// raise one (sheep AND shepherd); the panel shows who asked. This is how a
// shepherd reaches beyond its tier: refuse-by-permission errors all point
// here. Approving on the web runs the action server-side and wakes a review.
package mcp

import (
	"fmt"

	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/queue"
)

func getShepherdToolsList() []Tool {
	return []Tool{
		{
			Name: "task_stop",
			Description: "작업 하나를 중지·취소합니다. 대상이 running이면 프로세스를 종료하고, pending이면 큐에서 취소합니다(#626). " +
				"목자(매니저 양) 호출자는 autonomy 단계가 full일 때만 쓸 수 있습니다. " +
				"일반 양·사용자 호출은 지금과 같이 제한이 없습니다. 웹의 POST /api/tasks/:id/stop(running)·POST /api/tasks/:id/cancel(pending)과 완전히 같은 서버 경로로 동작합니다.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"task_id": {Type: "number", Description: "중지·취소할 작업 ID (pending 또는 running)"},
					"reason":  {Type: "string", Description: "중지 사유 (기록에 남는다)"},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name: "request_user_action",
			Description: "사용자 확인이 필요한 조치를 요청으로 등록합니다. kind는 stop_task / execute_issue / delete_issue / other 중 하나. " +
				"권한 단계 밖의 조치를 실행할 수 없을 때 이 도구로 승인을 요청하고, 결정되면 목자 review 작업으로 다시 깨어납니다. " +
				"이슈 삭제는 어떤 권한 단계에서도 이 승인 경로를 통해서만 가능합니다. 목자가 아닌 호출자도 쓸 수 있습니다.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"kind": {
						Type:        "string",
						Enum:        []string{"stop_task", "execute_issue", "delete_issue", "other"},
						Description: "승인 시 서버가 실행할 조치의 종류",
					},
					"project_name": {Type: "string", Description: "대상 프로젝트 (execute_issue/delete_issue에 필수)"},
					"target_id":    {Type: "number", Description: "대상 ID (stop_task=작업 id, execute_issue/delete_issue=이슈 id)"},
					"reason":       {Type: "string", Description: "요청 사유 (사용자가 화면에서 본다)"},
				},
				Required: []string{"kind", "reason"},
			},
		},
	}
}

func handleTaskStop(args map[string]interface{}) (string, error) {
	// Tier gate: only `full` shepherds are refused below; other callers pass.
	// #626 added the pending-cancel branch below WITHOUT touching this gate —
	// the same `task_stop` tier entry guards both branches.
	if err := checkShepherdPermission("task_stop", args); err != nil {
		return "", err
	}

	id := toInt(args["task_id"])
	if id <= 0 {
		return "", fmt.Errorf("task_id is required")
	}
	reason := toString(args["reason"])
	if reason == "" {
		reason = "stopped by shepherd request"
	}

	// A pending row has no provider process to kill, so it goes through the
	// daemon's cancel path — the exact one POST /api/tasks/:id/cancel uses
	// (queue.CancelPendingTask, which records error "cancelled by user").
	t, err := queue.GetTask(id)
	if err != nil {
		return "", err
	}
	if t.Status == entTask.StatusPending {
		if err := queue.CancelPendingTask(id); err != nil {
			return "", err
		}
		return fmt.Sprintf("작업 #%d를 큐에서 취소했습니다 (사유: %s). 감시 이슈였다면 review 작업으로 검토가 시작됩니다.", id, reason), nil
	}

	if err := queue.StopTaskByID(id, reason); err != nil {
		return "", err
	}
	return fmt.Sprintf("작업 #%d를 중지했습니다 (사유: %s). 감시 이슈였다면 review 작업으로 검토가 시작됩니다.", id, reason), nil
}

func handleRequestUserAction(args map[string]interface{}) (string, error) {
	kind := toString(args["kind"])
	reason := toString(args["reason"])
	projectName := toString(args["project_name"])
	targetID := toInt(args["target_id"])

	rec, err := manager.CreateShepherdRequest(kind, projectName, targetID, reason, callerTaskIDOrEnv(args))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"요청 #%d 등록됨 (%s). 사용자가 Shepherd 화면에서 승인하면 서버가 조치를 실행하고, 거절·승인 모두 목자 review 작업으로 알려 줍니다. 그 전까지는 이 조치를 직접 시도하지 마세요.",
		rec.ID, rec.Kind,
	), nil
}
