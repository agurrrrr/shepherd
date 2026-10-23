package server

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/issue"
)

// issueToListItem maps an issue (+ optional task count) for list responses.
func issueToListItem(iss *ent.Issue, taskCount int) fiber.Map {
	item := fiber.Map{
		"id":         iss.ID,
		"title":      iss.Title,
		"type":       string(iss.Type),
		"status":     string(iss.Status),
		"body":       iss.Body,
		"goal":       iss.Goal,
		"task_count": taskCount,
		"created_at": issue.FormatTime(iss.CreatedAt),
		"updated_at": issue.FormatTime(iss.UpdatedAt),
	}
	if iss.ParentID != nil {
		item["parent_id"] = *iss.ParentID
	}
	if s := issue.FormatTimePtr(iss.StartedAt); s != "" {
		item["started_at"] = s
	}
	if s := issue.FormatTimePtr(iss.CompletedAt); s != "" {
		item["completed_at"] = s
	}
	// Shepherd (목자) delegation state (#597) — the issue board reads the flag.
	item["shepherd_watch"] = iss.ShepherdWatch
	item["shepherd_rounds"] = iss.ShepherdRounds
	// Board cards show one line of the shepherd's last review (#599); the
	// full text lives in the detail response.
	if iss.ShepherdLastReview != "" {
		item["shepherd_last_review"] = firstLineForBoard(iss.ShepherdLastReview)
	}
	return item
}

// firstLineForBoard trims the shepherd review text to its first line, capped
// so wide list payloads stay small.
func firstLineForBoard(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return string(r)
}

// issueToDetail maps an issue with linked tasks, parent, and children for get/create/update.
func issueToDetail(iss *ent.Issue) fiber.Map {
	result := fiber.Map{
		"id":         iss.ID,
		"title":      iss.Title,
		"type":       string(iss.Type),
		"status":     string(iss.Status),
		"body":       iss.Body,
		"goal":       iss.Goal,
		"created_at": issue.FormatTime(iss.CreatedAt),
		"updated_at": issue.FormatTime(iss.UpdatedAt),
	}
	if iss.ParentID != nil {
		result["parent_id"] = *iss.ParentID
	}
	if s := issue.FormatTimePtr(iss.StartedAt); s != "" {
		result["started_at"] = s
	}
	if s := issue.FormatTimePtr(iss.CompletedAt); s != "" {
		result["completed_at"] = s
	}
	// Shepherd (목자) delegation state (#597).
	result["shepherd_watch"] = iss.ShepherdWatch
	result["shepherd_rounds"] = iss.ShepherdRounds
	if iss.ShepherdLastReview != "" {
		result["shepherd_last_review"] = iss.ShepherdLastReview
	}
	if s := issue.FormatTimePtr(iss.ShepherdWatchedAt); s != "" {
		result["shepherd_watched_at"] = s
	}

	var tasks []fiber.Map
	for _, t := range iss.Edges.Tasks {
		tm := fiber.Map{
			"id":         t.ID,
			"status":     string(t.Status),
			"summary":    t.Summary,
			"created_at": issue.FormatTime(t.CreatedAt),
		}
		if !t.CompletedAt.IsZero() {
			tm["completed_at"] = issue.FormatTime(t.CompletedAt)
		}
		tasks = append(tasks, tm)
	}
	if tasks == nil {
		tasks = []fiber.Map{}
	}
	result["tasks"] = tasks

	// Parent summary (when eager-loaded).
	if iss.Edges.Parent != nil {
		result["parent"] = fiber.Map{
			"id":     iss.Edges.Parent.ID,
			"title":  iss.Edges.Parent.Title,
			"status": string(iss.Edges.Parent.Status),
		}
	}

	// Children checklist data for WebUI (status shown live, not baked into body).
	children := make([]fiber.Map, 0, len(iss.Edges.Children))
	for _, c := range iss.Edges.Children {
		children = append(children, fiber.Map{
			"id":     c.ID,
			"title":  c.Title,
			"type":   string(c.Type),
			"status": string(c.Status),
		})
	}
	result["children"] = children
	if checklist := issue.ChildrenChecklist(iss); checklist != "" {
		result["children_checklist"] = checklist
	}
	return result
}

// issueHTTPStatus maps internal/issue errors to HTTP status codes.
func issueHTTPStatus(err error) int {
	if err == nil {
		return fiber.StatusOK
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return fiber.StatusNotFound
	case strings.Contains(msg, "required"),
		strings.Contains(msg, "invalid"),
		strings.Contains(msg, "cannot be empty"),
		strings.Contains(msg, "nothing to update"),
		strings.Contains(msg, "disabled"):
		return fiber.StatusBadRequest
	default:
		return fiber.StatusInternalServerError
	}
}

func issueFail(c *fiber.Ctx, err error) error {
	return fail(c, issueHTTPStatus(err), err.Error())
}

// GET /api/projects/:name/issues
func (s *Server) handleListIssues(c *fiber.Ctx) error {
	name := c.Params("name")

	var parentFilter = parseParentQuery(c.Query("parent_id"))

	result, err := issue.List(issue.ListFilter{
		Project:  name,
		Status:   c.Query("status"),
		Type:     c.Query("type"),
		Query:    c.Query("q"),
		ParentID: parentFilter,
		Page:     c.QueryInt("page", 1),
		Limit:    c.QueryInt("limit", 20),
		SortAsc:  c.Query("sort") == "asc",
	})
	if err != nil {
		return issueFail(c, err)
	}

	items := make([]fiber.Map, 0, len(result.Items))
	for _, iss := range result.Items {
		items = append(items, issueToListItem(iss, result.TaskCounts[iss.ID]))
	}

	return c.JSON(fiber.Map{
		"success":     true,
		"data":        items,
		"total":       result.Total,
		"page":        result.Page,
		"limit":       result.Limit,
		"total_pages": result.TotalPages,
	})
}

// issueToListItemAll adds the cross-project fields (#596) on top of the normal
// list item: project_name plus the newest linked task's id/status. `body` is
// already blanked by ListFilter.OmitBody, so wide lists stay small.
func issueToListItemAll(iss *ent.Issue, taskCount int, last *issue.LastTask) fiber.Map {
	item := issueToListItem(iss, taskCount)
	if iss.Edges.Project != nil {
		item["project_name"] = iss.Edges.Project.Name
	}
	if last != nil {
		item["last_task_id"] = last.ID
		item["last_task_status"] = last.Status
	}
	return item
}

// parseParentQuery turns the parent_id query param into a filter pointer.
// parent_id=0 → roots only; parent_id=N → children of N; absent/garbage → nil.
func parseParentQuery(raw string) *int {
	if raw == "" {
		return nil
	}
	if pid, err := strconv.Atoi(raw); err == nil {
		return &pid
	}
	return nil
}

// normalizeIssueSort maps the sort query param onto ListFilter.SortBy. "asc"
// keeps the per-project endpoint's oldest-first meaning, so it sorts by the
// default column instead of becoming a SortBy value.
func normalizeIssueSort(raw string) string {
	if raw == "updated" || raw == "updated_desc" {
		return "updated"
	}
	return ""
}

// splitCSVQuery splits a comma-separated query param and drops empty fields.
func splitCSVQuery(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GET /api/issues — issues across every in-scope project (#596).
//
//	?project=a,b&status=&type=&q=&parent_id=&page=&limit=&sort=updated&include_hidden=
//
// Items carry project_name, task_count and the last linked task; `counts` holds
// per-status totals for the board header, counted without the status filter.
func (s *Server) handleListAllIssues(c *fiber.Ctx) error {
	projects := splitCSVQuery(c.Query("project"))

	f := issue.ListFilter{
		Projects:      projects,
		AllProjects:   len(projects) == 0,
		Status:        c.Query("status"),
		Type:          c.Query("type"),
		Query:         c.Query("q"),
		ParentID:      parseParentQuery(c.Query("parent_id")),
		Page:          c.QueryInt("page", 1),
		Limit:         c.QueryInt("limit", 20),
		SortAsc:       c.Query("sort") == "asc",
		SortBy:        normalizeIssueSort(c.Query("sort")),
		IncludeHidden: c.QueryBool("include_hidden", false),
		OmitBody:      !c.QueryBool("include_body", false),
	}
	// watched=true → only shepherd-watched issues; watched=false → the rest.
	// Absent = no filter (#599 board toggle).
	if raw := c.Query("watched"); raw != "" {
		w := c.QueryBool("watched", false)
		f.Watched = &w
	}

	result, err := issue.List(f)
	if err != nil {
		return issueFail(c, err)
	}

	items := make([]fiber.Map, 0, len(result.Items))
	for _, iss := range result.Items {
		items = append(items, issueToListItemAll(iss, result.TaskCounts[iss.ID], result.LastTasks[iss.ID]))
	}

	return c.JSON(fiber.Map{
		"success":     true,
		"data":        items,
		"counts":      result.StatusCount,
		// Board cards render review rounds as k/max (#599); max is the global
		// wake cap from config, so it rides on the payload once per page.
		"max_review_rounds": config.GetInt("shepherd_max_review_rounds"),
		"total":             result.Total,
		"page":              result.Page,
		"limit":             result.Limit,
		"total_pages":       result.TotalPages,
	})
}

// POST /api/projects/:name/issues
func (s *Server) handleCreateIssue(c *fiber.Ctx) error {
	name := c.Params("name")

	var body struct {
		Title    string `json:"title"`
		Type     string `json:"type"`
		Body     string `json:"body"`
		Goal     string `json:"goal"`
		ParentID *int   `json:"parent_id,omitempty"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	iss, err := issue.Create(issue.CreateInput{
		Project:  name,
		Title:    body.Title,
		Type:     body.Type,
		Body:     body.Body,
		Goal:     body.Goal,
		ParentID: body.ParentID,
	})
	if err != nil {
		return issueFail(c, err)
	}

	// Reload with edges for consistent detail shape.
	detail, err := issue.Get(name, iss.ID)
	if err != nil {
		detail = iss
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success": true,
		"data":    issueToDetail(detail),
	})
}

// GET /api/projects/:name/issues/:id
func (s *Server) handleGetIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	iss, err := issue.Get(name, id)
	if err != nil {
		return issueFail(c, err)
	}

	return success(c, issueToDetail(iss))
}

// PATCH /api/projects/:name/issues/:id
func (s *Server) handleUpdateIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	var body struct {
		Title    *string `json:"title,omitempty"`
		Type     *string `json:"type,omitempty"`
		Body     *string `json:"body,omitempty"`
		Goal     *string `json:"goal,omitempty"`
		Status   *string `json:"status,omitempty"`
		ParentID *int    `json:"parent_id,omitempty"`
		// Shepherd (목자) fields (#597). Absent = leave unchanged.
		ShepherdWatch      *bool   `json:"shepherd_watch,omitempty"`
		ShepherdLastReview *string `json:"shepherd_last_review,omitempty"`
		// Review round reset (#599): the board's 회차 초기화 button sends 0.
		ShepherdRounds *int `json:"shepherd_rounds,omitempty"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	iss, err := issue.Update(name, id, issue.UpdateInput{
		Title:              body.Title,
		Type:               body.Type,
		Body:               body.Body,
		Goal:               body.Goal,
		Status:             body.Status,
		ParentID:           body.ParentID,
		ShepherdWatch:      body.ShepherdWatch,
		ShepherdLastReview: body.ShepherdLastReview,
		ShepherdRounds:     body.ShepherdRounds,
	})
	if err != nil {
		return issueFail(c, err)
	}

	return success(c, issueToDetail(iss))
}

// DELETE /api/projects/:name/issues/:id
func (s *Server) handleDeleteIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	if err := issue.Delete(name, id); err != nil {
		return issueFail(c, err)
	}

	return success(c, fiber.Map{
		"message": "issue deleted",
	})
}

// POST /api/projects/:name/issues/:id/execute
func (s *Server) handleExecuteIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	var body struct {
		SheepName string `json:"sheep_name"`
		Model     string `json:"model,omitempty"`
		// Watch (#599): the board's "감시를 켜고 실행" option — enqueueing also
		// turns on shepherd_watch for the whole cascade.
		Watch bool `json:"watch,omitempty"`
	}
	// Empty body is allowed (sheep falls back to project assignment).
	_ = c.BodyParser(&body)

	result, err := issue.Execute(issue.ExecuteInput{
		Project:   name,
		IssueID:   id,
		SheepName: body.SheepName,
		Model:     body.Model,
		Watch:     body.Watch,
	})
	if err != nil {
		return issueFail(c, err)
	}

	// Trigger immediate processing when daemon processor is available.
	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, fiber.Map{
		"task_id":    result.TaskID,
		"task_ids":   result.TaskIDs,
		"issue_ids":  result.IssueIDs,
		"sheep_name": result.SheepName,
		"issue_id":   result.IssueID,
	})
}
