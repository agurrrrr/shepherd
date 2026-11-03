// JSON parsing for every /api reply.
//
// The daemon answers API calls with `{"success": ..., "message": ...}`. A reply
// that is not JSON still reaches the browser: a reverse proxy or ingress in
// front of the daemon writes its own error page (nginx returns an HTML 413 page
// for a body over `proxy-body-size`), and an older daemon can answer as plain
// text. Calling `res.json()` on those throws, and the UI shows
// `Unexpected token '<', "<html> <h"... is not valid JSON` — a message that
// names neither the failing stage nor the limit to change. The upload flows hit
// this first because they are the only calls that carry a large body.
//
// So parse defensively: return the daemon's envelope when it is JSON, and a
// plain object describing the transport failure when it is not. Callers already
// branch on `res?.success` and print `res?.message`, so no caller needs to know
// which case happened.

import { translate } from './i18n/index.js';

/**
 * Parse an API reply body.
 * @param {string} text raw body (may be empty, HTML, or plain text)
 * @param {{status?: number, statusText?: string}} [meta] response status, used
 *   only when the body is not JSON, so the message can still say what failed.
 * @returns {any|null} the parsed JSON value, or `{success: false, message}` for
 *   a non-JSON body, or null for an empty body.
 */
export function parseApiResponse(text, meta = {}) {
	if (!text) return null;
	try {
		return JSON.parse(text);
	} catch {
		return { success: false, message: transportMessage(meta) };
	}
}

/**
 * A readable message for a reply whose body was not JSON. The raw body is never
 * returned: an nginx error page is HTML, and putting it in the UI's error line
 * tells the user nothing.
 * @param {{status?: number, statusText?: string}} meta
 * @returns {string}
 */
export function transportMessage(meta = {}) {
	// 413 from the transport means something between the browser and the daemon
	// refused the body — usually the proxy's own body-size limit, not the
	// daemon's max_upload_mb. The hint has to name both, or the user raises the
	// wrong knob.
	if (meta.status === 413) return translate('api.bodyTooLarge');
	if (meta.status >= 500) return translate('api.gatewayError', { status: meta.status });
	return translate('api.notJSON', { status: meta.status ?? 0 });
}
