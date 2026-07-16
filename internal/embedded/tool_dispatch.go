package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// maxParallelReadTools bounds concurrent read-only tool goroutines within a
// single assistant turn. Prevents local-disk thrash from unbounded fan-out
// when the model requests many read_file/grep/glob calls at once.
const maxParallelReadTools = 8

// isParallelSafeTool reports tools that are pure reads with no session side
// effects. Only these may run concurrently within a batch.
//
// Never parallel-safe: write_file, edit_file, bash, spawn_subagents, todo_write,
// browser_* / other MCP tools (sheep-session shared state).
func isParallelSafeTool(name string) bool {
	switch name {
	case "read_file", "grep", "glob":
		return true
	default:
		return false
	}
}

// batchAllParallelSafe is true when every tool_call in the batch is a pure
// read and there is more than one call. If any side-effecting tool is mixed
// in, the whole batch stays sequential so e.g. read+edit on the same path
// cannot race (simplest safe policy; matches Phase 3-3 / task #7547).
func batchAllParallelSafe(calls []ToolCall) bool {
	if len(calls) <= 1 {
		return false
	}
	for _, tc := range calls {
		if !isParallelSafeTool(tc.Func.Name) {
			return false
		}
	}
	return true
}

// toolBatchOutcome is one tool-call result ready to append as a role:tool
// history message. Spawn may also carry token/cost deltas for the parent task.
type toolBatchOutcome struct {
	msg              ChatMessage
	promptTokens     int64
	completionTokens int64
	costUSD          float64
}

// ensureToolCallIDs assigns fallback IDs to tool calls that arrived without one
// (llama.cpp streaming often returns empty tc.ID). Mutates calls in place.
func ensureToolCallIDs(calls []ToolCall, iteration int) {
	for idx := range calls {
		if calls[idx].ID == "" {
			calls[idx].ID = fmt.Sprintf("call_%d_%d", iteration, idx)
		}
	}
}

// runToolCallBatch executes a turn's tool_calls either in parallel (all-read
// batch) or sequentially (any side-effect tool present, or single call).
//
// Invariants preserved from the pre-parallel loop:
//   - tool results are returned in original tool_call order (id-matched)
//   - per-tool errors become "Error: …" content (no fail-fast cancel of siblings)
//   - spawn_subagents bypasses the 5-minute dispatch timeout
//   - pendingImages are drained by the caller after the batch
//   - OnOutput headers/results are emitted only from this function's main
//     goroutine so live output stays ordered and race-free
func runToolCallBatch(
	ctx context.Context,
	tr *ToolRegistry,
	calls []ToolCall,
	iteration int,
	opts ExecuteOptions,
	markToolUsed func(string),
) []toolBatchOutcome {
	if len(calls) == 0 {
		return nil
	}
	// Work on a local copy so ID assignment doesn't race with the caller's slice
	// if it is shared; IDs are needed for ToolCallID on results.
	work := make([]ToolCall, len(calls))
	copy(work, calls)
	ensureToolCallIDs(work, iteration)

	if batchAllParallelSafe(work) {
		return runToolCallBatchParallel(ctx, tr, work, opts, markToolUsed)
	}
	return runToolCallBatchSequential(ctx, tr, work, opts, markToolUsed)
}

// runToolCallBatchSequential is the original for-loop semantics: one tool at a
// time, including spawn_subagents special casing.
func runToolCallBatchSequential(
	ctx context.Context,
	tr *ToolRegistry,
	calls []ToolCall,
	opts ExecuteOptions,
	markToolUsed func(string),
) []toolBatchOutcome {
	out := make([]toolBatchOutcome, 0, len(calls))
	for _, tc := range calls {
		parsedArgs, _ := normalizeJSON(tc.Func.Args)
		emitOutput(opts.OnOutput, toolCallHeader(tc.Func.Name, parsedArgs))

		// spawn_subagents bypasses dispatchTool to avoid the 5-minute hard
		// timeout (#7461 I1). Sub-agents may run for tens of minutes.
		if tc.Func.Name == "spawn_subagents" && tr.HasSubagentSpawner() {
			outcome := runSpawnToolCall(ctx, tr, tc, opts, markToolUsed)
			out = append(out, outcome)
			continue
		}

		result, err := dispatchTool(ctx, tr, tc, opts)
		if markToolUsed != nil {
			markToolUsed(tc.Func.Name)
		}
		resultStr := result
		if err != nil {
			resultStr = fmt.Sprintf("Error: %v", err)
		}
		if preview := indentResult(resultStr); preview != "" {
			emitOutput(opts.OnOutput, preview)
		}
		out = append(out, toolBatchOutcome{
			msg: ChatMessage{
				Role:       ChatRoleTool,
				Content:    truncateToolResult(resultStr, tc.Func.Name),
				ToolCallID: tc.ID,
			},
		})
	}
	return out
}

// runToolCallBatchParallel runs an all-read batch with bounded concurrency.
// Headers are emitted in call order before work starts; results are emitted
// and returned in the same order after all workers finish.
func runToolCallBatchParallel(
	ctx context.Context,
	tr *ToolRegistry,
	calls []ToolCall,
	opts ExecuteOptions,
	markToolUsed func(string),
) []toolBatchOutcome {
	// Emit headers in original order before fanning out (stable live output).
	for _, tc := range calls {
		parsedArgs, _ := normalizeJSON(tc.Func.Args)
		emitOutput(opts.OnOutput, toolCallHeader(tc.Func.Name, parsedArgs))
	}

	// Disable read_file auto-paging for the batch lifetime (concurrent same-
	// path reads must not race lastRead* into false "already complete").
	tr.beginParallelReads()
	defer tr.endParallelReads()

	type slot struct {
		result string
		err    error
	}
	slots := make([]slot, len(calls))
	sem := make(chan struct{}, maxParallelReadTools)
	var wg sync.WaitGroup

	for i, tc := range calls {
		wg.Add(1)
		go func(i int, tc ToolCall) {
			defer wg.Done()
			// Bound concurrency; respect cancel while waiting for a slot.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				slots[i] = slot{err: fmt.Errorf("tool %s aborted: %w", tc.Func.Name, ctx.Err())}
				return
			}
			defer func() { <-sem }()

			result, err := dispatchTool(ctx, tr, tc, opts)
			slots[i] = slot{result: result, err: err}
		}(i, tc)
	}
	wg.Wait()

	out := make([]toolBatchOutcome, 0, len(calls))
	for i, tc := range calls {
		if markToolUsed != nil {
			markToolUsed(tc.Func.Name)
		}
		resultStr := slots[i].result
		if slots[i].err != nil {
			resultStr = fmt.Sprintf("Error: %v", slots[i].err)
		}
		if preview := indentResult(resultStr); preview != "" {
			emitOutput(opts.OnOutput, preview)
		}
		out = append(out, toolBatchOutcome{
			msg: ChatMessage{
				Role:       ChatRoleTool,
				Content:    truncateToolResult(resultStr, tc.Func.Name),
				ToolCallID: tc.ID,
			},
		})
	}
	return out
}

func runSpawnToolCall(
	ctx context.Context,
	tr *ToolRegistry,
	tc ToolCall,
	opts ExecuteOptions,
	markToolUsed func(string),
) toolBatchOutcome {
	var saArgs map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Func.Args), &saArgs); err != nil {
		resultStr := fmt.Sprintf("Error: JSON parse error for spawn_subagents: %v", err)
		if preview := indentResult(resultStr); preview != "" {
			emitOutput(opts.OnOutput, preview)
		}
		return toolBatchOutcome{
			msg: ChatMessage{
				Role:       ChatRoleTool,
				Content:    truncateToolResult(resultStr, tc.Func.Name),
				ToolCallID: tc.ID,
			},
		}
	}

	spawnResult, saErr := executeSpawnSubagents(ctx, tr, saArgs, opts.OnOutput)
	if markToolUsed != nil {
		markToolUsed(tc.Func.Name)
	}

	var resultStr string
	var promptTok, completionTok int64
	var cost float64
	if saErr != nil {
		resultStr = fmt.Sprintf("Error: %v", saErr)
	} else {
		resultStr = spawnResult.Content
		promptTok = spawnResult.PromptTokens
		completionTok = spawnResult.CompletionTokens
		cost = spawnResult.CostUSD
	}
	if preview := indentResult(resultStr); preview != "" {
		emitOutput(opts.OnOutput, preview)
	}
	return toolBatchOutcome{
		msg: ChatMessage{
			Role:       ChatRoleTool,
			Content:    truncateToolResult(resultStr, tc.Func.Name),
			ToolCallID: tc.ID,
		},
		promptTokens:     promptTok,
		completionTokens: completionTok,
		costUSD:          cost,
	}
}
