package mcp

import (
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/skill"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// registerTools registers all tool handlers
// registerCoreTools registers task / status / skill tools that talk to the
// shepherd database directly. Same in both daemon and stateless client modes.
func (s *Server) registerCoreTools() {
	s.tools["task_start"] = handleTaskStart
	s.tools["task_complete"] = handleTaskComplete
	s.tools["task_error"] = handleTaskError
	s.tools["get_history"] = handleGetHistory
	s.tools["get_task_detail"] = handleGetTaskDetail
	s.tools["get_status"] = handleGetStatus
	s.tools["skill_load"] = handleSkillLoad
}

// registerTools registers every tool in-process — daemon use only, since
// browser handlers manage live chrome processes that must outlive the call.
func (s *Server) registerTools() {
	s.registerCoreTools()
	if !s.minimal {
		s.registerBrowserTools()
	}
}

// InitForTools initializes config and DB for tool handlers
func InitForTools() error {
	if err := config.Init(); err != nil {
		return fmt.Errorf("설정 초기화 실패: %w", err)
	}
	if err := db.Init(); err != nil {
		return fmt.Errorf("데이터베이스 초기화 실패: %w", err)
	}
	return nil
}

func handleTaskStart(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	projectName, _ := args["project_name"].(string)
	prompt, _ := args["prompt"].(string)

	if projectName == "" || prompt == "" {
		return "", fmt.Errorf("project_name, prompt 모두 필요합니다")
	}

	// 프로젝트 조회
	proj, err := project.Get(projectName)
	if err != nil {
		return "", err
	}

	// 양 조회: sheep_name이 없으면 프로젝트에 할당된 양을 자동 사용
	if sheepName == "" {
		if proj.Edges.Sheep == nil {
			return "", fmt.Errorf("프로젝트 '%s'에 할당된 양이 없습니다. sheep_name을 지정해주세요", projectName)
		}
		sheepName = proj.Edges.Sheep.Name
	}

	sheep, err := worker.Get(sheepName)
	if err != nil {
		return "", err
	}

	// 작업 생성 (pending 상태로 큐에 추가)
	// StartTask를 호출하지 않음 - Processor가 pending 작업을 찾아서 실행함
	task, err := queue.CreateTask(prompt, sheep.ID, proj.ID)
	if err != nil {
		return "", err
	}

	// 대기중인 작업 수 확인
	pendingCount, _ := queue.CountPendingTasksBySheep(sheep.ID)

	if pendingCount > 1 {
		return fmt.Sprintf("작업 #%d 대기열에 추가됨 (양: %s, 프로젝트: %s, 대기: %d개)", task.ID, sheepName, projectName, pendingCount), nil
	}
	return fmt.Sprintf("작업 #%d 큐에 추가됨 (양: %s, 프로젝트: %s)", task.ID, sheepName, projectName), nil
}

func handleTaskComplete(args map[string]interface{}) (string, error) {
	taskIDFloat, ok := args["task_id"].(float64)
	if !ok {
		return "", fmt.Errorf("task_id가 필요합니다")
	}
	taskID := int(taskIDFloat)

	summary, _ := args["summary"].(string)
	filesStr, _ := args["files_modified"].(string)

	var filesModified []string
	if filesStr != "" {
		for _, f := range strings.Split(filesStr, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				filesModified = append(filesModified, f)
			}
		}
	}

	if err := queue.CompleteTask(taskID, summary, filesModified); err != nil {
		return "", err
	}

	return fmt.Sprintf("작업 #%d 완료됨", taskID), nil
}

func handleTaskError(args map[string]interface{}) (string, error) {
	taskIDFloat, ok := args["task_id"].(float64)
	if !ok {
		return "", fmt.Errorf("task_id가 필요합니다")
	}
	taskID := int(taskIDFloat)

	errMsg, _ := args["error"].(string)
	if errMsg == "" {
		errMsg = "Unknown error"
	}

	if err := queue.FailTask(taskID, errMsg); err != nil {
		return "", err
	}

	return fmt.Sprintf("작업 #%d 실패 기록됨", taskID), nil
}

func handleGetHistory(args map[string]interface{}) (string, error) {
	projectName, _ := args["project_name"].(string)
	if projectName == "" {
		return "", fmt.Errorf("project_name이 필요합니다")
	}

	if _, err := project.Get(projectName); err != nil {
		return "", fmt.Errorf("project '%s' is not registered — register it first with `shepherd project add %s <absolute-path>` (or `shepherd init` from inside the directory). Empty history alone does not imply registration", projectName, projectName)
	}

	limitFloat, _ := args["limit"].(float64)
	limit := int(limitFloat)
	if limit <= 0 {
		limit = 10
	}

	tasks, err := queue.ListTasksByProject(projectName)
	if err != nil {
		return "", err
	}

	if len(tasks) == 0 {
		return fmt.Sprintf("프로젝트 '%s'에 작업 기록이 없습니다", projectName), nil
	}

	// 제한 적용
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("프로젝트 '%s' 히스토리 (최근 %d건):\n\n", projectName, len(tasks)))

	for _, t := range tasks {
		status := queue.StatusToKorean(t.Status)
		sheepName := "-"
		if t.Edges.Sheep != nil {
			sheepName = t.Edges.Sheep.Name
		}

		sb.WriteString(fmt.Sprintf("#%d [%s] %s - %s\n", t.ID, status, t.CreatedAt.Format("01/02 15:04"), sheepName))
		sb.WriteString(fmt.Sprintf("  요청: %s\n", truncateString(t.Prompt, 50)))
		if t.Summary != "" {
			sb.WriteString(fmt.Sprintf("  결과: %s\n", truncateString(t.Summary, 50)))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func handleGetTaskDetail(args map[string]interface{}) (string, error) {
	taskIDFloat, ok := args["task_id"].(float64)
	if !ok {
		return "", fmt.Errorf("task_id가 필요합니다")
	}
	taskID := int(taskIDFloat)

	t, err := queue.GetTask(taskID)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sheepName := "-"
	if t.Edges.Sheep != nil {
		sheepName = t.Edges.Sheep.Name
	}
	projectName := "-"
	if t.Edges.Project != nil {
		projectName = t.Edges.Project.Name
	}

	sb.WriteString(fmt.Sprintf("=== 작업 #%d ===\n", t.ID))
	sb.WriteString(fmt.Sprintf("상태: %s\n", queue.StatusToKorean(t.Status)))
	sb.WriteString(fmt.Sprintf("양: %s\n", sheepName))
	sb.WriteString(fmt.Sprintf("프로젝트: %s\n", projectName))
	sb.WriteString(fmt.Sprintf("생성: %s\n", t.CreatedAt.Format("2006-01-02 15:04:05")))
	if !t.StartedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("시작: %s\n", t.StartedAt.Format("2006-01-02 15:04:05")))
	}
	if !t.CompletedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("완료: %s\n", t.CompletedAt.Format("2006-01-02 15:04:05")))
	}
	if t.CostUsd > 0 {
		sb.WriteString(fmt.Sprintf("비용: $%.4f\n", t.CostUsd))
	}

	sb.WriteString("\n--- 요청 ---\n")
	sb.WriteString(t.Prompt)
	sb.WriteString("\n")

	if t.Summary != "" {
		sb.WriteString("\n--- 결과 요약 ---\n")
		sb.WriteString(t.Summary)
		sb.WriteString("\n")
	}

	if t.Error != "" {
		sb.WriteString("\n--- 에러 ---\n")
		sb.WriteString(t.Error)
		sb.WriteString("\n")
	}

	if len(t.FilesModified) > 0 {
		sb.WriteString("\n--- 수정된 파일 ---\n")
		for _, f := range t.FilesModified {
			sb.WriteString(fmt.Sprintf("  - %s\n", f))
		}
	}

	if len(t.Output) > 0 {
		sb.WriteString("\n--- 출력 로그 ---\n")
		sb.WriteString(strings.Join(t.Output, "\n"))
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func handleGetStatus(args map[string]interface{}) (string, error) {
	var sb strings.Builder

	// 양 현황
	sheepList, err := worker.List()
	if err != nil {
		return "", err
	}

	sb.WriteString("=== 양 현황 ===\n")
	if len(sheepList) == 0 {
		sb.WriteString("  (없음)\n")
	} else {
		for _, s := range sheepList {
			projectName := "-"
			if s.Edges.Project != nil {
				projectName = s.Edges.Project.Name
			}
			status := worker.StatusToKorean(s.Status)
			sb.WriteString(fmt.Sprintf("  %s: %s (%s)\n", s.Name, status, projectName))
		}
	}

	// 프로젝트 현황
	projects, err := project.List()
	if err != nil {
		return "", err
	}

	sb.WriteString("\n=== 프로젝트 현황 ===\n")
	if len(projects) == 0 {
		sb.WriteString("  (없음)\n")
	} else {
		for _, p := range projects {
			sheepName := "-"
			if p.Edges.Sheep != nil {
				sheepName = p.Edges.Sheep.Name
			}
			sb.WriteString(fmt.Sprintf("  %s: %s (담당: %s)\n", p.Name, p.Path, sheepName))
		}
	}

	// 작업 현황
	counts, err := queue.CountByStatus()
	if err != nil {
		return "", err
	}

	sb.WriteString("\n=== 작업 현황 ===\n")
	sb.WriteString(fmt.Sprintf("  대기: %d, 진행중: %d, 완료: %d, 실패: %d, 중단: %d\n",
		counts[task.StatusPending], counts[task.StatusRunning], counts[task.StatusCompleted], counts[task.StatusFailed], counts[task.StatusStopped]))

	return sb.String(), nil
}

func truncateString(s string, maxLen int) string {
	lines := strings.Split(s, "\n")
	s = lines[0]
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func handleSkillLoad(args map[string]interface{}) (string, error) {
	skillName, _ := args["skill_name"].(string)
	if skillName == "" {
		return "", fmt.Errorf("skill_name is required")
	}

	// Search all skills by name
	skills, err := skill.ListAll()
	if err != nil {
		return "", fmt.Errorf("failed to list skills: %w", err)
	}

	for _, sk := range skills {
		if sk.Name == skillName {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("# %s\n", sk.Name))
			if sk.Description != "" {
				sb.WriteString(fmt.Sprintf("Description: %s\n", sk.Description))
			}
			if sk.Effort != "" {
				sb.WriteString(fmt.Sprintf("Effort: %s\n", sk.Effort))
			}
			if sk.MaxTurns > 0 {
				sb.WriteString(fmt.Sprintf("Max turns: %d\n", sk.MaxTurns))
			}
			if len(sk.DisallowedTools) > 0 {
				sb.WriteString(fmt.Sprintf("Disallowed tools: %s\n", strings.Join(sk.DisallowedTools, ", ")))
			}
			sb.WriteString("\n")
			sb.WriteString(sk.Content)
			return sb.String(), nil
		}
	}

	// List available skills for better error message
	var names []string
	for _, sk := range skills {
		names = append(names, sk.Name)
	}
	return "", fmt.Errorf("skill '%s' not found. Available skills: %s", skillName, strings.Join(names, ", "))
}
