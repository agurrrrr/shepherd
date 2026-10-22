package apiclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSSEStream(t *testing.T) {
	body := ": connected\n\n" +
		"event: output\ndata: {\"text\":\"hello\"}\n\n" +
		"event: output\ndata: {\"text\":\"world\"}\n\n" +
		"event: done\ndata: {\"result\":\"ok\",\"files_modified\":[\"a.go\"]}\n\n"

	type frame struct {
		event string
		data  string
	}
	var got []frame

	err := parseSSEStream(strings.NewReader(body), func(eventType string, data json.RawMessage) {
		got = append(got, frame{event: eventType, data: string(data)})
	})
	if err != nil {
		t.Fatalf("parseSSEStream returned error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 frames, got %d: %+v", len(got), got)
	}
	if got[0].event != "output" || got[0].data != `{"text":"hello"}` {
		t.Errorf("frame 0 = %+v", got[0])
	}
	if got[1].event != "output" || got[1].data != `{"text":"world"}` {
		t.Errorf("frame 1 = %+v", got[1])
	}
	if got[2].event != "done" || !strings.Contains(got[2].data, `"result":"ok"`) {
		t.Errorf("frame 2 = %+v", got[2])
	}
}

func TestParseSSEStreamSkipsFramelessData(t *testing.T) {
	// A data line without a preceding event line must not be emitted.
	var got []string
	err := parseSSEStream(strings.NewReader("data: {\"x\":1}\n\n"), func(eventType string, _ json.RawMessage) {
		got = append(got, eventType)
	})
	if err != nil {
		t.Fatalf("parseSSEStream returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no frames, got %v", got)
	}
}

func TestPostRevertSuccessAndConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tasks/7/revert/undo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"changed":true,"files":[],"conflicts":[]}}`))
			return
		}
		var body struct {
			DryRun bool `json:"dry_run"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.DryRun {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"success":false,"message":"conflict","conflicts":["a.txt"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"changed":true,"files":[{"path":"a.txt","status":"modified","additions":1,"deletions":0}],"conflicts":[]}}`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, http: srv.Client()}
	res, err := c.RevertTask(7, false)
	if err != nil {
		t.Fatalf("RevertTask: %v", err)
	}
	if !res.Changed || len(res.Files) != 1 || res.Files[0].Path != "a.txt" {
		t.Fatalf("result = %+v", res)
	}

	if _, err := c.RevertTask(7, true); err == nil {
		t.Fatal("expected conflict error")
	} else {
		var revertErr *RevertError
		if !errors.As(err, &revertErr) || len(revertErr.Conflicts) != 1 || revertErr.Conflicts[0] != "a.txt" {
			t.Fatalf("error = %v", err)
		}
	}

	res, err = c.UndoRevertTask(7)
	if err != nil || !res.Changed {
		t.Fatalf("UndoRevertTask = %+v, %v", res, err)
	}
}
