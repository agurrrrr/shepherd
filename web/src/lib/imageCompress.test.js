/**
 * Node built-in test runner: node --test web/src/lib/imageCompress.test.js
 *
 * Only the pure helpers are covered here. compressImageFile drives browser
 * APIs (createImageBitmap, canvas.toBlob) that the Node test runner lacks.
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { MAX_DIM, JPEG_QUALITY, isCompressibleImage, scaleDimensions } from './imageCompress.js';

describe('isCompressibleImage', () => {
	it('accepts raster image types', () => {
		assert.equal(isCompressibleImage('image/png'), true);
		assert.equal(isCompressibleImage('image/jpeg'), true);
		assert.equal(isCompressibleImage('image/webp'), true);
	});

	it('rejects gif so animation is not flattened to one frame', () => {
		assert.equal(isCompressibleImage('image/gif'), false);
	});

	it('rejects non-images and empty types', () => {
		assert.equal(isCompressibleImage('application/pdf'), false);
		assert.equal(isCompressibleImage('text/plain'), false);
		assert.equal(isCompressibleImage(''), false);
		assert.equal(isCompressibleImage(undefined), false);
	});
});

describe('scaleDimensions', () => {
	it('leaves images within the cap unchanged', () => {
		assert.deepEqual(scaleDimensions(800, 600), { w: 800, h: 600 });
		assert.deepEqual(scaleDimensions(MAX_DIM, MAX_DIM), { w: MAX_DIM, h: MAX_DIM });
	});

	it('scales the longest side down to the cap and keeps aspect ratio', () => {
		const { w, h } = scaleDimensions(5000, 3750);
		assert.equal(w, MAX_DIM);
		assert.equal(h, Math.round(3750 * (MAX_DIM / 5000)));
	});

	it('handles a tall portrait image', () => {
		const { w, h } = scaleDimensions(1080, 6000);
		assert.equal(h, MAX_DIM);
		assert.ok(w < w + 1 && w >= 1);
		assert.ok(w < h);
	});

	it('never returns a zero dimension for an extreme aspect ratio', () => {
		const { w, h } = scaleDimensions(20000, 3);
		assert.ok(w >= 1);
		assert.ok(h >= 1);
	});

	it('honors an explicit maxDim override', () => {
		assert.deepEqual(scaleDimensions(2000, 1000, 1024), { w: 1024, h: 512 });
	});
});

describe('quality constants', () => {
	it('targets 90 percent JPEG quality', () => {
		assert.equal(JPEG_QUALITY, 0.9);
	});

	it('caps the longest side at a vision-friendly size', () => {
		assert.equal(MAX_DIM, 4096);
	});
});
