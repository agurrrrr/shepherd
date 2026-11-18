package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/mcp"
)

// externalMCPServerStatus describes one external MCP server that is enabled
// for the project (global switch on AND project setting on). The embedded
// system prompt lists every such server by name, so the model knows which
// servers are connected to its session. Before this, the prompt had only a
// flat "[Available Shepherd MCP Tools]" list: external tools had no server
// name, and tools named get_* were filed under Shepherd's task management.
// Models could call the tools but told the user that the server (for example
// atsel or dtc) "is not registered in this session".
type externalMCPServerStatus struct {
	Name        string
	Label       string
	Description string
	// Tools holds the tool names the server offers, in tools/list order.
	Tools []string
	// Err is set when the server failed to start or to list its tools.
	Err string
}

// externalMCPSet is the set of external MCP servers connected for one run.
type externalMCPSet struct {
	defs      []embedded.MCPToolDef
	servers   map[string]*mcp.ExternalMCPServer
	toolOwner map[string]string // tool name -> server name
	status    []externalMCPServerStatus
}

// connectProjectExternalMCP starts (or reuses) every external MCP server that
// is enabled for projectName and collects its tool definitions. A server that
// fails to start is recorded in status with its error and does not fail the
// run. builtin holds the tool definitions already in the session; an external
// tool with one of those names is skipped, because duplicate function names
// make OpenAI-style APIs (GitHub Copilot) reject the request. logPrefix tags
// the daemon log lines (for example "[embedded]").
func connectProjectExternalMCP(projectName string, builtin []embedded.MCPToolDef, logPrefix string) externalMCPSet {
	set := externalMCPSet{
		servers:   map[string]*mcp.ExternalMCPServer{},
		toolOwner: map[string]string{},
	}
	if projectName == "" {
		return set
	}
	activeServers, err := getProjectActiveMCPServers(projectName)
	if err != nil {
		fmt.Printf("%s warning: cannot read MCP settings of project %q: %v\n", logPrefix, projectName, err)
		return set
	}
	for _, d := range builtin {
		set.toolOwner[d.Name] = "shepherd (built-in)"
	}
	set.addServers(activeServers, mcp.GetMCPManager().GetOrCreate, logPrefix)
	for _, d := range builtin {
		delete(set.toolOwner, d.Name)
	}
	return set
}

// addServers connects each server with connect and records the result. It is
// separate from connectProjectExternalMCP so tests can use a fake connector.
func (set *externalMCPSet) addServers(servers []*ent.MCPServer, connect func(*ent.MCPServer) (*mcp.ExternalMCPServer, error), logPrefix string) {
	for _, srv := range servers {
		st := externalMCPServerStatus{Name: srv.Name, Label: srv.Label, Description: srv.Description}
		ext, err := connect(srv)
		if err != nil {
			// Log but don't fail the whole task — some servers may not be installed.
			fmt.Printf("%s warning: failed to spawn MCP server %q: %v\n", logPrefix, srv.Name, err)
			st.Err = err.Error()
			set.status = append(set.status, st)
			continue
		}
		set.servers[srv.Name] = ext
		for _, t := range ext.Tools() {
			if owner, dup := set.toolOwner[t.Name]; dup {
				fmt.Printf("%s warning: MCP tool %q from %q is hidden by the same name from %q\n", logPrefix, t.Name, srv.Name, owner)
				continue
			}
			set.defs = append(set.defs, toEmbeddedMCPDef(t))
			set.toolOwner[t.Name] = srv.Name
			st.Tools = append(st.Tools, t.Name)
		}
		set.status = append(set.status, st)
	}
}

// owns reports whether name is a tool of a connected external server.
func (set *externalMCPSet) owns(name string) bool {
	_, ok := set.toolOwner[name]
	return ok
}

// call runs an external tool on the server that offers it. The multimodal
// call keeps image results (e.g. mobile_take_screenshot) for the vision model
// instead of dropping them (task #6684).
func (set *externalMCPSet) call(name string, args map[string]interface{}) (string, []embedded.MCPImage, error) {
	owner, ok := set.toolOwner[name]
	if !ok {
		return "", nil, fmt.Errorf("external tool %q not found", name)
	}
	ext, ok := set.servers[owner]
	if !ok {
		return "", nil, fmt.Errorf("external MCP server %q for tool %q is not connected", owner, name)
	}
	text, imgs, err := ext.CallToolMultimodal(name, args)
	return text, toEmbeddedMCPImages(imgs), err
}

// summaryLine returns one task-output line that shows which external MCP
// servers are connected to the session, or "" when the project enables none.
// It lets the user see in the task log whether the servers that are ON in
// the project settings reached the session.
func (set *externalMCPSet) summaryLine() string {
	if len(set.status) == 0 {
		return ""
	}
	parts := make([]string, 0, len(set.status))
	for _, st := range set.status {
		if st.Err != "" {
			parts = append(parts, fmt.Sprintf("%s (failed: %s)", st.Name, oneLine(st.Err, 200)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%d tools)", st.Name, len(st.Tools)))
	}
	return "[MCP] external servers for this session: " + strings.Join(parts, ", ")
}

// oneLine collapses whitespace and caps s at max runes.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// buildMCPGuide constructs the MCP section of the embedded system prompt. It
// lists the built-in Shepherd tools by category and then every external MCP
// server enabled for the project, with its name and tools. Only tools present
// in defs are listed, so a filtered (read-only) tool set gets a matching guide.
func buildMCPGuide(defs []embedded.MCPToolDef, projectName string, external []externalMCPServerStatus) string {
	externalNames := map[string]bool{}
	for _, st := range external {
		for _, n := range st.Tools {
			externalNames[n] = true
		}
	}

	var sb strings.Builder
	sb.WriteString("[Available Shepherd MCP Tools]\n\n")

	categoryOrder := []string{"Task management", "Skills", "Wiki", "Browser automation", "Other"}
	categories := map[string][]embedded.MCPToolDef{}
	visible := map[string]embedded.MCPToolDef{}
	for _, d := range defs {
		if externalNames[d.Name] {
			visible[d.Name] = d
			continue
		}
		switch {
		case strings.HasPrefix(d.Name, "task_") || strings.HasPrefix(d.Name, "get_"):
			categories["Task management"] = append(categories["Task management"], d)
		case d.Name == "skill_load":
			categories["Skills"] = append(categories["Skills"], d)
		case strings.HasPrefix(d.Name, "wiki_"):
			categories["Wiki"] = append(categories["Wiki"], d)
		case strings.HasPrefix(d.Name, "browser_"):
			categories["Browser automation"] = append(categories["Browser automation"], d)
		default:
			categories["Other"] = append(categories["Other"], d)
		}
	}
	for _, cat := range categoryOrder {
		tools := categories[cat]
		if len(tools) == 0 {
			continue
		}
		sb.WriteString(cat + ":\n")
		for _, t := range tools {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
		}
		sb.WriteString("\n")
	}

	if len(external) > 0 {
		writeExternalMCPGuide(&sb, external, visible)
	}

	if projectName != "" {
		sb.WriteString(fmt.Sprintf("Project: %s\n", projectName))
	}
	return sb.String()
}

// writeExternalMCPGuide writes the "[Connected External MCP Servers]" part of
// the guide. visible holds the external tool definitions of this run.
func writeExternalMCPGuide(sb *strings.Builder, external []externalMCPServerStatus, visible map[string]embedded.MCPToolDef) {
	sb.WriteString("[Connected External MCP Servers]\n")
	sb.WriteString("These MCP servers are turned ON in this project's settings and are registered in this session. " +
		"Their tools are in your tool list. Call a tool directly by the tool name shown below " +
		"(no server prefix such as mcp__<server>__). " +
		"When the user names one of these servers, use its tools; do not say it is not registered.\n\n")

	sorted := append([]externalMCPServerStatus(nil), external...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, st := range sorted {
		title := "MCP server \"" + st.Name + "\""
		if st.Label != "" && st.Label != st.Name {
			title += " (" + st.Label + ")"
		}
		if st.Description != "" {
			title += " — " + oneLine(st.Description, 200)
		}
		if st.Err != "" {
			sb.WriteString(title + ":\n")
			sb.WriteString("- ON in the project settings, but it failed to connect: " + oneLine(st.Err, 300) + "\n")
			sb.WriteString("- Its tools are not available in this session. Tell the user about this error if they ask for this server.\n\n")
			continue
		}
		var lines []string
		for _, n := range st.Tools {
			d, ok := visible[n]
			if !ok {
				continue // filtered out of this run (e.g. read-only MAGI/sub-agent)
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", d.Name, oneLine(d.Description, 300)))
		}
		sb.WriteString(title + ":\n")
		if len(lines) == 0 {
			sb.WriteString("- No tools of this server are available in this context.\n\n")
			continue
		}
		sb.WriteString(strings.Join(lines, "\n") + "\n\n")
	}
}

// toEmbeddedMCPDef converts an mcp.Tool to embedded.MCPToolDef.
func toEmbeddedMCPDef(t mcp.Tool) embedded.MCPToolDef {
	properties := make(map[string]interface{})
	for k, v := range t.InputSchema.Properties {
		prop := map[string]interface{}{
			"description": v.Description,
		}
		// An empty type is omitted: "type": "" is not valid JSON Schema.
		if v.Type != "" {
			prop["type"] = v.Type
		}
		if len(v.Enum) > 0 {
			prop["enum"] = v.Enum
		}
		// OpenAI-style validators (GitHub Copilot) reject an array schema
		// without items, so always send one.
		if v.Type == "array" {
			var items interface{}
			if len(v.Items) > 0 && json.Unmarshal(v.Items, &items) == nil && items != nil {
				prop["items"] = items
			} else {
				prop["items"] = map[string]interface{}{}
			}
		}
		properties[k] = prop
	}
	// JSON Schema requires `required` to be an array. A nil slice marshals to
	// `null`, which strict tool-template parsers (e.g. llama.cpp) reject with a
	// 400 ("type must be array, but is null"), aborting the whole request before
	// any inference runs. Tools with no required params (e.g. get_status) hit
	// this, so always emit an empty array instead of null.
	required := t.InputSchema.Required
	if required == nil {
		required = []string{}
	}
	schemaType := t.InputSchema.Type
	if schemaType == "" {
		schemaType = "object"
	}
	return embedded.MCPToolDef{
		Name:        t.Name,
		Description: t.Description,
		Parameters: map[string]interface{}{
			"type":       schemaType,
			"properties": properties,
			"required":   required,
		},
	}
}
