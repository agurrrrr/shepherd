/**
 * Subagent live-output panel assembly for SubagentStreamPanel.
 *
 * Server contract (step-04/05):
 *   - Each complete line is stored as a separate array entry, often with
 *     a "[SUB:name]" / "[SUB:*]" prefix.
 *   - Same LineCoalescer contract as [MAGI:N] (task #7209).
 *   - Lines without a prefix are continuations of a multi-line OnOutput
 *     chunk (only the first line carried the prefix).
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
 *   - null means "no [SUB:] prefix" → continuation of previous slot
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
 *   slots: Array<{ name: string, text: string, status: string }>,
 *   general: string,
 * }}
 */
export function assembleSubagentPanel(lines) {
	/** @type {Map<string, { name: string, text: string, status: string }>} */
	const slots = new Map();
	let general = '';
	let lastSlot = null;

	const input = Array.isArray(lines) ? lines : [];

	for (const raw of input) {
		const { slot, text } = parseSubagentLine(raw);

		if (slot === null) {
			// Continuation — append to whichever slot was last active.
			if (lastSlot === null || lastSlot === '*') {
				general = appendSlotLine(general, text);
			} else {
				const s = slots.get(lastSlot);
				if (s && s.status === 'running') {
					s.text = appendSlotLine(s.text, text);
				}
			}
			continue;
		}

		if (slot === '*') {
			lastSlot = '*';
			general = appendSlotLine(general, text);
			continue;
		}

		// Per-agent line
		lastSlot = slot;
		if (!slots.has(slot)) {
			slots.set(slot, { name: slot, text: '', status: 'running' });
		}
		const s = slots.get(slot);
		s.text = appendSlotLine(s.text, text);

		// Detect status from content markers
		if (text.includes('✅')) {
			s.status = 'done';
		} else if (text.includes('❌')) {
			s.status = 'error';
		}
	}

	return { slots: Array.from(slots.values()), general };
}
