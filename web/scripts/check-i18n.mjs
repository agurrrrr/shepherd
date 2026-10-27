#!/usr/bin/env node
// check-i18n (#661, #662): reports Hangul left in the Web UI source outside
// the i18n dictionaries, and checks that ko.js and en.js define the same keys.
//
//   npm run check:i18n                 report; exit 0 unless the key sets differ
//   npm run check:i18n -- --strict     also exit 1 while any Hangul line remains
//   npm run check:i18n -- --verbose    also print each remaining line
//
// Scanned: web/src/**/*.{svelte,js}. Not counted:
//   - comments: // and /* */ in scripts, /* */ in <style>, <!-- --> in markup
//   - web/src/lib/i18n/ (the dictionaries) and *.test.js
//   - text listed in scripts/i18n-allowlist.json for that file. Only the
//     listed text is excused; other Hangul on the same line still counts.
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const WEB_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SRC_DIR = path.join(WEB_DIR, 'src');
const I18N_DIR = path.join(SRC_DIR, 'lib', 'i18n');
const ALLOWLIST_PATH = path.join(WEB_DIR, 'scripts', 'i18n-allowlist.json');

const HANGUL = /\p{Script=Hangul}/u;

/** Replace every character but newlines with a space, so line numbers stay put. */
function blank(text) {
	return text.replace(/[^\n]/g, ' ');
}

// Keywords after which a `/` starts a regex literal rather than a division.
const REGEX_AFTER_WORD = new Set([
	'return', 'typeof', 'instanceof', 'in', 'of', 'new', 'delete', 'void',
	'throw', 'case', 'do', 'else', 'yield', 'await'
]);

/**
 * JS/TS source with comments blanked out. Strings, template literals and
 * regex literals are kept as written, so a `//` inside them is not a comment.
 * @param {string} src
 */
export function stripJsComments(src) {
	const n = src.length;
	let out = '';
	let i = 0;
	let depth = 0; // open `{` count
	const templateDepths = []; // `depth` outside each `${` we are inside
	let last = null; // last significant token: { kind: 'word' | 'punct' | 'value', text }

	/** Index just past the end of a quoted string that starts at `start`. */
	function skipString(start, quote) {
		let j = start + 1;
		while (j < n) {
			const ch = src[j];
			if (ch === '\\') j += 2;
			else if (ch === quote || ch === '\n') return j + 1;
			else j++;
		}
		return n;
	}

	/** Scan template text from `start`; stops after the closing ` or after `${`. */
	function scanTemplate(start) {
		let j = start;
		while (j < n) {
			const ch = src[j];
			if (ch === '\\') j += 2;
			else if (ch === '`') return { end: j + 1, opensExpr: false };
			else if (ch === '$' && src[j + 1] === '{') return { end: j + 2, opensExpr: true };
			else j++;
		}
		return { end: n, opensExpr: false };
	}

	/** Index just past a regex literal at `start`, or -1 when it is not one. */
	function skipRegex(start) {
		let j = start + 1;
		let inClass = false;
		while (j < n) {
			const ch = src[j];
			if (ch === '\n') return -1;
			if (ch === '\\') j += 2;
			else if (ch === '[') { inClass = true; j++; }
			else if (ch === ']') { inClass = false; j++; }
			else if (ch === '/' && !inClass) {
				j++;
				while (j < n && /[a-z]/i.test(src[j])) j++;
				return j;
			} else j++;
		}
		return -1;
	}

	function regexAllowed() {
		if (!last) return true;
		if (last.kind === 'word') return REGEX_AFTER_WORD.has(last.text);
		if (last.kind === 'punct') return !')]}'.includes(last.text);
		return false;
	}

	function emitTemplate(start) {
		const r = scanTemplate(start);
		out += src.slice(i, r.end);
		i = r.end;
		if (r.opensExpr) {
			templateDepths.push(depth);
			depth++;
			last = { kind: 'punct', text: '{' };
		} else {
			last = { kind: 'value', text: '`' };
		}
	}

	while (i < n) {
		const c = src[i];
		const next = src[i + 1];

		if (c === '/' && next === '/') {
			let end = src.indexOf('\n', i);
			if (end === -1) end = n;
			out += blank(src.slice(i, end));
			i = end;
			continue;
		}
		if (c === '/' && next === '*') {
			const close = src.indexOf('*/', i + 2);
			const end = close === -1 ? n : close + 2;
			out += blank(src.slice(i, end));
			i = end;
			continue;
		}
		if (c === '"' || c === "'") {
			const end = skipString(i, c);
			out += src.slice(i, end);
			i = end;
			last = { kind: 'value', text: c };
			continue;
		}
		if (c === '`') {
			emitTemplate(i + 1);
			continue;
		}
		if (c === '}' && templateDepths.length && depth - 1 === templateDepths[templateDepths.length - 1]) {
			// closes a `${ ... }` — back inside the template literal
			templateDepths.pop();
			depth--;
			emitTemplate(i + 1);
			continue;
		}
		if (c === '/' && regexAllowed()) {
			const end = skipRegex(i);
			if (end !== -1) {
				out += src.slice(i, end);
				i = end;
				last = { kind: 'value', text: '/' };
				continue;
			}
		}
		if (/[\w$]/.test(c)) {
			let j = i + 1;
			while (j < n && /[\w$]/.test(src[j])) j++;
			const word = src.slice(i, j);
			out += word;
			i = j;
			last = { kind: 'word', text: word };
			continue;
		}
		if (c === '{') depth++;
		else if (c === '}') depth--;
		out += c;
		i++;
		if (!/\s/.test(c)) last = { kind: 'punct', text: c };
	}
	return out;
}

/** CSS with block comments blanked out. */
function stripCssComments(src) {
	return src.replace(/\/\*[\s\S]*?(\*\/|$)/g, blank);
}

/**
 * Svelte component with comments blanked out: JS comments inside <script>,
 * CSS comments inside <style>, and <!-- --> in the markup.
 * @param {string} src
 */
export function stripSvelteComments(src) {
	let out = '';
	let i = 0;
	const blockRe = /<!--|<(script|style)\b[^>]*>/g;
	while (i < src.length) {
		blockRe.lastIndex = i;
		const m = blockRe.exec(src);
		if (!m) {
			out += src.slice(i);
			break;
		}
		out += src.slice(i, m.index);
		if (m[0] === '<!--') {
			const close = src.indexOf('-->', m.index + 4);
			const end = close === -1 ? src.length : close + 3;
			out += blank(src.slice(m.index, end));
			i = end;
			continue;
		}
		const tag = m[1];
		const bodyStart = m.index + m[0].length;
		let bodyEnd = src.indexOf(`</${tag}>`, bodyStart);
		if (bodyEnd === -1) bodyEnd = src.length;
		const body = src.slice(bodyStart, bodyEnd);
		out += m[0] + (tag === 'script' ? stripJsComments(body) : stripCssComments(body));
		i = bodyEnd;
	}
	return out;
}

/**
 * Lines that still contain Hangul once comments and allowlisted text are gone.
 * @param {string} src file content
 * @param {'svelte'|'js'} kind
 * @param {string[]} allowed text excused in this file
 * @param {Set<string>} [usedAllowed] collects the allowed text that matched
 * @returns {{ line: number, text: string }[]}
 */
export function findHangulLines(src, kind, allowed = [], usedAllowed = new Set()) {
	const stripped = kind === 'svelte' ? stripSvelteComments(src) : stripJsComments(src);
	const original = src.split('\n');
	const hits = [];
	stripped.split('\n').forEach((line, idx) => {
		if (!HANGUL.test(line)) return;
		let rest = line;
		for (const text of allowed) {
			if (rest.includes(text)) {
				usedAllowed.add(text);
				rest = rest.split(text).join('');
			}
		}
		if (HANGUL.test(rest)) hits.push({ line: idx + 1, text: original[idx].trim() });
	});
	return hits;
}

function listSourceFiles(dir) {
	const files = [];
	for (const entry of readdirSync(dir, { withFileTypes: true })) {
		const full = path.join(dir, entry.name);
		if (entry.isDirectory()) {
			if (full === I18N_DIR || entry.name === 'node_modules') continue;
			files.push(...listSourceFiles(full));
		} else if (/\.(svelte|js)$/.test(entry.name) && !entry.name.endsWith('.test.js')) {
			files.push(full);
		}
	}
	return files.sort();
}

function loadAllowlist() {
	const entries = JSON.parse(readFileSync(ALLOWLIST_PATH, 'utf8'));
	if (!Array.isArray(entries)) throw new Error('i18n-allowlist.json must be an array');
	/** @type {Map<string, string[]>} */
	const byFile = new Map();
	entries.forEach((e, idx) => {
		for (const field of ['file', 'text', 'reason']) {
			if (typeof e?.[field] !== 'string' || !e[field].trim()) {
				throw new Error(`i18n-allowlist.json entry ${idx}: "${field}" is required`);
			}
		}
		if (!byFile.has(e.file)) byFile.set(e.file, []);
		byFile.get(e.file).push(e.text);
	});
	return { entries, byFile };
}

/** Keys missing from each dictionary, compared with the other one. */
export function compareKeys(ko, en) {
	const koKeys = new Set(Object.keys(ko));
	const enKeys = new Set(Object.keys(en));
	return {
		missingInKo: [...enKeys].filter((k) => !koKeys.has(k)).sort(),
		missingInEn: [...koKeys].filter((k) => !enKeys.has(k)).sort()
	};
}

const placeholders = (text) => [...new Set(String(text).match(/\{\w+\}/g) || [])].sort().join(' ');

async function main(argv) {
	const strict = argv.includes('--strict');
	const verbose = argv.includes('--verbose');
	let failed = false;

	// 1. Dictionaries
	const ko = (await import(pathToFileURL(path.join(I18N_DIR, 'ko.js')).href)).default;
	const en = (await import(pathToFileURL(path.join(I18N_DIR, 'en.js')).href)).default;
	const { missingInKo, missingInEn } = compareKeys(ko, en);
	const nonString = Object.entries({ ...en, ...ko }).filter(([, v]) => typeof v !== 'string');
	if (missingInKo.length || missingInEn.length || nonString.length) {
		failed = true;
		console.log('✗ ko.js and en.js do not match');
		for (const k of missingInKo) console.log(`    missing in ko.js: ${k}`);
		for (const k of missingInEn) console.log(`    missing in en.js: ${k}`);
		for (const [k] of nonString) console.log(`    not a string: ${k}`);
	} else {
		console.log(`✓ ko.js and en.js define the same ${Object.keys(en).length} keys`);
	}
	for (const key of Object.keys(en)) {
		if (key in ko && placeholders(ko[key]) !== placeholders(en[key])) {
			console.log(`  ! placeholders differ for ${key}: ko "${placeholders(ko[key])}" / en "${placeholders(en[key])}"`);
		}
	}

	// 2. Hangul left in the source
	const { entries, byFile } = loadAllowlist();
	const used = new Map(); // file -> Set of allowed text that matched
	const results = [];
	for (const file of listSourceFiles(SRC_DIR)) {
		const rel = path.relative(WEB_DIR, file).split(path.sep).join('/');
		const allowed = byFile.get(rel) || [];
		const usedHere = new Set();
		const hits = findHangulLines(readFileSync(file, 'utf8'), file.endsWith('.svelte') ? 'svelte' : 'js', allowed, usedHere);
		used.set(rel, usedHere);
		if (hits.length) results.push({ rel, hits });
	}

	const total = results.reduce((sum, r) => sum + r.hits.length, 0);
	console.log('');
	console.log('Lines with Hangul outside src/lib/i18n/ (comments, *.test.js and allowlist skipped):');
	results.sort((a, b) => b.hits.length - a.hits.length || a.rel.localeCompare(b.rel));
	for (const { rel, hits } of results) {
		console.log(`${String(hits.length).padStart(6)}  ${rel}`);
		if (verbose) for (const h of hits) console.log(`          ${h.line}: ${h.text}`);
	}
	console.log(`Total: ${total} line(s) in ${results.length} file(s)`);

	const stale = entries.filter((e) => !used.get(e.file)?.has(e.text));
	for (const e of stale) console.log(`  ! allowlist entry not found: ${e.file} "${e.text}"`);

	if (strict && total > 0) {
		console.log('✗ --strict: Hangul UI text remains; move it to src/lib/i18n/ or the allowlist');
		failed = true;
	}
	return failed ? 1 : 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	main(process.argv.slice(2)).then(
		(code) => process.exit(code),
		(err) => {
			console.error(err);
			process.exit(1);
		}
	);
}
