/**
 * Append a streaming output chunk into the live-output line array.
 *
 * Providers like Grok emit per-token deltas. Each OnOutput call used to become
 * its own "line", so BPE fragments ("screens" + "hots") rendered as mid-word
 * breaks and markdown re-rendered on every token (task #7201/#7202).
 *
 * Convention: a chunk that does NOT end with `\n` leaves the last line "open".
 * The next chunk is concatenated onto that line instead of starting a new one.
 *
 * @param {string[]} lines - current live output lines
 * @param {string} text - raw SSE/output chunk (may contain embedded newlines)
 * @param {{ open: boolean }} state - mutable flag; true when last line is incomplete
 * @param {number} [maxLines=5000]
 * @returns {string[]}
 */
export function appendLiveOutput(lines, text, state, maxLines = 5000) {
	if (text == null || text === '') {
		return lines;
	}

	let combined;
	let base;
	if (state.open && lines.length > 0) {
		combined = lines[lines.length - 1] + text;
		base = lines.slice(0, -1);
	} else {
		combined = text;
		base = lines.slice();
	}

	const endsWithNL = combined.endsWith('\n');
	const parts = combined.split('\n');
	if (endsWithNL) {
		parts.pop(); // drop trailing empty from split
		state.open = false;
	} else {
		state.open = true;
	}

	let next;
	if (state.open) {
		// Last part is the incomplete line still being written.
		const incomplete = parts.pop() ?? '';
		next = base.concat(parts, incomplete);
	} else {
		next = base.concat(parts);
	}

	if (next.length > maxLines) {
		return next.slice(-maxLines);
	}
	return next;
}
