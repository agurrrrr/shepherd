package server

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
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
	return item
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

	var parentFilter *int
	if raw := c.Query("parent_id"); raw != "" {
		// parent_id=0 → roots only; parent_id=N → children of N
		if pid, err := strconv.Atoi(raw); err == nil {
			parentFilter = &pid
		}
	}

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
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	iss, err := issue.Update(name, id, issue.UpdateInput{
		Title:    body.Title,
		Type:     body.Type,
		Body:     body.Body,
		Goal:     body.Goal,
		Status:   body.Status,
		ParentID: body.ParentID,
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
	}
	// Empty body is allowed (sheep falls back to project assignment).
	_ = c.BodyParser(&body)

	result, err := issue.Execute(issue.ExecuteInput{
		Project:   name,
		IssueID:   id,
		SheepName: body.SheepName,
		Model:     body.Model,
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
