/**
 * Node built-in test runner: node --test web/src/lib/mdStream.test.js
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
	resolveStreamMd,
	stickyFromRender,
	hashText,
	createMdStreamCache,
} from './mdStream.js';

describe('resolveStreamMd', () => {
	it('returns raw for empty text', () => {
		const v = resolveStreamMd('', null, null);
		assert.equal(v.kind, 'raw');
	});

	it('returns exact when cache hits', () => {
		const v = resolveStreamMd('# Hi', '<h1>Hi</h1>', null);
		assert.equal(v.kind, 'exact');
		assert.equal(v.html, '<h1>Hi</h1>');
		assert.deepEqual(v.sticky, { text: '# Hi', html: '<h1>Hi</h1>' });
	});

	it('returns sticky + tail when text extends last render', () => {
		const sticky = stickyFromRender('Hello', '<p>Hello</p>');
		const v = resolveStreamMd('Hello world', null, sticky);
		assert.equal(v.kind, 'sticky');
		assert.equal(v.html, '<p>Hello</p>');
		assert.equal(v.tail, ' world');
	});

	it('does not flicker to raw when stream grows past a prior exact', () => {
		// Simulate: pause rendered "The quick", then tokens " brown fox"
		const sticky = stickyFromRender('The quick', '<p>The quick</p>');
		const mid = resolveStreamMd('The quick brown', null, sticky);
		assert.equal(mid.kind, 'sticky');
		assert.equal(mid.tail, ' brown');
		// Still not raw — this is the anti-flicker guarantee
		assert.notEqual(mid.kind, 'raw');
	});

	it('drops sticky when text is not an extension (rewrite)', () => {
		const sticky = stickyFromRender('AAA', '<p>AAA</p>');
		const v = resolveStreamMd('BBB', null, sticky);
		assert.equal(v.kind, 'raw');
		assert.equal(v.sticky, null);
	});

	it('drops sticky when text shrinks', () => {
		const sticky = stickyFromRender('Hello world', '<p>Hello world</p>');
		const v = resolveStreamMd('Hello', null, sticky);
		assert.equal(v.kind, 'raw');
	});
});

describe('hashText', () => {
	it('is stable for same input and differs by length', () => {
		assert.equal(hashText('abc'), hashText('abc'));
		assert.notEqual(hashText('ab'), hashText('abc'));
	});
});

describe('createMdStreamCache', () => {
	it('sticky holds through growth until new exact arrives', async () => {
		const rendered = new Map([
			['A', '<p>A</p>'],
			['AB', '<p>AB</p>'],
		]);
		const cache = createMdStreamCache({
			render: async (t) => {
				if (!rendered.has(t)) throw new Error('unexpected ' + t);
				return rendered.get(t);
			},
		});

		await cache.ensure('A', 't0');
		const v1 = cache.resolve('t0', 'A');
		assert.equal(v1.kind, 'exact');
		assert.equal(v1.html, '<p>A</p>');

		// Growth before re-render: sticky + tail, never raw
		const v2 = cache.resolve('t0', 'AB');
		assert.equal(v2.kind, 'sticky');
		assert.equal(v2.html, '<p>A</p>');
		assert.equal(v2.tail, 'B');

		await cache.ensure('AB', 't0');
		const v3 = cache.resolve('t0', 'AB');
		assert.equal(v3.kind, 'exact');
		assert.equal(v3.html, '<p>AB</p>');
	});

	it('ignores stale ensure completion for an id', async () => {
		let resolveSlow;
		const slow = new Promise((r) => {
			resolveSlow = r;
		});
		const cache = createMdStreamCache({
			render: async (t) => {
				if (t === 'slow') {
					await slow;
					return '<p>slow</p>';
				}
				return '<p>fast</p>';
			},
		});

		const pSlow = cache.ensure('slow', 'x');
		await cache.ensure('fast', 'x');
		assert.equal(cache.peekSticky('x').html, '<p>fast</p>');

		resolveSlow();
		await pSlow;
		// Generation guard: slow must not overwrite fast
		assert.equal(cache.peekSticky('x').html, '<p>fast</p>');
		assert.equal(cache.peekSticky('x').text, 'fast');
	});
});
