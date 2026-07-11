/**
 * Node built-in test runner: node --test web/src/lib/outputLines.test.js
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { expandGluedLines, classifyLine, groupLines } from './outputLines.js';

describe('expandGluedLines', () => {
	it('does not split checklist items with ✅', () => {
		const lines = [
			'- ✅ TTS 캐시 K8s 볼륨 (M2-4)\n',
			'1. ✅ 인플레 자동 축소: 문서 명시\n'
		];
		const out = expandGluedLines(lines);
		assert.equal(out.length, 2);
		assert.equal(out[0], lines[0]);
		assert.equal(out[1], lines[1]);
	});

	it('splits mid-line tool glue after missing newline', () => {
		const line = '수정하겠습니다.🔧 bash → git diff\n';
		const out = expandGluedLines([line]);
		assert.deepEqual(out, ['수정하겠습니다.', '🔧 bash → git diff\n']);
	});

	it('splits mid-line thinking and progress markers', () => {
		const line = '내용입니다.⏳ LLM 프롬프트 처리 중... (30s 경과)💭 next thought\n';
		const out = expandGluedLines([line]);
		assert.equal(out.length, 3);
		assert.equal(out[0], '내용입니다.');
		assert.ok(out[1].startsWith('⏳ '));
		assert.ok(out[2].startsWith('💭 '));
	});

	it('does not split glue markers inside fenced code blocks', () => {
		const lines = [
			'```text\n',
			'  out.js  빌드 성공.🔧 bash → git diff\n',
			'   1 file changed(+)수정 완료\n',
			'```\n',
			'**원인:** LineCoalescer\n'
		];
		const out = expandGluedLines(lines);
		assert.equal(out.length, 5);
		assert.ok(out[1].includes('🔧 bash'));
		assert.equal(out[4], '**원인:** LineCoalescer\n');
	});

	it('does not split at column 0 (lookbehind requires prior non-space)', () => {
		const out = expandGluedLines(['🔧 bash → ls\n']);
		assert.deepEqual(out, ['🔧 bash → ls\n']);
	});
});

describe('classifyLine', () => {
	it('keeps agent checklist ✅ lines as text', () => {
		assert.equal(classifyLine('✅ TTS 캐시 K8s 볼륨 (M2-4)\n', 'text'), 'text');
		assert.equal(classifyLine('- ✅ item\n', null), 'text');
		assert.equal(classifyLine('1. ✅ item\n', null), 'text');
	});

	it('classifies known system status lines', () => {
		assert.equal(classifyLine('✅ Task complete\n', null), 'status');
		assert.equal(classifyLine('✅ Tool complete\n', null), 'status');
		assert.equal(classifyLine('🚀 Claude session starting...\n', null), 'status');
	});

	it('only treats indented lines as result after tool/result', () => {
		assert.equal(classifyLine('  tool output\n', 'tool'), 'result');
		assert.equal(classifyLine('  indented markdown\n', 'text'), 'text');
		assert.equal(classifyLine('  still result\n', 'result'), 'result');
	});
});

describe('groupLines', () => {
	it('keeps checklist markdown in a single text block (no status pills)', () => {
		const lines = [
			'리뷰 체크리스트:\n',
			'1. ✅ 인플레 자동 축소\n',
			'2. ✅ 스트릭+용서\n',
			'- ✅ TTS 캐시\n'
		];
		const blocks = groupLines(lines);
		assert.equal(blocks.length, 1);
		assert.equal(blocks[0].type, 'text');
		assert.ok(blocks[0].text.includes('1. ✅ 인플레'));
		assert.ok(blocks[0].text.includes('- ✅ TTS'));
		assert.ok(!blocks.some((b) => b.type === 'status'));
	});

	it('preserves fenced glue examples as text, not tool/result', () => {
		const lines = [
			'예제:\n',
			'```text\n',
			'  out.js  성공.🔧 bash → git diff\n',
			'   1 file changed(+)수정 완료\n',
			'```\n',
			'**원인:** 테스트\n'
		];
		const blocks = groupLines(lines);
		assert.ok(blocks.every((b) => b.type === 'text' || b.type === 'text'));
		assert.ok(!blocks.some((b) => b.type === 'tool' || b.type === 'result'));
		const joined = blocks.map((b) => b.text).join('\n');
		assert.ok(joined.includes('🔧 bash'));
		assert.ok(joined.includes('**원인:**'));
	});

	it('still splits real mid-line tool glue outside fences', () => {
		const lines = ['이제 커밋하겠습니다.🔧 bash → git commit\n', '  1 file changed\n'];
		const blocks = groupLines(lines);
		assert.equal(blocks[0].type, 'text');
		assert.ok(blocks[0].text.includes('커밋하겠습니다.'));
		assert.equal(blocks[1].type, 'tool');
		assert.equal(blocks[2].type, 'result');
	});

	it('strips trailing newlines so list items stay contiguous', () => {
		const lines = [
			'1. ✅ 인플레 자동 축소\n',
			'2. ✅ 스트릭+용서\n',
			'3. ✅ 시드 60\n'
		];
		const blocks = groupLines(lines);
		assert.equal(blocks.length, 1);
		// Single \n between items — not \n\n which would break tight lists.
		assert.equal(
			blocks[0].text,
			'1. ✅ 인플레 자동 축소\n2. ✅ 스트릭+용서\n3. ✅ 시드 60'
		);
	});
});
