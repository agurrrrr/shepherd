/**
 * MAGI live-output panel assembly for MagiStreamPanel.
 *
 * Server contract (task #7209+):
 *   - Each complete line is stored as a separate array entry, often with
 *     a "[MAGI:N]" / "[MAGI:*]" prefix.
 *   - appendLiveOutput strips the trailing '\n' that LineCoalescer emits,
 *     so consecutive [MAGI:N] lines must be re-joined with '\n' here.
 *   - Lines without a prefix are continuations of a multi-line OnOutput
 *     chunk (only the first line carried the prefix).
 *
 * Historical bug: proposerTexts[slot] += text (no separator) collapsed
 * markdown headings, tables, and blank lines into a single wall of text
 * in the per-model cards (task screenshot 2026-07-11).
 */

export function stripAnsi(text) {
	return text.replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
}

/**
 * Parse a raw line into { slot, text }.
 * slot: 0|1|2|'*'|null
 *   - null means "no [MAGI:] prefix" → continuation of previous slot
 */
export function parseMagiLine(raw) {
	const line = stripAnsi(raw ?? '');
	const m = line.match(/^\[MAGI:(\d|\*)\]\s?(.*)$/s);
	if (m) {
		if (m[1] === '*') return { slot: '*', text: m[2] };
		return { slot: parseInt(m[1], 10), text: m[2] };
	}
	return { slot: null, text: line };
}

/** Persona announcement: "🧩 🔮 햄찌" — used for headers, not card body. */
export function isPersonaAnnouncement(text) {
	return /^\s*🧩\s+/.test(text ?? '');
}

/**
 * Append a complete line to a slot accumulator.
 * Always inserts '\n' between successive lines so blank lines and
 * markdown block structure survive (see module header).
 */
export function appendSlotLine(existing, text) {
	if (existing == null || existing === '') {
		return text ?? '';
	}
	return existing + '\n' + (text ?? '');
}

/**
 * Soft-repair mid-line markdown block starters that lost their leading
 * newline inside a single stored line (model/tool narration glue).
 * Inter-line joining is handled by appendSlotLine; this only fixes
 * "합니다.# Title" / "CONFIDENCE: 8본문" style damage within one line.
 *
 * Skips content inside fenced code blocks.
 */
export function softRepairMarkdownNewlines(text) {
	if (text == null || text === '') return text ?? '';
	// Fast path: nothing that looks glued.
	if (
		!/(CONFIDENCE:\s*\d+)\S/.test(text) &&
		!/[^\n#]#{1,6}\s+\S/.test(text) &&
		!/[^\n\-\s|]---/.test(text)
	) {
		return text;
	}

	const parts = String(text).split(/(^```[^\n]*\n[\s\S]*?^```$)/m);
	return parts
		.map((part) => {
			// Leave fenced blocks untouched.
			if (/^```/.test(part)) return part;
			return part
				// "CONFIDENCE: 8설계..." → separate meta line
				.replace(/(CONFIDENCE:\s*\d+)(?=\S)/g, '$1\n\n')
				// "정리합니다.# Title" / "done.## Section"
				.replace(/([^\n#])(#{1,6}\s+\S)/g, '$1\n\n$2')
				// "합니다.---" horizontal rule glued after prose
				.replace(/([^\n\-\s|])(---)(?=\s*(?:\n|$|#{1,6}\s))/g, '$1\n\n$2');
		})
		.join('');
}

/**
 * Assemble per-slot proposer bodies, completion summaries, and unified
 * (MAGI:*) lines from a flat live-output line array.
 *
 * @param {string[]} lines
 * @returns {{
 *   proposerTexts: string[],
 *   proposerSummaries: string[],
 *   unifiedLines: string[],
 *   completed: boolean[],
 * }}
 */
export function assembleMagiPanel(lines) {
	const proposerTexts = ['', '', ''];
	const proposerSummaries = ['', '', ''];
	const unifiedLines = [];
	const completed = [false, false, false];
	let lastSlot = '*';

	const input = Array.isArray(lines) ? lines : [];

	for (const raw of input) {
		const { slot, text } = parseMagiLine(raw);

		// Debate round entry → reset per-slot state so streaming tokens
		// are captured again instead of being dropped by completed[].
		if (slot === '*' && text.includes('토론 라운드 진입')) {
			for (let i = 0; i < 3; i++) {
				proposerTexts[i] = '';
				proposerSummaries[i] = '';
				completed[i] = false;
			}
			unifiedLines.push(text);
			lastSlot = '*';
			continue;
		}

		if (slot === null) {
			// Continuation — append to whichever slot was last active.
			if (lastSlot === '*') {
				if (unifiedLines.length > 0) {
					unifiedLines[unifiedLines.length - 1] = appendSlotLine(
						unifiedLines[unifiedLines.length - 1],
						text
					);
				} else {
					unifiedLines.push(text);
				}
			} else if (lastSlot >= 0 && lastSlot <= 2 && !completed[lastSlot]) {
				proposerTexts[lastSlot] = appendSlotLine(proposerTexts[lastSlot], text);
			}
			continue;
		}

		if (slot === '*') {
			lastSlot = '*';
			unifiedLines.push(text);
			continue;
		}

		if (slot < 0 || slot > 2) {
			continue;
		}

		lastSlot = slot;

		// Persona announcement is parsed separately for headers; never
		// dump it into the card body (would show "🔮 햄찌" above content).
		if (isPersonaAnnouncement(text)) {
			continue;
		}

		if (text.includes('응답 완료') || text.includes('응답 실패')) {
			completed[slot] = true;
			proposerSummaries[slot] = appendSlotLine(
				proposerSummaries[slot],
				text.trim()
			);
			continue;
		}

		if (!completed[slot]) {
			// Complete lines (post-#7209) — re-join with '\n'.
			// Do NOT concatenate without a separator: blank lines and
			// markdown structure would be destroyed.
			proposerTexts[slot] = appendSlotLine(proposerTexts[slot], text);
		}
	}

	// Soft-repair mid-line markdown glue inside each finished body.
	for (let i = 0; i < 3; i++) {
		if (proposerTexts[i]) {
			proposerTexts[i] = softRepairMarkdownNewlines(proposerTexts[i]);
		}
	}

	return { proposerTexts, proposerSummaries, unifiedLines, completed };
}
