import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
	parseSubagentLine,
	appendSlotLine,
	assembleSubagentPanel
} from './subagentPanel.js';

describe('parseSubagentLine', () => {
	it('parses named and star prefixes', () => {
		assert.deepEqual(parseSubagentLine('[SUB:logo-analysis] hello'), {
			slot: 'logo-analysis',
			text: 'hello'
		});
		assert.deepEqual(parseSubagentLine('[SUB:*] 2개 시작'), {
			slot: '*',
			text: '2개 시작'
		});
	});

	it('returns null slot for unprefixed lines', () => {
		assert.deepEqual(parseSubagentLine('parent narration'), {
			slot: null,
			text: 'parent narration'
		});
	});
});

describe('assembleSubagentPanel', () => {
	it('keeps per-agent tools/results separated when all lines are prefixed', () => {
		const { slots, general, generalLines } = assembleSubagentPanel([
			'[SUB:*] 2개 서브에이전트 시작\n',
			'[SUB:logo-analysis] 시작 — find logo\n',
			'[SUB:redirect-analysis] 시작 — find redirect\n',
			'[SUB:logo-analysis] 🔧 glob → **/*logo*\n',
			'[SUB:redirect-analysis] 💭 redirect thinking\n',
			'[SUB:logo-analysis]   No files found\n',
			'[SUB:redirect-analysis] 🔧 get_history\n',
			'[SUB:redirect-analysis]   history ok\n',
			'[SUB:logo-analysis] ✅ 완료 — 10 chars\n',
			'[SUB:redirect-analysis] ✅ 완료 — 20 chars\n',
			'[SUB:*] 2개 서브에이전트 완료\n'
		]);

		assert.equal(slots.length, 2);
		const logo = slots.find((s) => s.name === 'logo-analysis');
		const redir = slots.find((s) => s.name === 'redirect-analysis');
		assert.ok(logo);
		assert.ok(redir);

		assert.match(logo.text, /glob → \*\*\/\*logo\*/);
		assert.match(logo.text, /No files found/);
		assert.doesNotMatch(logo.text, /get_history|redirect thinking/);
		assert.equal(logo.status, 'done');
		// lines[] is what OutputViewer consumes inside SubagentStreamPanel.
		assert.ok(logo.lines.some((l) => l.includes('glob')));
		assert.ok(logo.lines.some((l) => l.includes('No files found')));
		assert.ok(!logo.lines.some((l) => l.includes('get_history')));

		assert.match(redir.text, /redirect thinking/);
		assert.match(redir.text, /get_history/);
		assert.match(redir.text, /history ok/);
		assert.doesNotMatch(redir.text, /glob|No files found/);
		assert.equal(redir.status, 'done');

		assert.match(general, /2개 서브에이전트 시작/);
		assert.match(general, /2개 서브에이전트 완료/);
		assert.equal(generalLines.length, 2);
		assert.ok(generalLines[0].includes('시작'));
		assert.ok(generalLines[1].includes('완료'));
	});

	it('routes unprefixed lines to general (parent), not lastSlot', () => {
		// Pre-#7564 bug: after redirect "시작", bare logo tool lines stuck
		// to redirect because lastSlot was the last prefixed agent.
		const { slots, general } = assembleSubagentPanel([
			'[SUB:logo-analysis] 시작 — logo\n',
			'[SUB:redirect-analysis] 시작 — redirect\n',
			'🔧 glob → **/*logo*\n',
			'  No files found\n',
			'[SUB:redirect-analysis] 💭 only mine\n'
		]);

		const logo = slots.find((s) => s.name === 'logo-analysis');
		const redir = slots.find((s) => s.name === 'redirect-analysis');
		assert.ok(logo);
		assert.ok(redir);

		// Bare body must NOT land on redirect (or logo) cards.
		assert.doesNotMatch(redir.text, /glob|No files found/);
		assert.doesNotMatch(logo.text, /glob|No files found/);
		assert.match(redir.text, /only mine/);
		assert.match(general, /glob → \*\*\/\*logo\*/);
		assert.match(general, /No files found/);
	});

	it('still accepts prefixed body after done marker (no drop)', () => {
		const { slots } = assembleSubagentPanel([
			'[SUB:a] 시작\n',
			'[SUB:a] ✅ 완료 — 1 chars\n',
			'[SUB:a] late residual line\n'
		]);
		assert.equal(slots.length, 1);
		assert.equal(slots[0].status, 'done');
		assert.match(slots[0].text, /late residual line/);
	});

	it('appendSlotLine preserves blank lines between rows', () => {
		assert.equal(appendSlotLine('a', 'b'), 'a\nb');
		assert.equal(appendSlotLine('a', ''), 'a\n');
	});
});
