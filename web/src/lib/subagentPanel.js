/**
 * Subagent live-output panel assembly for SubagentStreamPanel.
 *
 * Server contract (step-04/05, hardened #7562/#7564):
 *   - Each complete line is stored as a separate array entry, often with
 *     a "[SUB:name]" / "[SUB:*]" prefix.
 *   - Same LineCoalescer contract as [MAGI:N] (task #7209).
 *   - Body stream lines from sub-agents are ALL prefixed per-agent via
 *     wrapSubagentOnOutput (server.go). Lifecycle lines also carry prefix.
 *   - Lines without a prefix are treated as parent/general stream — not as
 *     continuation of the last sub-agent slot (P1 defense for partial deploy
 *     and parent narration interleaved with spawn_subagents).
 *
 * Unlike magiPanel.js which assumes 3 fixed numeric slots (proposerTexts[0..2]),
 * this parser handles a variable number of named agents (1~4).
 */

export function stripAnsi(text) {
	return text.replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
}

/**
 * Normalize a stored/streamed line for assembly.
 * Strips ANSI and a single trailing newline (DB path keeps it; SSE path
 * already strips it via appendLiveOutput).
 */
export function normalizeSubagentLine(raw) {
	return stripAnsi(raw ?? '').replace(/\r?\n$/, '');
}

/**
 * Parse a raw line into { slot, text }.
 * slot: string | '*' | null
 *   - null means "no [SUB:] prefix" → parent/general stream (not lastSlot)
 */
export function parseSubagentLine(raw) {
	const line = normalizeSubagentLine(raw);
	const m = line.match(/^\[SUB:([^\]]+)\]\s?(.*)$/s);
	if (m) {
		return { slot: m[1], text: m[2] };
	}
	return { slot: null, text: line };
}

/**
 * Append a complete line to an accumulator string.
 * Always inserts a single '\n' between successive lines so blank lines and
 * markdown block structure survive (same fix as magiPanel.js #7270).
 */
export function appendSlotLine(existing, text) {
	const t = (text ?? '').replace(/\r?\n$/, '');
	if (existing == null || existing === '') {
		return t;
	}
	return existing + '\n' + t;
}

/**
 * Assemble per-agent slot bodies and general ([SUB:*] / non-prefixed)
 * messages from a flat live-output line array.
 *
 * This is a pure function — each call creates fresh state, so there is no
 * accumulation bug (#7465 Critical #2 does not apply). The O(n²) re-parse
 * cost on each SSE chunk is acceptable for Phase 1 (max 4 agents).
 *
 * @param {string[]} lines
 * @returns {{
 *   slots: Array<{ name: string, text: string, lines: string[], status: string }>,
 *   general: string,
 *   generalLines: string[],
 * }}
 */
export function assembleSubagentPanel(lines) {
	/** @type {Map<string, { name: string, text: string, lines: string[], status: string }>} */
	const slots = new Map();
	let general = '';
	/** @type {string[]} */
	const generalLines = [];

	const input = Array.isArray(lines) ? lines : [];

	for (const raw of input) {
		const { slot, text } = parseSubagentLine(raw);

		if (slot === null) {
			// Unprefixed = parent/general stream (task #7564 P1).
			// Do NOT attach to a "last active" sub-agent slot — parallel bare
			// streams used to collapse every agent into one card.
			general = appendSlotLine(general, text);
			generalLines.push(text);
			continue;
		}

		if (slot === '*') {
			general = appendSlotLine(general, text);
			generalLines.push(text);
			continue;
		}

		// Per-agent line
		if (!slots.has(slot)) {
			slots.set(slot, { name: slot, text: '', lines: [], status: 'running' });
		}
		const s = slots.get(slot);
		// Prefixed lines are always accepted — including after ✅/❌ —
		// so late body chunks are not dropped when lifecycle "완료" races
		// ahead of residual Flush (#7564 P2 partial: drop only unprefixed).
		s.text = appendSlotLine(s.text, text);
		s.lines.push(text);

		// Detect status from content markers
		if (text.includes('✅')) {
			s.status = 'done';
		} else if (text.includes('❌')) {
			s.status = 'error';
		}
	}

	return { slots: Array.from(slots.values()), general, generalLines };
}
