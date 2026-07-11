/**
 * Unit tests for MAGI panel line assembly (task #7270+).
 * Run: node --test src/lib/magiPanel.test.js
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
	parseMagiLine,
	isPersonaAnnouncement,
	appendSlotLine,
	softRepairMarkdownNewlines,
	assembleMagiPanel,
} from './magiPanel.js';

describe('parseMagiLine', () => {
	it('parses slot-prefixed lines', () => {
		assert.deepEqual(parseMagiLine('[MAGI:0] hello'), { slot: 0, text: 'hello' });
		assert.deepEqual(parseMagiLine('[MAGI:2] world'), { slot: 2, text: 'world' });
		assert.deepEqual(parseMagiLine('[MAGI:*] 🧠 start'), { slot: '*', text: '🧠 start' });
	});

	it('treats unprefixed lines as continuations', () => {
		assert.deepEqual(parseMagiLine('continuation'), { slot: null, text: 'continuation' });
	});

	it('preserves empty body after prefix (blank line)', () => {
		assert.deepEqual(parseMagiLine('[MAGI:0] '), { slot: 0, text: '' });
		assert.deepEqual(parseMagiLine('[MAGI:1]'), { slot: 1, text: '' });
	});
});

describe('isPersonaAnnouncement', () => {
	it('detects 🧩 announcement lines', () => {
		assert.equal(isPersonaAnnouncement('🧩 🔮 햄찌'), true);
		assert.equal(isPersonaAnnouncement('  🧩 ⚡ CASPER-3'), true);
		assert.equal(isPersonaAnnouncement('## heading'), false);
		assert.equal(isPersonaAnnouncement('응답 완료 — 신뢰도 8/10'), false);
	});
});

describe('appendSlotLine', () => {
	it('joins successive lines with newline', () => {
		assert.equal(appendSlotLine('', 'a'), 'a');
		assert.equal(appendSlotLine('a', 'b'), 'a\nb');
		assert.equal(appendSlotLine('a\nb', ''), 'a\nb\n');
		assert.equal(appendSlotLine('---', '## H'), '---\n## H');
	});
});

describe('softRepairMarkdownNewlines', () => {
	it('splits glued ATIDENCE and mid-line headings', () => {
		const raw =
			'CONFIDENCE: 8설계를 대조합니다.최종 의견을 정리합니다.# Charis 심의\n\n본문';
		const fixed = softRepairMarkdownNewlines(raw);
		assert.ok(fixed.startsWith('CONFIDENCE: 8\n\n'), fixed.slice(0, 40));
		assert.ok(fixed.includes('정리합니다.\n\n# Charis'), fixed);
		assert.equal(fixed.includes('합니다.#'), false);
	});

	it('does not alter headings already on their own line', () => {
		const raw = '# Title\n\n## Section\n\nparagraph';
		assert.equal(softRepairMarkdownNewlines(raw), raw);
	});
});

describe('assembleMagiPanel', () => {
	it('re-joins consecutive [MAGI:N] lines with newlines (not glued)', () => {
		const lines = [
			'[MAGI:0] # Title',
			'[MAGI:0] ',
			'[MAGI:0] - item 1',
			'[MAGI:0] - item 2',
			'[MAGI:0] ',
			'[MAGI:0] ## Section',
		];
		const { proposerTexts, completed } = assembleMagiPanel(lines);
		assert.equal(completed[0], false);
		assert.equal(
			proposerTexts[0],
			'# Title\n\n- item 1\n- item 2\n\n## Section'
		);
	});

	it('does not collapse blank lines between markdown blocks', () => {
		// Real shape from task #7265 stored output after appendLiveOutput.
		const lines = [
			'[MAGI:0] ---',
			'[MAGI:0] ',
			'[MAGI:0] # Charis 설계 심의',
			'[MAGI:0] ',
			'[MAGI:0] | 축 | 판정 |',
			'[MAGI:0] |----|------|',
			'[MAGI:0] | 가설 | 강함 |',
		];
		const { proposerTexts } = assembleMagiPanel(lines);
		const body = proposerTexts[0];
		assert.ok(body.includes('---\n\n# Charis'), 'heading after blank line');
		assert.ok(body.includes('| 축 | 판정 |\n|----|------|\n| 가설 | 강함 |'));
		// Wall-of-text regression: no glued "---# Charis"
		assert.equal(body.includes('---#'), false);
	});

	it('excludes persona announcement from card body', () => {
		const lines = [
			'[MAGI:0] 🧩 🔮 햄찌',
			'[MAGI:0] 본문 시작',
		];
		const { proposerTexts } = assembleMagiPanel(lines);
		assert.equal(proposerTexts[0], '본문 시작');
		assert.equal(proposerTexts[0].includes('🧩'), false);
	});

	it('routes completion lines to summary and freezes body', () => {
		const lines = [
			'[MAGI:1] answer line',
			'[MAGI:1]   🔬 BALTHASAR-2 응답 완료 — 신뢰도 8/10 (74초)',
			'[MAGI:1] should-not-append',
		];
		const { proposerTexts, proposerSummaries, completed } = assembleMagiPanel(lines);
		assert.equal(completed[1], true);
		assert.equal(proposerTexts[1], 'answer line');
		assert.ok(proposerSummaries[1].includes('응답 완료'));
		assert.equal(proposerTexts[1].includes('should-not-append'), false);
	});

	it('keeps slots independent', () => {
		const lines = [
			'[MAGI:0] from-0',
			'[MAGI:1] from-1',
			'[MAGI:2] from-2',
			'[MAGI:0] more-0',
		];
		const { proposerTexts } = assembleMagiPanel(lines);
		assert.equal(proposerTexts[0], 'from-0\nmore-0');
		assert.equal(proposerTexts[1], 'from-1');
		assert.equal(proposerTexts[2], 'from-2');
	});

	it('appends unprefixed continuations to last slot with newline', () => {
		const lines = [
			'[MAGI:0] first',
			'second continuation',
			'third',
		];
		const { proposerTexts } = assembleMagiPanel(lines);
		assert.equal(proposerTexts[0], 'first\nsecond continuation\nthird');
	});

	it('collects unified MAGI:* lines separately', () => {
		const lines = [
			'[MAGI:*] 🧠 MAGI 심의 개시',
			'[MAGI:0] proposer body',
			'[MAGI:*] ✅ 합의 도달',
		];
		const { proposerTexts, unifiedLines } = assembleMagiPanel(lines);
		assert.equal(proposerTexts[0], 'proposer body');
		assert.deepEqual(unifiedLines, ['🧠 MAGI 심의 개시', '✅ 합의 도달']);
	});

	it('resets slot state on debate round entry', () => {
		const lines = [
			'[MAGI:0] round1 answer',
			'[MAGI:0] 응답 완료 — 신뢰도 7/10',
			'[MAGI:*] ⚖️ 합의 판정: split — 토론 라운드 진입',
			'[MAGI:0] debate answer',
		];
		const { proposerTexts, completed, unifiedLines } = assembleMagiPanel(lines);
		assert.equal(completed[0], false);
		assert.equal(proposerTexts[0], 'debate answer');
		assert.ok(unifiedLines.some((t) => t.includes('토론 라운드 진입')));
	});

	it('handles empty input', () => {
		const r = assembleMagiPanel([]);
		assert.deepEqual(r.proposerTexts, ['', '', '']);
		assert.deepEqual(r.completed, [false, false, false]);
		assert.deepEqual(r.unifiedLines, []);
	});
});
