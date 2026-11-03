/**
 * Node built-in runner: node --test web/src/lib/uploadLimit.test.js
 *
 * The upload-limit store decides which files the browser sends, so its two
 * failure modes matter: a limit that drops to 0 would reject every upload, and a
 * limit that ignores the daemon's ceiling would fail on Save. These tests cover
 * the pure helpers; Svelte components read the same store.
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { get } from 'svelte/store';
import { uploadLimitBytes, DEFAULT_UPLOAD_LIMIT_BYTES } from './stores.js';
import {
	applyUploadLimitMB,
	normalizeUploadMBInput,
	getUploadLimitBytes,
	formatBytes,
	MAX_UPLOAD_MB_CEIL,
	DEFAULT_UPLOAD_MB
} from './uploadLimit.js';

describe('applyUploadLimitMB', () => {
	it('stores the configured value in bytes', () => {
		assert.equal(applyUploadLimitMB(25), 25 * 1024 * 1024);
		assert.equal(get(uploadLimitBytes), 25 * 1024 * 1024);
		assert.equal(getUploadLimitBytes(), 25 * 1024 * 1024);
	});

	it('accepts the string form a JSON/text input can carry', () => {
		assert.equal(applyUploadLimitMB('50'), 50 * 1024 * 1024);
	});

	it('falls back to the default for missing or unusable values', () => {
		for (const bad of [null, undefined, '', 0, -1, 'abc', MAX_UPLOAD_MB_CEIL + 1]) {
			assert.equal(applyUploadLimitMB(bad), DEFAULT_UPLOAD_LIMIT_BYTES, `value ${String(bad)}`);
		}
	});

	it('keeps the ceiling usable', () => {
		assert.equal(applyUploadLimitMB(MAX_UPLOAD_MB_CEIL), MAX_UPLOAD_MB_CEIL * 1024 * 1024);
	});
});

describe('normalizeUploadMBInput', () => {
	it('passes a valid number through', () => {
		assert.equal(normalizeUploadMBInput(30), 30);
		assert.equal(normalizeUploadMBInput('30'), 30);
	});

	it('replaces an empty or invalid field with the default instead of 0', () => {
		for (const bad of ['', null, undefined, NaN, 'ten', 0, -5]) {
			assert.equal(normalizeUploadMBInput(bad), DEFAULT_UPLOAD_MB, `value ${String(bad)}`);
		}
	});

	it('clamps above the ceiling', () => {
		assert.equal(normalizeUploadMBInput(9999), MAX_UPLOAD_MB_CEIL);
	});

	it('rounds a fractional value to a whole MB', () => {
		assert.equal(normalizeUploadMBInput(12.4), 12);
	});
});

describe('formatBytes', () => {
	it('labels whole MB without a decimal', () => {
		assert.equal(formatBytes(10 * 1024 * 1024), '10 MB');
		assert.equal(formatBytes(512 * 1024 * 1024), '512 MB');
	});

	it('keeps one decimal when the value is not whole', () => {
		assert.equal(formatBytes(1536 * 1024), '1.5 MB');
	});

	it('uses KB and B below a megabyte', () => {
		assert.equal(formatBytes(512 * 1024), '512.0 KB');
		assert.equal(formatBytes(900), '900 B');
	});

	it('returns empty for a non-number', () => {
		assert.equal(formatBytes(undefined), '');
	});
});
