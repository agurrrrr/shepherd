/**
 * Node built-in runner: node --test web/src/lib/apiResponse.test.js
 *
 * The upload bug this guards: a reverse proxy in front of the daemon answers an
 * oversized body with an nginx HTML page (413), and `res.json()` threw on it, so
 * the Files tab reported `Unexpected token '<', "<html> <h"... is not valid JSON`.
 * These tests cover the two failure modes that matter: an HTML/plain-text body
 * must never throw, and a 413 must name both limits — the daemon's Max Upload
 * Size and the proxy's own body-size limit — because raising only one of them
 * leaves the upload broken.
 */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { parseApiResponse, transportMessage } from './apiResponse.js';

const NGINX_413 = `<html>
<head><title>413 Request Entity Too Large</title></head>
<body>
<center><h1>413 Request Entity Too Large</h1></center>
<hr><center>nginx</center>
</body>
</html>`;

describe('parseApiResponse', () => {
	it('returns the daemon envelope unchanged', () => {
		const parsed = parseApiResponse('{"success":true,"data":{"files":[]}}');
		assert.equal(parsed.success, true);
		assert.deepEqual(parsed.data.files, []);
	});

	it('returns null for an empty body', () => {
		assert.equal(parseApiResponse(''), null);
	});

	it('does not throw on an nginx HTML error page', () => {
		const parsed = parseApiResponse(NGINX_413, { status: 413 });
		assert.equal(parsed.success, false);
		assert.ok(parsed.message.length > 0);
	});

	it('does not throw on plain text (an older daemon)', () => {
		const parsed = parseApiResponse('Request Entity Too Large', { status: 413 });
		assert.equal(parsed.success, false);
	});

	it('never leaks the raw HTML into the message', () => {
		const parsed = parseApiResponse(NGINX_413, { status: 413 });
		assert.ok(!parsed.message.includes('<html>'));
		assert.ok(!parsed.message.includes('nginx'));
	});

	it('keeps a JSON-looking error envelope readable', () => {
		const parsed = parseApiResponse('{"success":false,"message":"file too big"}', { status: 400 });
		assert.equal(parsed.message, 'file too big');
	});
});

describe('transportMessage', () => {
	it('names both limits for a 413', () => {
		const msg = transportMessage({ status: 413 });
		assert.match(msg, /Max Upload Size/);
		assert.match(msg, /proxy|ingress/i);
	});

	it('reports the status for a gateway error', () => {
		const msg = transportMessage({ status: 502 });
		assert.match(msg, /502/);
	});

	it('reports the status for an unreadable reply', () => {
		const msg = transportMessage({ status: 418 });
		assert.match(msg, /418/);
	});

	it('does not need a status', () => {
		const msg = transportMessage();
		assert.ok(msg.length > 0);
	});
});
