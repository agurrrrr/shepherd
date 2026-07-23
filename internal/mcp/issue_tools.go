package mcp

import (
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/internal/issue"
)

func (s *Server) registerIssueTools() {
	s.tools["issue_list"] = handleIssueList
	s.tools["issue_get"] = handleIssueGet
	s.tools["issue_upsert"] = handleIssueUpsert
	s.tools["issue_execute"] = handleIssueExecute
}

func getIssueToolsList() []Tool {
	return []Tool{
		{
			Name:        "issue_list",
			Description: "List project issues with optional status/type/query filters. Returns a page of issues with linked task counts.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
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
					"query": {Type: "string", Description: "Title substring filter"},
					"limit": {Type: "number", Description: "Page size (default 20, max 100)"},
					"page":  {Type: "number", Description: "1-based page number (default 1)"},
				},
				Required: []string{"project_name"},
			},
		},
		{
			Name:        "issue_get",
			Description: "Get one issue with body, goal, and linked tasks.",
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
			Description: "id 없으면 새 이슈 생성, id 있으면 해당 이슈 부분 수정. status는 수정 시에만 적용됨.",
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
					"body": {Type: "string", Description: "Issue body / description"},
					"goal": {Type: "string", Description: "Success criteria"},
					"status": {
						Type:        "string",
						Enum:        []string{"todo", "in_progress", "testing", "failed", "done"},
						Description: "Status (update only; ignored on create)",
					},
				},
				Required: []string{"project_name"},
			},
		},
		{
			Name: "issue_execute",
			Description: "이슈를 작업으로 큐에 적재한다. 즉시 실행이 아니라 큐 적재다(task_start와 동일 계약). REST의 ProcessPendingNow와 다름. " +
				"호출 시 이슈 status를 in_progress로 바꾼다. " +
				"이미 pending/running task가 연결된 이슈에 재호출하면 task가 추가로 큐에 쌓여 중복 적재될 수 있으니 주의.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"id":           {Type: "number", Description: "Issue ID"},
					"sheep_name":   {Type: "string", Description: "Sheep name (default: project-assigned sheep)"},
					"model":        {Type: "string", Description: "Optional per-task model override"},
				},
				Required: []string{"project_name", "id"},
			},
		},
	}
}

func handleIssueList(args map[string]interface{}) (string, error) {
	projectName := toString(args["project_name"])
	if projectName == "" {
		return "", fmt.Errorf("project_name is required")
	}
	res, err := issue.List(issue.ListFilter{
		Project: projectName,
		Status:  toString(args["status"]),
		Type:    toString(args["type"]),
		Query:   toString(args["query"]),
		Page:    toInt(args["page"]),
		Limit:   toInt(args["limit"]),
	})
	if err != nil {
		return "", err
	}
	if len(res.Items) == 0 {
		return fmt.Sprintf("프로젝트 %q에 이슈가 없습니다", projectName), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("이슈 목록 %q (%d/%d, page %d/%d):\n\n",
		projectName, len(res.Items), res.Total, res.Page, res.TotalPages))
	for _, iss := range res.Items {
		sb.WriteString(fmt.Sprintf("#%d [%s/%s] %s (task %d개)\n",
			iss.ID, string(iss.Type), string(iss.Status), iss.Title, res.TaskCounts[iss.ID]))
	}
	return sb.String(), nil
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
	if iss.Body != "" {
		sb.WriteString(fmt.Sprintf("\n## 내용\n%s\n", iss.Body))
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
		iss, err := issue.Create(issue.CreateInput{
			Project: projectName,
			Title:   title,
			Type:    toString(args["type"]),
			Body:    toString(args["body"]),
			Goal:    toString(args["goal"]),
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("이슈 #%d 생성됨: %s [%s]", iss.ID, iss.Title, string(iss.Type)), nil
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
	iss, err := issue.Update(projectName, id, in)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("이슈 #%d 수정됨: %s [%s/%s]", iss.ID, iss.Title, string(iss.Type), string(iss.Status)), nil
}

func handleIssueExecute(args map[string]interface{}) (string, error) {
	projectName := toString(args["project_name"])
	id := toInt(args["id"])
	if projectName == "" || id <= 0 {
		return "", fmt.Errorf("project_name and id are required")
	}
	res, err := issue.Execute(issue.ExecuteInput{
		Project:   projectName,
		IssueID:   id,
		SheepName: toString(args["sheep_name"]),
		Model:     toString(args["model"]),
	})
	if err != nil {
		return "", err
	}
	// 계약: 큐 적재 + status→in_progress 부수효과 + 중복 적재 가능 (#7797)
	return fmt.Sprintf(
		"이슈 #%d를 작업 #%d로 큐에 적재했습니다 (양: %s). 이슈 상태 → in_progress. processor가 폴링으로 픽업합니다. 이미 진행 중인 작업이 있으면 중복 적재될 수 있습니다.",
		res.IssueID, res.TaskID, res.SheepName), nil
}
