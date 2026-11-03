// Upload-limit plumbing for the `max_upload_mb` setting.
//
// The daemon owns the per-file upload limit (internal/config: max_upload_mb,
// default 10 MB) and enforces it in both upload handlers. The browser checks the
// same number before sending, so an oversized file is refused with a readable
// message instead of an aborted request. This module keeps that number in the
// `uploadLimitBytes` store:
//
//   - routes/+layout.svelte writes it from GET /api/config on sign-in;
//   - the Settings page refreshes it after a save, so a new limit applies to
//     the next upload without a page reload;
//   - upload call sites read it (CommandInput.svelte, FileBrowser.svelte).
//
// MAX_UPLOAD_MB_CEIL mirrors config.MaxUploadMBHardCeil and DEFAULT_UPLOAD_MB
// mirrors config.DefaultMaxUploadMB. The daemon rejects an out-of-range value
// with 400; the form clamps to the same numbers so Save does not fail on a
// guessable typo.
import { get } from 'svelte/store';
import { uploadLimitBytes, DEFAULT_UPLOAD_LIMIT_BYTES } from './stores.js';

/** Hard ceiling for `max_upload_mb`, in MB (config.MaxUploadMBHardCeil). */
export const MAX_UPLOAD_MB_CEIL = 512;

/** Fallback for `max_upload_mb`, in MB — derived from the store's default byte
 * limit so the two cannot drift apart. */
export const DEFAULT_UPLOAD_MB = DEFAULT_UPLOAD_LIMIT_BYTES / (1024 * 1024);

/**
 * Store the daemon's limit. A missing, non-numeric, or out-of-range value keeps
 * the default instead of storing 0 — a 0 limit would reject every upload.
 * @param {unknown} mb value of `max_upload_mb` from /api/config
 * @returns {number} the limit now in the store, in bytes
 */
export function applyUploadLimitMB(mb) {
	const parsed = toWholeMB(mb);
	if (parsed === null || parsed < 1 || parsed > MAX_UPLOAD_MB_CEIL) {
		uploadLimitBytes.set(DEFAULT_UPLOAD_LIMIT_BYTES);
		return DEFAULT_UPLOAD_LIMIT_BYTES;
	}
	const bytes = parsed * 1024 * 1024;
	uploadLimitBytes.set(bytes);
	return bytes;
}

/**
 * Whole MB count for a raw setting value, or null when it is not a number.
 * @param {unknown} value
 * @returns {number|null}
 */
function toWholeMB(value) {
	const parsed = typeof value === 'number' ? value : parseInt(value ?? '', 10);
	if (!Number.isFinite(parsed)) return null;
	return Math.round(parsed);
}

/**
 * Clamp what the settings form sends for `max_upload_mb`. An empty or invalid
 * field falls back to the default rather than sending 0, and anything above the
 * ceiling comes down to it. The daemon validates again — this only keeps the
 * form honest.
 * @param {unknown} value raw input value (number or string)
 * @returns {number} a whole MB count between 1 and MAX_UPLOAD_MB_CEIL
 */
export function normalizeUploadMBInput(value) {
	const parsed = toWholeMB(value);
	if (parsed === null || parsed < 1) return DEFAULT_UPLOAD_MB;
	return Math.min(parsed, MAX_UPLOAD_MB_CEIL);
}

/** Current per-file upload limit in bytes. */
export function getUploadLimitBytes() {
	return get(uploadLimitBytes) || DEFAULT_UPLOAD_LIMIT_BYTES;
}

/**
 * "10 MB" / "512 KB" style label for a byte count, used in upload messages so
 * the number a user sees matches the number they set.
 * @param {number} bytes
 * @returns {string}
 */
export function formatBytes(bytes) {
	if (!Number.isFinite(bytes)) return '';
	if (bytes < 1024) return bytes + ' B';
	if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
	const mb = bytes / (1024 * 1024);
	return (Number.isInteger(mb) ? String(mb) : mb.toFixed(1)) + ' MB';
}
