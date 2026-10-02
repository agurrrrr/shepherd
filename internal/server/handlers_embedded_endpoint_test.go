package server

import (
	"encoding/json"
	"testing"

	"github.com/agurrrrr/shepherd/internal/config"
)

// TestEmbeddedEndpointFromJSON_ReasoningControls: the Web UI save body
// (snake_case keys) must reach the stored endpoint, or saving the edit form
// silently resets the issue #347 settings.
func TestEmbeddedEndpointFromJSON_ReasoningControls(t *testing.T) {
	raw := `{"id":"strata","base_url":"http://127.0.0.1:8084/v1","model":"m","enabled":true,
		"context_tokens":100000,"reasoning_budget_tokens":6144,"handoff_no_thinking":true}`
	var body config.EmbeddedEndpointJSON
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	ep := embeddedEndpointFromJSON(body)
	if ep.ReasoningBudgetTokens != 6144 || !ep.HandoffNoThinking {
		t.Fatalf("fields dropped: %+v", ep)
	}

	var plain config.EmbeddedEndpointJSON
	if err := json.Unmarshal([]byte(`{"id":"plain","base_url":"x","model":"m"}`), &plain); err != nil {
		t.Fatal(err)
	}
	if ep := embeddedEndpointFromJSON(plain); ep.ReasoningBudgetTokens != 0 || ep.HandoffNoThinking {
		t.Fatalf("defaults must be off: %+v", ep)
	}
}
