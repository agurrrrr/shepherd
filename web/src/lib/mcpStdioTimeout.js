// External MCP stdio-timeout plumbing for the `mcp.stdio_timeout_seconds`
// setting (embedded.yaml `mcp:` section).
//
// The daemon owns how long one external MCP server may take to answer a single
// JSON-RPC request (initialize, tools/list, tools/call). It stores the value in
// seconds and enforces it in internal/mcp readResponse. It used to be a
// hard-coded 10 seconds, which killed slow legitimate calls (an npx cold start,
// browser automation, a large tool result) with "read response timeout".
//
// The browser only needs the number to render the Settings field and its hint.
// MCP_STDIO_TIMEOUT_CEIL_SECONDS mirrors config.MCPStdioTimeoutHardCeilSeconds
// and MCP_STDIO_TIMEOUT_DEFAULT_SECONDS mirrors
// config.DefaultMCPStdioTimeoutSeconds. The daemon rejects an out-of-range or
// unparseable value with 400; the form sends a trimmed string (seconds or a
// duration like "5m") and the daemon normalizes it.

/** Hard ceiling for `stdio_timeout_seconds`, in seconds (config.MCPStdioTimeoutHardCeilSeconds). */
export const MCP_STDIO_TIMEOUT_CEIL_SECONDS = 3600;

/** Fallback for `stdio_timeout_seconds`, in seconds (config.DefaultMCPStdioTimeoutSeconds). */
export const MCP_STDIO_TIMEOUT_DEFAULT_SECONDS = 600;
