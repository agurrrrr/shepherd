/**
 * Streaming markdown display helpers.
 *
 * Problem (Grok / any high-frequency stream, task #7202 residual, #7274):
 * OutputViewer keyed the cache on the *full* growing text. Each new token
 * missed the cache → UI flipped block-text-raw ↔ rendered HTML (visible
 * flicker: "렌더링이 안되다 되다"). Debounce alone does not help: after a
 * 200ms pause HTML appears, then the next burst flips back to raw.
 *
 * Fix: keep the last successfully rendered HTML as a *sticky* snapshot for
 * the same stream id. While a newer render is pending, show sticky HTML plus
 * the unrendered tail as plain text — content keeps growing without a DOM
 * mode switch.
 */

/**
 * @typedef {{ text: string, html: string }} StickySnapshot
 * @typedef {{ kind: 'exact', html: string, sticky: StickySnapshot }
 *   | { kind: 'sticky', html: string, tail: string, sticky: StickySnapshot }
 *   | { kind: 'raw', sticky: StickySnapshot | null }} StreamMdView
 */

/**
 * Resolve what to show for a streaming markdown body.
 *
 * @param {string} text - current full markdown source
 * @param {string | null | undefined} exactHtml - cache hit for `text` (null/undefined = miss)
 * @param {StickySnapshot | null | undefined} sticky - last good render for this stream id
 * @returns {StreamMdView}
 */
export function resolveStreamMd(text, exactHtml, sticky) {
	if (text == null || text === '') {
		return { kind: 'raw', sticky: null };
	}

	if (exactHtml) {
		const next = { text, html: exactHtml };
		return { kind: 'exact', html: exactHtml, sticky: next };
	}

	// Prefer sticky when the stream is a pure extension of the last render.
	// (Normal token streaming: "Hello" → "Hello world".)
	if (sticky?.html && sticky.text != null && text.startsWith(sticky.text)) {
		return {
			kind: 'sticky',
			html: sticky.html,
			tail: text.slice(sticky.text.length),
			sticky,
		};
	}

	// Text was rewritten or is shorter — drop sticky so we don't show stale body.
	return { kind: 'raw', sticky: null };
}

/**
 * Build / refresh a sticky snapshot after a successful render.
 * @param {string} text
 * @param {string} html
 * @returns {StickySnapshot}
 */
export function stickyFromRender(text, html) {
	return { text, html };
}

/**
 * Simple string hash used as markdown cache key (matches OutputViewer).
 * @param {string} t
 * @returns {string}
 */
export function hashText(t) {
	let h = 0;
	for (let i = 0; i < t.length; i++) h = ((h << 5) - h + t.charCodeAt(i)) | 0;
	return 'md_' + h + '_' + t.length;
}

/**
 * Create an LRU-capped markdown cache + sticky registry for one viewer instance.
 *
 * @param {{ limit?: number, render: (text: string) => Promise<string> }} opts
 */
export function createMdStreamCache(opts) {
	const limit = opts.limit ?? 100;
	/** @type {Record<string, string | null>} */
	const cache = Object.create(null);
	/** @type {string[]} */
	const keys = [];
	/** @type {Record<string, StickySnapshot>} */
	const stickyById = Object.create(null);
	/** @type {Record<string, number>} */
	const genById = Object.create(null);

	function getExact(text) {
		if (text == null || text === '') return null;
		const v = cache[hashText(text)];
		// null = in-flight pending; treat as miss for display
		return v || null;
	}

	/**
	 * @param {string} id stream identity (e.g. "text:0", "prop:1")
	 * @param {string} text
	 * @returns {StreamMdView}
	 */
	function resolve(id, text) {
		const exact = getExact(text);
		const view = resolveStreamMd(text, exact, stickyById[id] || null);
		// When we have an exact hit, pin sticky for this id.
		if (view.kind === 'exact') {
			stickyById[id] = view.sticky;
		} else if (view.kind === 'raw' && view.sticky === null) {
			delete stickyById[id];
		}
		return view;
	}

	/**
	 * Kick off (or skip) an async render. Updates sticky for `id` when done,
	 * ignoring stale completions via a generation counter.
	 * @param {string} text
	 * @param {string} [id]
	 * @returns {Promise<void>}
	 */
	async function ensure(text, id) {
		if (!text || !String(text).trim()) return;
		const key = hashText(text);
		if (cache[key] !== undefined) {
			if (cache[key] && id != null) {
				stickyById[id] = stickyFromRender(text, cache[key]);
			}
			return;
		}
		cache[key] = null;
		keys.push(key);
		while (keys.length > limit) {
			const old = keys.shift();
			delete cache[old];
		}

		const gen = id != null ? (genById[id] = (genById[id] || 0) + 1) : 0;
		try {
			const html = await opts.render(text);
			cache[key] = html;
			if (id != null && genById[id] === gen) {
				stickyById[id] = stickyFromRender(text, html);
			}
		} catch {
			cache[key] = text;
			if (id != null && genById[id] === gen) {
				stickyById[id] = stickyFromRender(text, text);
			}
		}
	}

	function peekSticky(id) {
		return stickyById[id] || null;
	}

	/** Test / debug access */
	function _cache() {
		return cache;
	}

	return { getExact, resolve, ensure, peekSticky, hashText, _cache };
}
