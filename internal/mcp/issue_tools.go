package mcp

import (
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/internal/issue"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/queue"
)

func (s *Server) registerIssueTools() {
	s.tools["issue_list"] = handleIssueList
	s.tools["issue_get"] = handleIssueGet
	s.tools["issue_upsert"] = handleIssueUpsert
	s.tools["issue_execute"] = handleIssueExecute
}

// registerShepherdTools attaches the two #601 tools. task_stop mutates task
// state (DB write + provider kill), so it belongs to dbWriteToolNames and the
// client forwards it to the daemon; request_user_action writes
// shepherd_requests, so it forwards too.
func (s *Server) registerShepherdTools() {
	s.tools["task_stop"] = handleTaskStop
	s.tools["request_user_action"] = handleRequestUserAction
}

func getIssueToolsList() []Tool {
	return []Tool{
		{
			Name: "issue_list",
			Description: "List project issues with optional status/type/query/parent filters. Returns a page of issues with linked task counts. " +
				"Pass project_name to scope one project; omit project_name to list issues across all projects " +
				"(rows are prefixed with [project]; hidden projects are excluded unless include_hidden=true).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name; omit to list across all projects"},
					"status": {
						Type:        "string",
						Enum:        []string{"todo", "in_progress", "testing", "failed", "done"},
						Description: "Filter by status",
					},
					"type": {
						Type:        "string",
						Enum:        []string{"design", "feature", "bug"},
						Description: "Filter by type",
					},
					"query":          {Type: "string", Description: "Title substring filter"},
					"parent_id":      {Type: "number", Description: "Filter by parent: 0=roots only, N=children of N"},
					"limit":          {Type: "number", Description: "Page size (default 20, max 100)"},
					"page":           {Type: "number", Description: "1-based page number (default 1)"},
					"include_hidden": {Type: "boolean", Description: "Cross-project mode only: include issues of hidden projects"},
				},
			},
		},
		{
			Name:        "issue_get",
			Description: "Get one issue with body, goal, linked tasks, parent, and children (with live status checklist).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"id":           {Type: "number", Description: "Issue ID"},
				},
				Required: []string{"project_name", "id"},
			},
		},
		{
			Name: "issue_upsert",
			Description: "id 없으면 새 이슈 생성, id 있으면 해당 이슈 부분 수정. status는 수정 시에만 적용됨. " +
				"parent_id로 상위 이슈 연결(0이면 부모 해제). shepherd_review로 목자 검토 기록을 남긴다.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"id":           {Type: "number", Description: "Issue ID for update; omit to create"},
					"title":        {Type: "string", Description: "Issue title (required on create)"},
					"type": {
						Type:        "string",
						Enum:        []string{"design", "feature", "bug"},
						Description: "Issue type (default feature on create)",
					},
					"body":      {Type: "string", Description: "Issue body / description"},
					"goal":      {Type: "string", Description: "Success criteria"},
					"parent_id": {Type: "number", Description: "Parent issue ID (0 to clear on update)"},
					"status": {
						Type:        "string",
						Enum:        []string{"todo", "in_progress", "testing", "failed", "done"},
						Description: "Status (update only; ignored on create)",
					},
					"shepherd_review": {Type: "string", Description: "Update only: record the shepherd's latest review verdict on the issue (shepherd_last_review)"},
				},
				Required: []string{"project_name"},
			},
		},
		{
			Name: "issue_execute",
			Description: "이슈를 작업으로 큐에 적재한다. 즉시 실행이 아니라 큐 적재다(task_start와 동일 계약). REST의 ProcessPendingNow와 다름. " +
				"호출 시 이슈 status를 in_progress로 바꾼다. " +
				"하위 이슈가 있고 미완료(status!=done)이면 미완료 하위부터 순차(FIFO) 적재한 뒤 상위 이슈를 마지막에 적재한다. " +
				"일괄 적재 시 작업 간 2초 딜레이를 둬 같은 양이 동시 디스패치되지 않게 한다. " +
				"작업이 중단됐는데 이슈만 in_progress로 남은 경우 실행 전에 failed/testing으로 자동 보정한다. " +
				"이미 pending/running task가 연결된 이슈는 건너뛴다(중복 적재 방지). " +
				"watch=true면 적재된 모든 이슈(하위 포함)에 shepherd_watch를 켠다 — 작업이 끝날 때마다 목자가 검토한다. " +
				"목자(매니저 양) 작업에서 호출하면 watch 생략해도 자동으로 켜진다.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"id":           {Type: "number", Description: "Issue ID"},
					"sheep_name":   {Type: "string", Description: "Sheep name (default: project-assigned sheep)"},
					"model":        {Type: "string", Description: "Optional per-task model override"},
					"watch":        {Type: "boolean", Description: "Set shepherd_watch on every enqueued issue (cascaded children included). Auto-on when the caller is a shepherd (manager-sheep) task."},
				},
				Required: []string{"project_name", "id"},
			},
		},
	}
}

func handleIssueList(args map[string]interface{}) (string, error) {
	projectName := toString(args["project_name"])
	f := issue.ListFilter{
		Project:       projectName,
		AllProjects:   projectName == "", // no project_name → every visible project (#596)
		Status:        toString(args["status"]),
		Type:          toString(args["type"]),
		Query:         toString(args["query"]),
		Page:          toInt(args["page"]),
		Limit:         toInt(args["limit"]),
		IncludeHidden: toBool(args["include_hidden"]),
		OmitBody:      true, // the list output never prints body; keeps the payload small
	}
	if v, ok := args["parent_id"]; ok && v != nil {
		pid := toInt(v)
		f.ParentID = &pid
	}
	res, err := issue.List(f)
	if err != nil {
		return "", err
	}

	if projectName == "" {
		return formatCrossProjectIssueList(res), nil
	}

	if len(res.Items) == 0 {
		return fmt.Sprintf("프로젝트 %q에 이슈가 없습니다", projectName), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("이슈 목록 %q (%d/%d, page %d/%d):\n\n",
		projectName, len(res.Items), res.Total, res.Page, res.TotalPages))
	for _, iss := range res.Items {
		parentNote := ""
		if iss.ParentID != nil {
			parentNote = fmt.Sprintf(" parent=#%d", *iss.ParentID)
		}
		sb.WriteString(fmt.Sprintf("#%d [%s/%s] %s (task %d개%s)\n",
			iss.ID, string(iss.Type), string(iss.Status), iss.Title, res.TaskCounts[iss.ID], parentNote))
	}
	return sb.String(), nil
}

// formatCrossProjectIssueList renders the no-project_name form of issue_list.
// Every row is labelled with its project so an agent can tell which repo to act
// on before calling issue_get / issue_upsert (those still need project_name).
func formatCrossProjectIssueList(res *issue.ListResult) string {
	if len(res.Items) == 0 {
		return "전체 프로젝트에 이슈가 없습니다"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("전체 프로젝트 이슈 목록 (%d/%d, page %d/%d):\n\n",
		len(res.Items), res.Total, res.Page, res.TotalPages))
	for _, iss := range res.Items {
		project := "?"
		if iss.Edges.Project != nil {
			project = iss.Edges.Project.Name
		}
		parentNote := ""
		if iss.ParentID != nil {
			parentNote = fmt.Sprintf(" parent=#%d", *iss.ParentID)
		}
		sb.WriteString(fmt.Sprintf("[%s] #%d [%s/%s] %s (task %d개%s)\n",
			project, iss.ID, string(iss.Type), string(iss.Status), iss.Title, res.TaskCounts[iss.ID], parentNote))
	}
	return sb.String()
}

func handleIssueGet(args map[string]interface{}) (string, error) {
	projectName := toString(args["project_name"])
	id := toInt(args["id"])
	if projectName == "" || id <= 0 {
		return "", fmt.Errorf("project_name and id are required")
	}
	iss, err := issue.Get(projectName, id)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== 이슈 #%d ===\n", iss.ID))
	sb.WriteString(fmt.Sprintf("제목: %s\n타입: %s\n상태: %s\n", iss.Title, string(iss.Type), string(iss.Status)))
	if iss.ParentID != nil {
		parentTitle := ""
		if iss.Edges.Parent != nil {
			parentTitle = " — " + iss.Edges.Parent.Title
		}
		sb.WriteString(fmt.Sprintf("상위: #%d%s\n", *iss.ParentID, parentTitle))
	}
	if iss.Body != "" {
		sb.WriteString(fmt.Sprintf("\n## 내용\n%s\n", iss.Body))
	}
	if checklist := issue.ChildrenChecklist(iss); checklist != "" {
		sb.WriteString("\n" + checklist + "\n")
	}
	if iss.Goal != "" {
		sb.WriteString(fmt.Sprintf("\n## 목표\n%s\n", iss.Goal))
	}
	if len(iss.Edges.Tasks) > 0 {
		sb.WriteString("\n## 연결된 작업\n")
		for _, t := range iss.Edges.Tasks {
			sb.WriteString(fmt.Sprintf("- task #%d [%s]\n", t.ID, string(t.Status)))
		}
	}
	return sb.String(), nil
}

func handleIssueUpsert(args map[string]interface{}) (string, error) {
	// Autonomy gate (#601): a shepherd caller needs at least `plan`. Ordinary
	// sheep, users, and unidentified providers are untouched.
	if err := checkShepherdPermission("issue_upsert", args); err != nil {
		return "", err
	}

	projectName := toString(args["project_name"])
	if projectName == "" {
		return "", fmt.Errorf("project_name is required")
	}
	id := toInt(args["id"])

	// id 없음 → 생성
	if id <= 0 {
		title := toString(args["title"])
		if title == "" {
			return "", fmt.Errorf("title is required when creating an issue (no id)")
		}
		in := issue.CreateInput{
			Project: projectName,
			Title:   title,
			Type:    toString(args["type"]),
			Body:    toString(args["body"]),
			Goal:    toString(args["goal"]),
		}
		if v, ok := args["parent_id"]; ok && v != nil {
			pid := toInt(v)
			if pid > 0 {
				in.ParentID = &pid
			}
		}
		iss, err := issue.Create(in)
		if err != nil {
			return "", err
		}
		parentNote := ""
		if iss.ParentID != nil {
			parentNote = fmt.Sprintf(" parent=#%d", *iss.ParentID)
		}
		return fmt.Sprintf("이슈 #%d 생성됨: %s [%s]%s", iss.ID, iss.Title, string(iss.Type), parentNote), nil
	}

	// id 있음 → 부분 수정 (제공된 필드만 포인터로)
	in := issue.UpdateInput{}
	if v, ok := args["title"]; ok {
		s := toString(v)
		in.Title = &s
	}
	if v, ok := args["type"]; ok {
		s := toString(v)
		in.Type = &s
	}
	if v, ok := args["body"]; ok {
		s := toString(v)
		in.Body = &s
	}
	if v, ok := args["goal"]; ok {
		s := toString(v)
		in.Goal = &s
	}
	if v, ok := args["status"]; ok {
		s := toString(v)
		in.Status = &s
	}
	if v, ok := args["parent_id"]; ok && v != nil {
		pid := toInt(v)
		in.ParentID = &pid
	}
	// Shepherd review note (#597): the shepherd records its last review verdict
	// on the issue right after a review task, without touching other fields.
	if v, ok := args["shepherd_review"]; ok {
		s := toString(v)
		in.ShepherdLastReview = &s
	}
	iss, err := issue.Update(projectName, id, in)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("이슈 #%d 수정됨: %s [%s/%s]", iss.ID, iss.Title, string(iss.Type), string(iss.Status)), nil
}

func handleIssueExecute(args map[string]interface{}) (string, error) {
	// Autonomy gates (#601): a shepherd caller needs `execute` AND must be
	// inside the delegation-count / daily-cost caps. Ordinary sheep, users,
	// and unidentified providers are untouched.
	if err := checkShepherdPermission("issue_execute", args); err != nil {
		return "", err
	}
	if err := checkShepherdExecuteCaps(args); err != nil {
		return "", err
	}

	projectName := toString(args["project_name"])
	id := toInt(args["id"])
	if projectName == "" || id <= 0 {
		return "", fmt.Errorf("project_name and id are required")
	}

	// Watch marking (#597): explicit watch=true, or a caller that is itself a
	// task running on the manager (목자) sheep — the shepherd delegates through
	// issue_execute, so everything it enqueues should be reviewed when it ends.
	watch := toBool(args["watch"]) || callerIsShepherdTask(callerTaskIDOrEnv(args))

	res, err := issue.Execute(issue.ExecuteInput{
		Project:   projectName,
		IssueID:   id,
		SheepName: toString(args["sheep_name"]),
		Model:     toString(args["model"]),
		Watch:     watch,
	})
	if err != nil {
		return "", err
	}
	watchNote := ""
	if watch {
		watchNote = " shepherd_watch=true (작업 종료 시 목자가 검토합니다)."
	}
	if len(res.TaskIDs) > 1 {
		return fmt.Sprintf(
			"이슈 #%d 수행: 미완료 하위 포함 %d개 작업을 큐에 적재했습니다 (이슈 %v → task %v; 양: %s). 마지막이 상위 이슈. processor가 폴링으로 픽업합니다.%s",
			res.IssueID, len(res.TaskIDs), res.IssueIDs, res.TaskIDs, res.SheepName, watchNote), nil
	}
	// 계약: 큐 적재 + status→in_progress 부수효과 + 중복 적재 가능 (#7797)
	return fmt.Sprintf(
		"이슈 #%d를 작업 #%d로 큐에 적재했습니다 (양: %s). 이슈 상태 → in_progress. processor가 폴링으로 픽업합니다. 이미 진행 중인 작업이 있으면 중복 적재될 수 있습니다.%s",
		res.IssueID, res.TaskID, res.SheepName, watchNote), nil
}

// callerIsShepherdTask reports whether taskID belongs to a task running on the
// manager (목자) sheep (#597). The check stays local to the tool layer — it
// only needs queue.GetTask + names.IsManager, so the MCP package does not pull
// in internal/manager's wake logic. manager.IsShepherdCaller is the same test
// for daemon-side callers.
func callerIsShepherdTask(taskID int) bool {
	if taskID <= 0 {
		return false
	}
	t, err := queue.GetTask(taskID)
	if err != nil || t == nil || t.Edges.Sheep == nil {
		return false
	}
	return names.IsManager(t.Edges.Sheep.Name)
}
