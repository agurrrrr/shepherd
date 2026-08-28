/**
 * Node built-in test runner: node --test web/src/lib/markdown.test.js
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { unified } from 'unified';
import remarkParse from 'remark-parse';
import remarkGfm from 'remark-gfm';
import remarkRehype from 'remark-rehype';
import rehypeStringify from 'rehype-stringify';
import { convertSoftBreaks, remarkPreserveSoftBreaks } from './markdown.js';

function render(src) {
	return unified()
		.use(remarkParse)
		.use(remarkGfm)
		.use(remarkPreserveSoftBreaks)
		.use(remarkRehype)
		.use(rehypeStringify)
		.process(src)
		.then((f) => String(f));
}

describe('convertSoftBreaks', () => {
	it('turns a single newline inside a paragraph into a break node', () => {
		const tree = {
			type: 'root',
			children: [
				{
					type: 'paragraph',
					children: [{ type: 'text', value: 'Hello\nWorld' }]
				}
			]
		};
		convertSoftBreaks(tree);
		assert.deepEqual(tree.children[0].children, [
			{ type: 'text', value: 'Hello' },
			{ type: 'break' },
			{ type: 'text', value: 'World' }
		]);
	});

	it('does not rewrite fenced code value', () => {
		const tree = {
			type: 'root',
			children: [{ type: 'code', value: 'line1\nline2' }]
		};
		convertSoftBreaks(tree);
		assert.equal(tree.children[0].value, 'line1\nline2');
		assert.equal(tree.children[0].type, 'code');
	});

	it('splits newlines nested under strong/emphasis', () => {
		const tree = {
			type: 'root',
			children: [
				{
					type: 'paragraph',
					children: [
						{ type: 'strong', children: [{ type: 'text', value: 'a\nb' }] }
					]
				}
			]
		};
		convertSoftBreaks(tree);
		assert.deepEqual(tree.children[0].children[0].children, [
			{ type: 'text', value: 'a' },
			{ type: 'break' },
			{ type: 'text', value: 'b' }
		]);
	});
});

describe('remarkPreserveSoftBreaks HTML', () => {
	it('keeps a single enter as <br> instead of a space', async () => {
		const html = await render('문장1입니다.\n문장2입니다.');
		assert.match(html, /문장1입니다\.<br>\s*문장2입니다/);
		assert.doesNotMatch(html, /문장1입니다\.\s+문장2입니다/);
	});

	it('still splits paragraphs on a blank line', async () => {
		const html = await render('문장1입니다.\n\n다음 단락입니다.');
		assert.match(html, /<p>문장1입니다\.<\/p>/);
		assert.match(html, /<p>다음 단락입니다\.<\/p>/);
	});

	it('does not break tight numbered lists (trailing-NL strip contract)', async () => {
		const html = await render('1. ✅ 인플레 자동 축소\n2. ✅ 스트릭+용서\n3. ✅ 시드 60');
		assert.match(html, /<ol>/);
		assert.match(html, /<li>✅ 인플레 자동 축소<\/li>/);
		assert.match(html, /<li>✅ 스트릭\+용서<\/li>/);
		assert.match(html, /<li>✅ 시드 60<\/li>/);
	});

	it('does not break GFM tables', async () => {
		const html = await render('| a | b |\n| --- | --- |\n| 1 | 2 |');
		assert.match(html, /<table>/);
		assert.match(html, /<th>a<\/th>/);
		assert.match(html, /<td>1<\/td>/);
	});

	it('leaves fenced code newlines as source, not <br>', async () => {
		const html = await render('```\nline1\nline2\n```');
		assert.match(html, /<pre><code>line1\nline2\n?<\/code><\/pre>/);
		assert.doesNotMatch(html, /<br>/);
	});

	it('does not double-break markdown two-space hard breaks', async () => {
		const html = await render('Hello  \nWorld');
		const brs = html.match(/<br>/g) || [];
		assert.equal(brs.length, 1);
	});
});
