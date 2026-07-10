/**
 * Append a streaming output chunk into the live-output line array.
 *
 * Line contract (task #7209): the server-side LineCoalescer (in
 * internal/worker/interactive.go) is the single source of truth for line
 * boundaries. It buffers incomplete chunks and emits only complete lines
 * (ending with '\n'). The frontend no longer merges chunks across SSE
 * events — each event is already a coalesced line.
 *
 * The state.open flag is kept for backward compatibility with UI components
 * that check it, but it no longer causes cross-event line merging.
 *
 * @param {string[]} lines - current live output lines
 * @param {string} text - SSE/output chunk (already coalesced by server)
 * @param {{ open: boolean }} state - mutable flag; true when last line is incomplete
 * @param {number} [maxLines=5000]
 * @returns {string[]}
 */
export function appendLiveOutput(lines, text, state, maxLines = 5000) {
	if (text == null || text === '') {
		return lines;
	}

	// The server coalescer emits complete lines (ending with '\n') and
	// at most one trailing open line (from Flush, without '\n').
	// Split on '\n' to handle multi-line events.
	const endsWithNL = text.endsWith('\n');
	const parts = text.split('\n');
	if (endsWithNL) {
		parts.pop(); // drop trailing empty from split
		state.open = false;
	} else {
		state.open = true;
	}

	const next = lines.concat(parts);

	if (next.length > maxLines) {
		return next.slice(-maxLines);
	}
	return next;
}
