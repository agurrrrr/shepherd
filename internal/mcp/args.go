package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func toBool(v interface{}) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// toInt converts MCP number arguments to int. JSON numbers arrive as float64,
// but other hosts send strings ("8201"), json.Number, or native ints.
// Returns 0 for missing or non-numeric values.
func toInt(v interface{}) int {
	n, ok := parseIntArg(v)
	if !ok {
		return 0
	}
	return n
}

// parseIntArg accepts the numeric shapes MCP hosts actually send.
func parseIntArg(v interface{}) (int, bool) {
	switch n := v.(type) {
	case nil:
		return 0, false
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		if uint64(n) > uint64(math.MaxInt) {
			return 0, false
		}
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		if n > uint64(math.MaxInt) {
			return 0, false
		}
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			f, ferr := n.Float64()
			if ferr != nil {
				return 0, false
			}
			return int(f), true
		}
		return int(i), true
	case string:
		s := strings.TrimSpace(n)
		s = strings.TrimPrefix(s, "#")
		if s == "" {
			return 0, false
		}
		i, err := strconv.Atoi(s)
		if err != nil {
			f, ferr := strconv.ParseFloat(s, 64)
			if ferr != nil {
				return 0, false
			}
			return int(f), true
		}
		return i, true
	default:
		return 0, false
	}
}

func argValue(args map[string]interface{}, keys ...string) (interface{}, bool) {
	if args == nil {
		return nil, false
	}
	for _, k := range keys {
		if v, ok := args[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// requireTaskID reads task_id, falling back to id (CLI / issue_get alias).
// Accepts number or numeric string. Distinguishes missing vs unparseable.
func requireTaskID(args map[string]interface{}) (int, error) {
	v, ok := argValue(args, "task_id", "id")
	if !ok {
		return 0, fmt.Errorf("task_id가 필요합니다 (숫자 또는 숫자 문자열, 예: 8201). id 키도 허용합니다")
	}
	n, parsed := parseIntArg(v)
	if !parsed || n <= 0 {
		return 0, fmt.Errorf("task_id를 해석할 수 없습니다: %v (양의 정수여야 합니다)", v)
	}
	return n, nil
}
