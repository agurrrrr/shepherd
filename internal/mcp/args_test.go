package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseIntArg(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want int
		ok   bool
	}{
		{"float64", float64(8201), 8201, true},
		{"float32", float32(7), 7, true},
		{"int", 42, 42, true},
		{"int64", int64(99), 99, true},
		{"json.Number int", json.Number("8201"), 8201, true},
		{"json.Number float", json.Number("8201.0"), 8201, true},
		{"string", "8201", 8201, true},
		{"string hash", "#8201", 8201, true},
		{"string spaced", "  8201  ", 8201, true},
		{"string float", "8201.0", 8201, true},
		{"empty string", "", 0, false},
		{"non numeric", "nope", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseIntArg(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseIntArg(%v) = (%d, %v); want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestToInt_NumericString(t *testing.T) {
	if got := toInt("42"); got != 42 {
		t.Fatalf("toInt(\"42\") = %d; want 42", got)
	}
	if got := toInt("#42"); got != 42 {
		t.Fatalf("toInt(\"#42\") = %d; want 42", got)
	}
	if got := toInt("nope"); got != 0 {
		t.Fatalf("toInt(\"nope\") = %d; want 0", got)
	}
}

func TestRequireTaskID(t *testing.T) {
	id, err := requireTaskID(map[string]interface{}{"task_id": float64(8201)})
	if err != nil || id != 8201 {
		t.Fatalf("float64 task_id: id=%d err=%v", id, err)
	}

	id, err = requireTaskID(map[string]interface{}{"task_id": "8201"})
	if err != nil || id != 8201 {
		t.Fatalf("string task_id: id=%d err=%v", id, err)
	}

	id, err = requireTaskID(map[string]interface{}{"task_id": "#8201"})
	if err != nil || id != 8201 {
		t.Fatalf("hash task_id: id=%d err=%v", id, err)
	}

	id, err = requireTaskID(map[string]interface{}{"id": "8178"})
	if err != nil || id != 8178 {
		t.Fatalf("id alias: id=%d err=%v", id, err)
	}

	_, err = requireTaskID(map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "task_id가 필요합니다") {
		t.Fatalf("missing: %v", err)
	}

	_, err = requireTaskID(map[string]interface{}{"task_id": "abc"})
	if err == nil || !strings.Contains(err.Error(), "해석할 수 없습니다") {
		t.Fatalf("unparseable: %v", err)
	}

	_, err = requireTaskID(map[string]interface{}{"task_id": float64(0)})
	if err == nil || !strings.Contains(err.Error(), "해석할 수 없습니다") {
		t.Fatalf("zero: %v", err)
	}
}

func TestHandleGetTaskDetail_ArgErrors(t *testing.T) {
	_, err := handleGetTaskDetail(map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "task_id가 필요합니다") {
		t.Fatalf("missing task_id: %v", err)
	}

	_, err = handleGetTaskDetail(map[string]interface{}{"task_id": "not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "해석할 수 없습니다") {
		t.Fatalf("bad string: %v", err)
	}
}

func TestGetTaskDetailToolDef_DocumentsCoercion(t *testing.T) {
	var def Tool
	found := false
	for _, d := range ListCoreToolDefs() {
		if d.Name == "get_task_detail" {
			def = d
			found = true
			break
		}
	}
	if !found {
		t.Fatal("get_task_detail tool definition not found")
	}
	desc := def.Description + def.InputSchema.Properties["task_id"].Description
	if !strings.Contains(desc, "문자열") && !strings.Contains(strings.ToLower(desc), "string") {
		t.Errorf("schema should mention string IDs: %q", desc)
	}
	if _, ok := def.InputSchema.Properties["id"]; !ok {
		t.Error("schema should advertise id alias")
	}
}
