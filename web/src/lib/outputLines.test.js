/**
 * Node built-in test runner: node --test web/src/lib/outputLines.test.js
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { expandGluedLines, classifyLine, groupLines, thinkingBody } from './outputLines.js';

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

	it('classifies 💭 lines as thinking, with 3-space continuations', () => {
		assert.equal(classifyLine('💭 reasoning about the bug\n', null), 'thinking');
		assert.equal(classifyLine('   more thought on next line\n', 'thinking'), 'thinking');
		// 3-space indent after text is ordinary markdown, not thinking.
		assert.equal(classifyLine('   indented list item\n', 'text'), 'text');
	});
});

describe('thinkingBody', () => {
	it('strips 💭 marker and continuation indent', () => {
		assert.equal(thinkingBody('💭 hello'), 'hello');
		assert.equal(thinkingBody('   cont'), 'cont');
		assert.equal(thinkingBody('plain'), 'plain');
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

	it('groups 💭 reasoning into a thinking block separate from answer text', () => {
		const lines = [
			'💭 The flicker is raw↔HTML mode flip\n',
			'   sticky HTML + tail is the fix\n',
			'Grok 스트리밍 깜박임 원인을 잡고 수정했습니다.\n',
			'**원인**은 프론트 표시 모드 전환입니다.\n'
		];
		const blocks = groupLines(lines);
		assert.equal(blocks.length, 2);
		assert.equal(blocks[0].type, 'thinking');
		assert.ok(blocks[0].text.includes('flicker'));
		assert.ok(blocks[0].text.includes('sticky HTML'));
		assert.ok(!blocks[0].text.startsWith('💭'));
		assert.equal(blocks[1].type, 'text');
		assert.ok(blocks[1].text.includes('**원인**'));
		assert.ok(blocks[1].text.includes('Grok 스트리밍'));
	});

	it('splits mid-line 💭 glue and merges adjacent re-tagged thought chunks', () => {
		// Grok text→thought without newline + safety-flush re-tags each chunk.
		const lines = [
			'수정하겠습니다.💭 Now update OutputViewer\n',
			'💭 and MagiStreamPanel next\n',
			'\n',
			'테스트 42개 통과.\n'
		];
		const blocks = groupLines(lines);
		const types = blocks.map((b) => b.type);
		assert.ok(types.includes('thinking'));
		assert.ok(types.includes('text'));
		const think = blocks.find((b) => b.type === 'thinking');
		assert.ok(think.text.includes('Now update OutputViewer'));
		assert.ok(think.text.includes('MagiStreamPanel'));
		const texts = blocks.filter((b) => b.type === 'text').map((b) => b.text).join('\n');
		assert.ok(texts.includes('수정하겠습니다.'));
		assert.ok(texts.includes('테스트 42개'));
	});
});
