package names

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSheepNameLength is the longest a sheep name may be, in runes. Sheep names
// become directory names (~/.shepherd/sheep/<name>/, ~/.shepherd/browser/<name>/)
// and CLI arguments, so they must stay short and filesystem-safe.
const MaxSheepNameLength = 32

// forbiddenNameChars are characters that may not appear in a sheep name. The
// set mirrors what filesystems and shells make life difficult about; names flow
// into directory paths, argv, and log lines.
const forbiddenNameChars = `/ \:*?"<>|`

// ValidateCustom checks a user- or LLM-chosen sheep name. It is the single rule
// book shared by worker.Rename, internal/namegen's candidate filter, and the
// rename REST handler — so a name that passes the UI always passes the CLI too.
//
// Rules (issue #638 §5):
//   - not empty after trimming surrounding whitespace
//   - at most MaxSheepNameLength runes
//   - not a reserved name (the manager sheep's names)
//   - no whitespace, control characters, or / \ : * ? " < > |
//   - does not start with "." and is not "." or ".."
//
// Names already stored in the DB are NOT validated by this function — it only
// gates newly chosen names. Existing pool names like "양13" stay legal even
// though the LLM prompt forbids minting new numbered names.
func ValidateCustom(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("name must not be empty")
	}
	if utf8.RuneCountInString(trimmed) > MaxSheepNameLength {
		return fmt.Errorf("name too long (max %d characters)", MaxSheepNameLength)
	}
	if IsReserved(trimmed) {
		return fmt.Errorf("'%s' is a reserved name", trimmed)
	}
	if trimmed == "." || trimmed == ".." || strings.HasPrefix(trimmed, ".") {
		return fmt.Errorf("name must not start with '.'")
	}
	for _, r := range trimmed {
		if strings.ContainsRune(forbiddenNameChars, r) {
			return fmt.Errorf("name must not contain '%c'", r)
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("name must not contain control characters")
		}
		if unicode.IsSpace(r) {
			return fmt.Errorf("name must not contain whitespace")
		}
	}
	return nil
}

// IsNumberedPoolName reports whether a name has the auto-generated numbered
// pool form "양13", "양14", ... . The LLM prompt and candidate filter forbid
// minting new names in this form (issue #638 §3); existing DB names with the
// form are unaffected because ValidateCustom does not check it.
func IsNumberedPoolName(name string) bool {
	const prefix = "양"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	digits := name[len(prefix):]
	if digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
