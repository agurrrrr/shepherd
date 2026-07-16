package embedded

import (
	"fmt"
	"strings"
)

// Todo status values for the opt-in todo_write tool (Phase 3-2 / task #7547).
// Mirrors grok-build TodoStatus snake_case tags; cancelled is terminal like completed.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoCompleted  = "completed"
	TodoCancelled  = "cancelled"
)

// maxTodoGateNudges bounds how many times the turn-end incomplete-todo gate
// injects a <system-reminder> before marking the task incomplete.
// Matches grok DEFAULT_TODO_GATE_MAX_FIRES = 2.
const maxTodoGateNudges = 2

// todoGateNudgeBody is the fixed lead-in of the incomplete-todo reminder.
// Listed items are appended by buildTodoGateReminder.
const todoGateNudgeLead = "미완료 todo 항목이 남아 있습니다. 완료 선언 전에 남은 항목을 실제 도구로 진행하거나, " +
	"todo_write로 completed/cancelled로 갱신하세요. " +
	"단순히 '완료했습니다'만 말하지 마세요."

// TodoItem is one structured plan step held in session state.
type TodoItem struct {
	ID      string
	Content string
	Status  string
}

// TodoState is the in-session structured plan (no persistence, no LLM classifier).
// Empty state means the model never tracked todos — the gate must not fire.
type TodoState struct {
	items []TodoItem
}

// HasIncomplete reports whether any item is still pending or in_progress.
// Cancelled and completed are terminal. Empty state is not incomplete.
func (s *TodoState) HasIncomplete() bool {
	if s == nil {
		return false
	}
	for _, it := range s.items {
		switch it.Status {
		case TodoPending, TodoInProgress:
			return true
		}
	}
	return false
}

// IsEmpty reports whether no todos are tracked.
func (s *TodoState) IsEmpty() bool {
	return s == nil || len(s.items) == 0
}

// Items returns a copy of the current list (order preserved).
func (s *TodoState) Items() []TodoItem {
	if s == nil || len(s.items) == 0 {
		return nil
	}
	out := make([]TodoItem, len(s.items))
	copy(out, s.items)
	return out
}

// Summary returns a compact multi-line summary for the tool result.
func (s *TodoState) Summary() string {
	if s.IsEmpty() {
		return "No tasks currently tracked."
	}
	var b strings.Builder
	for _, it := range s.items {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", it.Status, it.ID, it.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

// IncompleteBuckets returns content strings for pending and in_progress items.
func (s *TodoState) IncompleteBuckets() (pending, inProgress []string) {
	if s == nil {
		return nil, nil
	}
	for _, it := range s.items {
		switch it.Status {
		case TodoPending:
			pending = append(pending, it.Content)
		case TodoInProgress:
			inProgress = append(inProgress, it.Content)
		}
	}
	return pending, inProgress
}

// TodoUpdate is one item from a todo_write call.
type TodoUpdate struct {
	ID      string
	Content string // empty means omit (merge keeps prior / replace falls back to id)
	Status  string // empty defaults to pending on create
}

// ApplyReplace fully replaces the todo list with updates.
func (s *TodoState) ApplyReplace(updates []TodoUpdate) error {
	if s == nil {
		return fmt.Errorf("todo state is nil")
	}
	if err := validateTodoUpdates(updates); err != nil {
		return err
	}
	s.items = s.items[:0]
	for _, u := range updates {
		content := u.Content
		if content == "" {
			content = u.ID
		}
		status := normalizeTodoStatus(u.Status)
		if status == "" {
			status = TodoPending
		}
		s.items = append(s.items, TodoItem{ID: u.ID, Content: content, Status: status})
	}
	return nil
}

// ApplyMerge merges updates by id. Existing items can flip status without content.
// Unknown ids are appended (id used as content fallback when content empty).
func (s *TodoState) ApplyMerge(updates []TodoUpdate) error {
	if s == nil {
		return fmt.Errorf("todo state is nil")
	}
	if err := validateTodoUpdates(updates); err != nil {
		return err
	}
	index := make(map[string]int, len(s.items))
	for i, it := range s.items {
		index[it.ID] = i
	}
	for _, u := range updates {
		if i, ok := index[u.ID]; ok {
			if u.Content != "" {
				s.items[i].Content = u.Content
			}
			if st := normalizeTodoStatus(u.Status); st != "" {
				s.items[i].Status = st
			}
			continue
		}
		content := u.Content
		if content == "" {
			content = u.ID
		}
		status := normalizeTodoStatus(u.Status)
		if status == "" {
			status = TodoPending
		}
		s.items = append(s.items, TodoItem{ID: u.ID, Content: content, Status: status})
		index[u.ID] = len(s.items) - 1
	}
	return nil
}

// Apply chooses merge vs replace. When merge is false but every update is a
// status-only touch of an existing id (model forgot merge:true), auto-upgrade
// to merge so content is not wiped (grok regression guard).
func (s *TodoState) Apply(merge bool, updates []TodoUpdate) error {
	if s == nil {
		return fmt.Errorf("todo state is nil")
	}
	effectiveMerge := merge
	if !merge && !s.IsEmpty() && len(updates) > 0 && allStatusOnlyExisting(s, updates) {
		effectiveMerge = true
	}
	if effectiveMerge {
		return s.ApplyMerge(updates)
	}
	return s.ApplyReplace(updates)
}

func allStatusOnlyExisting(s *TodoState, updates []TodoUpdate) bool {
	index := make(map[string]struct{}, len(s.items))
	for _, it := range s.items {
		index[it.ID] = struct{}{}
	}
	for _, u := range updates {
		if u.Content != "" {
			return false
		}
		if _, ok := index[u.ID]; !ok {
			return false
		}
	}
	return true
}

func validateTodoUpdates(updates []TodoUpdate) error {
	seen := make(map[string]struct{}, len(updates))
	for _, u := range updates {
		id := strings.TrimSpace(u.ID)
		if id == "" {
			return fmt.Errorf("todo item missing id")
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate todo id %q", id)
		}
		seen[id] = struct{}{}
		if st := u.Status; st != "" && normalizeTodoStatus(st) == "" {
			return fmt.Errorf("invalid todo status %q (want pending|in_progress|completed|cancelled)", st)
		}
	}
	return nil
}

func normalizeTodoStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case TodoPending, "todo":
		return TodoPending
	case TodoInProgress, "in-progress", "doing":
		return TodoInProgress
	case TodoCompleted, "done":
		return TodoCompleted
	case TodoCancelled, "canceled":
		return TodoCancelled
	case "":
		return ""
	default:
		return ""
	}
}

// buildTodoGateReminder builds the system-reminder body for incomplete todos.
func buildTodoGateReminder(pending, inProgress []string) string {
	var b strings.Builder
	b.WriteString(todoGateNudgeLead)
	b.WriteString("\n")
	if len(inProgress) > 0 {
		b.WriteString("\nIn-progress:\n")
		for _, c := range inProgress {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(pending) > 0 {
		b.WriteString("\nPending:\n")
		for _, c := range pending {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseTodoWriteArgs extracts merge flag and updates from tool arguments.
// Accepts todos or steps arrays; content/text fields; id optional (auto "1","2",…).
func parseTodoWriteArgs(args map[string]interface{}) (merge bool, updates []TodoUpdate, err error) {
	merge = true // default merge (grok convention)
	if v, ok := args["merge"]; ok {
		switch x := v.(type) {
		case bool:
			merge = x
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "false", "0", "no", "off":
				merge = false
			case "true", "1", "yes", "on":
				merge = true
			}
		case float64:
			merge = x != 0
		}
	}

	rawList := firstNonNil(args["todos"], args["steps"], args["items"])
	if rawList == nil {
		return merge, nil, fmt.Errorf("todo_write requires todos (or steps) array")
	}
	list, ok := rawList.([]interface{})
	if !ok {
		return merge, nil, fmt.Errorf("todos must be an array")
	}

	updates = make([]TodoUpdate, 0, len(list))
	for i, el := range list {
		m, ok := el.(map[string]interface{})
		if !ok {
			return merge, nil, fmt.Errorf("todos[%d] must be an object", i)
		}
		id := stringArg(m, "id")
		if id == "" {
			id = fmt.Sprintf("%d", i+1)
		}
		content := stringArg(m, "content")
		if content == "" {
			content = stringArg(m, "text")
		}
		if content == "" {
			content = stringArg(m, "description")
		}
		status := stringArg(m, "status")
		updates = append(updates, TodoUpdate{ID: id, Content: content, Status: status})
	}
	return merge, updates, nil
}

func firstNonNil(vals ...interface{}) interface{} {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func stringArg(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}
