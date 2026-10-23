package worker

import (
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/skill"

	"github.com/agurrrrr/shepherd/internal/names"
)

// shepherdContextProvider supplies the manager-side system context (#595):
// operating instructions, workspace, management-scope table, autonomy notice,
// and the user's extra shepherd instructions. The implementation lives in
// internal/manager (manager.BuildShepherdContext), but worker cannot import
// manager — manager imports worker. So the daemon registers the callback at
// startup (cmd/shepherd/main.go), same pattern as SetEmbeddedExecutor.
var shepherdContextProvider func() string

// SetShepherdContextProvider registers the builder used when a prompt is
// assembled for the manager sheep. Passing nil clears it (tests).
func SetShepherdContextProvider(fn func() string) {
	shepherdContextProvider = fn
}

// shepherdContextFor returns the manager context for the manager sheep and ""
// for every ordinary sheep — that is the guarantee that ordinary prompts stay
// unchanged (#595 goal).
func shepherdContextFor(sheepName string) string {
	if !names.IsManager(sheepName) || shepherdContextProvider == nil {
		return ""
	}
	return shepherdContextProvider()
}

// skillsSummaryFor returns the skill-summary section for a sheep. Ordinary
// sheep get their project's skills (global + project scope). The manager has
// no project, so it only gets the enabled GLOBAL skills (#595): it needs to
// know skills like discord-notify exist, and skill_load fetches the content.
func skillsSummaryFor(sheepName string) string {
	if text := getProjectSkillsSummary(sheepName); text != "" {
		return text
	}
	if names.IsManager(sheepName) {
		return getGlobalSkillsSummary()
	}
	return ""
}

// getGlobalSkillsSummary renders enabled global-scope skills as a compact
// name+description list (same shape as the project summary).
func getGlobalSkillsSummary() string {
	skills, err := skill.ListEnabledGlobal()
	if err != nil || len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Global Skills - use skill_load MCP tool for full content]\n")
	writeSkillLines(&sb, skills)
	return sb.String()
}

// writeSkillLines renders the shared "- name: description" body of a skill
// summary.
func writeSkillLines(sb *strings.Builder, skills []*ent.Skill) {
	for _, sk := range skills {
		desc := sk.Description
		if desc == "" {
			desc = truncateStr(sk.Content, 60)
		}
		sb.WriteString(fmt.Sprintf("- %s: %s\n", sk.Name, desc))
	}
}
