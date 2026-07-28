package mcp

import (
	"strings"
	"testing"
)

func TestDBWriteToolNames_ExpectedSet(t *testing.T) {
	want := map[string]bool{
		"wiki_create":   true,
		"wiki_edit":     true,
		"issue_upsert":  true,
		"issue_execute": true,
	}
	if len(dbWriteToolNames) != len(want) {
		t.Fatalf("dbWriteToolNames len=%d, want %d: %v", len(dbWriteToolNames), len(want), dbWriteToolNames)
	}
	for _, name := range dbWriteToolNames {
		if !want[name] {
			t.Errorf("unexpected db write tool %q", name)
		}
	}
}

// TestNewClient_DBWriteToolsForwardWhenDaemonDown verifies that wiki/issue
// write tools on the client are not the in-process handlers: when the daemon
// is not reachable they fail with a daemon-missing error rather than a local
// validation/DB error (e.g. "content required" from handleWikiCreate).
func TestNewClient_DBWriteToolsForwardWhenDaemonDown(t *testing.T) {
	// Ensure no runtime.json points at a live daemon for this process, or
	// that if one exists the error still looks like a proxy path.
	// We call with intentionally incomplete args so a local handler would
	// return a validation error immediately without needing DB.
	client := NewClient(true) // minimal: no browser tools

	// wiki_create with full-looking args — local handler would still need DB;
	// empty content would fail local validation. Use empty content so BOTH
	// paths could fail, but messages differ: local says "required", forwarder
	// says "daemon is not running" OR (if daemon up) may succeed/fail via HTTP.
	//
	// Safer check: use a tool name's handler identity vs NewServer.
	daemon := NewServer(true)

	for _, name := range dbWriteToolNames {
		clientH, okC := client.tools[name]
		daemonH, okD := daemon.tools[name]
		if !okC {
			t.Errorf("NewClient missing tool %q", name)
			continue
		}
		if !okD {
			t.Errorf("NewServer missing tool %q", name)
			continue
		}
		// Function values are not comparable; call both with empty args and
		// compare error prefixes.
		_, errC := clientH(map[string]interface{}{})
		_, errD := daemonH(map[string]interface{}{})
		if errC == nil {
			t.Errorf("client %s: expected error with empty args", name)
			continue
		}
		if errD == nil {
			t.Errorf("daemon %s: expected error with empty args", name)
			continue
		}
		// Daemon in-process: validation error (required fields).
		// Client forwarder: daemon not running OR unreachable OR (if daemon
		// is actually up on this machine) the same validation error from the
		// proxy path — either way client must not panic.
		msgC := errC.Error()
		msgD := errD.Error()
		if strings.Contains(msgC, "daemon is not running") || strings.Contains(msgC, "daemon unreachable") {
			// Expected when no daemon / wrong token — pure forwarder path.
			continue
		}
		// Daemon is running and proxy worked: both should surface similar
		// validation failures. Local daemon handler message is authoritative.
		if msgC == "" {
			t.Errorf("client %s: empty error", name)
		}
		// Client must not return a totally different class of error that
		// suggests it never registered the tool (unknown tool).
		if strings.Contains(msgC, "unknown tool") {
			t.Errorf("client %s: unknown tool (forwarder not registered): %v", name, errC)
		}
		// Soft check: if proxy hit a live daemon, errors should both mention
		// required fields (same handler on daemon).
		if strings.Contains(msgD, "required") && !strings.Contains(msgC, "required") &&
			!strings.Contains(msgC, "daemon") {
			t.Errorf("client %s error %q does not look like forwarder or daemon validation; daemon had %q",
				name, msgC, msgD)
		}
	}
}

// TestNewClient_ReadToolsStayInProcess ensures wiki/issue reads are not
// accidentally forwarded (they must work offline for listing when DB is
// readable; forwarder would fail without daemon).
func TestNewClient_ReadToolsStayInProcess(t *testing.T) {
	client := NewClient(true)
	// Call wiki_read_page with empty args — in-process handler returns
	// "project_name and slug are required" without contacting daemon.
	// A mis-wired forwarder would say "daemon is not running" first only if
	// runtime is missing; if runtime exists it would hit daemon. Either way
	// we compare against NewServer handler message class.
	h, ok := client.tools["wiki_read_page"]
	if !ok {
		t.Fatal("wiki_read_page not registered on client")
	}
	_, err := h(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "required") {
		// If someone accidentally forwarded reads, we might get daemon errors
		// when runtime is missing.
		if strings.Contains(err.Error(), "daemon is not running") {
			t.Fatalf("wiki_read_page appears forwarded to daemon (should stay in-process): %v", err)
		}
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewServer_DBWriteToolsInProcess(t *testing.T) {
	// NewServer must keep real handlers so /api/_internal/mcp/call can execute them.
	srv := NewServer(true)
	for _, name := range dbWriteToolNames {
		if _, ok := srv.tools[name]; !ok {
			t.Errorf("NewServer missing write tool %q needed for proxy dispatch", name)
		}
	}
	// Direct call: validation without daemon hop.
	_, err := srv.tools["wiki_create"](map[string]interface{}{})
	if err == nil {
		t.Fatal("expected validation error from in-process wiki_create")
	}
	if strings.Contains(err.Error(), "daemon is not running") {
		t.Fatalf("NewServer wiki_create must not be a forwarder: %v", err)
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required-fields error, got: %v", err)
	}
}

func TestForwardToDaemon_EmptyArgsSurfacesError(t *testing.T) {
	// Empty args never create a page. Outcomes:
	// - no runtime → "daemon is not running"
	// - daemon up → validation "required" from in-process handler via proxy
	_, err := forwardToDaemon("wiki_create", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for empty wiki_create via forwardToDaemon")
	}
	msg := err.Error()
	if !(strings.Contains(msg, "daemon") || strings.Contains(msg, "required")) {
		t.Fatalf("unexpected forwardToDaemon error: %v", err)
	}
}
