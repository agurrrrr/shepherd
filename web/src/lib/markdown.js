/**
 * Shared carta-md setup for Live Output / MAGI / summaries.
 *
 * CommonMark treats a single '\n' inside a paragraph as a space. The Live
 * Output raw/sticky tail uses white-space:pre-wrap, so those enters are
 * visible while streaming, then vanish ~280ms later when carta HTML lands
 * (SSE "alignment jumps after the body prints"). Convert leftover soft
 * breaks into mdast `break` nodes so remark-rehype emits <br>.
 *
 * Trailing-NL stripping in groupLines / appendLiveOutput stays: that only
 * stops DB lines ("foo\\n") from becoming blank lines between list items.
 *
 * Do not import carta-md from this file — node --test cannot load its
 * .svelte entry. Viewers pass `cartaSoftBreaks` to `new Carta({ extensions })`.
 */

const SKIP_SOFT_BREAK = new Set(['code', 'inlineCode']);

/**
 * Replace '\n' inside phrasing `text` nodes with mdast `break`.
 * Fenced/indented code and inline code store source on `value`, so they
 * are not rewritten.
 *
 * @param {object} tree
 * @returns {object}
 */
export function convertSoftBreaks(tree) {
	if (!tree || !Array.isArray(tree.children)) return tree;

	const next = [];
	for (const child of tree.children) {
		if (
			child &&
			child.type === 'text' &&
			typeof child.value === 'string' &&
			child.value.includes('\n')
		) {
			const parts = child.value.split(/\r?\n/);
			for (let i = 0; i < parts.length; i++) {
				if (i > 0) next.push({ type: 'break' });
				if (parts[i] !== '') next.push({ type: 'text', value: parts[i] });
			}
			continue;
		}
		if (child && !SKIP_SOFT_BREAK.has(child.type)) {
			convertSoftBreaks(child);
		}
		next.push(child);
	}
	tree.children = next;
	return tree;
}

/** unified remark plugin (sync). Carta runs this after remark-gfm. */
export function remarkPreserveSoftBreaks() {
	return (tree) => {
		convertSoftBreaks(tree);
	};
}

/** Carta extension matching carta-md Plugin shape. */
export const cartaSoftBreaks = {
	transformers: [
		{
			execution: 'sync',
			type: 'remark',
			transform({ processor }) {
				processor.use(remarkPreserveSoftBreaks);
			}
		}
	]
};
