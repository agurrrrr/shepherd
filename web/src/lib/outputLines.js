/**
 * Live-output line classification for OutputViewer.
 *
 * Stream producers (embedded / claude / grok) emit discrete events as lines.
 * Historical rows may glue events mid-line when trailing '\n' was missing;
 * expandGluedLines repairs those without mangling legitimate markdown
 * (checklists with ✅, list markers, fenced code examples of glue).
 */

export function stripAnsi(text) {
	return text.replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
}

// True stream-protocol markers that may be glued mid-line after a missing \n.
// Intentionally excludes ✅ / 🚀 / 📋 — agents use those in normal markdown
// (checklists, todo labels). Splitting on them turns "1. ✅ item" into a bare
// "1." text line + a status pill (task #7268 / screenshot on #7266).
const MID_LINE_MARKERS = /(?<=\S)(?=🔧 |💭 |⚠️ |⏳ )/u;

// Pure fence marker line: ``` or ```lang (optional trailing whitespace).
const FENCE_LINE = /^\s*```[^\n`]*\s*$/;

/**
 * Split glued mid-line protocol markers. Skips content inside markdown
 * fenced code blocks so examples that literally contain "🔧 " stay intact.
 */
export function expandGluedLines(lines) {
	const out = [];
	let inFence = false;

	for (const raw of lines) {
		if (raw == null || raw === '') {
			out.push(raw);
			continue;
		}

		const stripped = stripAnsi(raw);

		// Fence toggles must be pure marker lines so we don't treat
		// "use ``` for code" prose as a fence.
		if (FENCE_LINE.test(stripped)) {
			out.push(raw);
			inFence = !inFence;
			continue;
		}

		if (inFence) {
			// Never split inside a fence — keeps glue demos renderable.
			out.push(raw);
			continue;
		}

		const pieces = raw.split(MID_LINE_MARKERS);
		if (pieces.length === 1) {
			out.push(raw);
			continue;
		}
		for (const piece of pieces) {
			if (piece === '') continue;
			out.push(piece);
		}
	}
	return out;
}

// System status lines that legitimately start with ✅. Agent checklist lines
// like "✅ TTS 캐시" are ordinary text and must stay in the markdown stream.
const STATUS_OK = /^✅ (Task complete|Tool complete|Task #\d+ completed|Done|Remembered:|합의 도달)/;

/**
 * Classify a single output line.
 * @param {string} raw
 * @param {string|null} prevType
 * @returns {string}
 */
export function classifyLine(raw, prevType) {
	const line = stripAnsi(raw);
	if (/^[🟠🟢🔵⚪]\s/.test(line)) return 'sheep';
	if (line.startsWith('🚀 ')) return 'status';
	if (STATUS_OK.test(line) || line.startsWith('✅ Task ') || line.startsWith('✅ Tool ')) {
		return 'status';
	}
	// Bare "✅ …" from agents is markdown text, not a status chip.
	if (line.startsWith('⏸')) return 'status';
	if (line.startsWith('🔧 ')) return 'tool';
	if (line.startsWith('❓')) return 'question';
	if (line.startsWith('  ▸ ')) return 'question-option';
	// Reasoning / thinking stream (Grok thought, OpenCode reasoning, pi,
	// embedded reasoning_content). Marker is always "💭 " at the start of a
	// chunk; multi-line bodies use a 3-space indent on continuations
	// (worker convention: ReplaceAll("\n", "\n   ")).
	if (line.startsWith('💭 ') || line === '💭') return 'thinking';
	if (prevType === 'thinking' && /^\s{3}\S/.test(line)) return 'thinking';
	// Only classify as 'result' when preceded by a tool call or another
	// result line. Without this context check, indented markdown lines
	// (sub-lists, blockquotes, etc.) are misclassified as tool output.
	if (/^\s{2}/.test(line) && (prevType === 'tool' || prevType === 'result')) return 'result';
	return 'text';
}

/**
 * Body text of a thinking line: strip the 💭 marker and the 3-space
 * continuation indent used by workers for multi-line reasoning.
 * @param {string} text
 * @returns {string}
 */
export function thinkingBody(text) {
	let t = text;
	if (t.startsWith('💭 ')) t = t.slice(2).replace(/^\s/, '');
	else if (t === '💭') t = '';
	else if (/^\s{3}/.test(t)) t = t.slice(3);
	return t;
}

/**
 * Group classified lines into render blocks for OutputViewer.
 * @param {string[]} lines
 * @returns {object[]}
 */
export function groupLines(lines) {
	const blocks = [];
	let currentResult = null;
	let currentText = null;
	let currentThinking = null;
	let currentQuestion = null;
	let inFence = false;

	function flushAll() {
		if (currentThinking) {
			blocks.push(currentThinking);
			currentThinking = null;
		}
		if (currentText) {
			blocks.push(currentText);
			currentText = null;
		}
		if (currentResult) {
			blocks.push(currentResult);
			currentResult = null;
		}
		if (currentQuestion) {
			blocks.push(currentQuestion);
			currentQuestion = null;
		}
	}

	let prevType = null;
	for (const raw of expandGluedLines(lines)) {
		// DB-persisted lines keep a trailing '\n'; SSE path (appendLiveOutput)
		// strips it. Normalize so text-block joins don't invent blank lines
		// that break markdown lists.
		const text = stripAnsi(raw).replace(/\n$/, '');
		const isFence = FENCE_LINE.test(text);

		// Inside a fenced code block everything is markdown text — even lines
		// that look like tool headers or indented results (doc examples).
		let type;
		if (inFence && !isFence) {
			type = 'text';
		} else {
			type = classifyLine(raw, prevType);
		}

		if (isFence) {
			// Fence markers themselves are always text.
			type = 'text';
			inFence = !inFence;
		}

		if (type === 'question') {
			if (currentThinking) {
				blocks.push(currentThinking);
				currentThinking = null;
			}
			if (currentText) {
				blocks.push(currentText);
				currentText = null;
			}
			if (currentResult) {
				blocks.push(currentResult);
				currentResult = null;
			}
			if (currentQuestion) {
				blocks.push(currentQuestion);
			}
			const hm = text.match(/^❓\s+\[(.+?)\]\s+(.+)/);
			currentQuestion = {
				type: 'question',
				header: hm ? hm[1] : '',
				question: hm ? hm[2] : text.replace(/^❓\s*/, ''),
				options: []
			};
		} else if (type === 'question-option' && currentQuestion) {
			const om = text.match(/^\s+▸\s+(.+?)\s+—\s+(.+)/);
			if (om) {
				currentQuestion.options.push({ label: om[1], description: om[2] });
			} else {
				currentQuestion.options.push({
					label: text.replace(/^\s+▸\s+/, ''),
					description: ''
				});
			}
		} else if (type === 'result') {
			if (currentThinking) {
				blocks.push(currentThinking);
				currentThinking = null;
			}
			if (currentQuestion) {
				blocks.push(currentQuestion);
				currentQuestion = null;
			}
			if (currentText) {
				blocks.push(currentText);
				currentText = null;
			}
			if (currentResult) {
				currentResult.lines.push(text);
			} else {
				currentResult = { type: 'result', lines: [text] };
			}
		} else if (type === 'thinking') {
			if (currentQuestion) {
				blocks.push(currentQuestion);
				currentQuestion = null;
			}
			if (currentResult) {
				blocks.push(currentResult);
				currentResult = null;
			}
			if (currentText) {
				blocks.push(currentText);
				currentText = null;
			}
			const body = thinkingBody(text);
			if (currentThinking) {
				// Adjacent 💭 chunks (Grok safety-flush re-tags) merge into one
				// collapsible block so the UI is not a stack of tiny cards.
				if (body !== '') {
					currentThinking.text += (currentThinking.text ? '\n' : '') + body;
				}
			} else {
				currentThinking = { type: 'thinking', text: body };
			}
		} else if (type === 'text') {
			if (currentThinking) {
				blocks.push(currentThinking);
				currentThinking = null;
			}
			if (currentQuestion) {
				blocks.push(currentQuestion);
				currentQuestion = null;
			}
			if (currentResult) {
				blocks.push(currentResult);
				currentResult = null;
			}
			if (currentText) {
				currentText.text += '\n' + text;
			} else {
				currentText = { type: 'text', text };
			}
		} else {
			flushAll();
			blocks.push({ type, text });
		}
		prevType = type;
	}
	flushAll();
	return blocks;
}
