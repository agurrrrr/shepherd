package mcp

import (
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// registerTools registers all tool handlers
func (s *Server) registerTools() {
	s.tools["task_start"] = handleTaskStart
	s.tools["task_complete"] = handleTaskComplete
	s.tools["task_error"] = handleTaskError
	s.tools["get_history"] = handleGetHistory
	s.tools["get_status"] = handleGetStatus

	// 브라우저 도구 등록
	s.registerBrowserTools()
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

	if sheepName == "" || projectName == "" || prompt == "" {
		return "", fmt.Errorf("sheep_name, project_name, prompt 모두 필요합니다")
	}

	// 양 조회
	sheep, err := worker.Get(sheepName)
	if err != nil {
		return "", err
	}

	// 프로젝트 조회
	proj, err := project.Get(projectName)
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
	sb.WriteString(fmt.Sprintf("  대기: %d, 진행중: %d, 완료: %d, 실패: %d\n",
		counts[task.StatusPending], counts[task.StatusRunning], counts[task.StatusCompleted], counts[task.StatusFailed]))

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
