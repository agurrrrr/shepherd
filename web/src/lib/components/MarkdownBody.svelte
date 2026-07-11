<script>
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';

	/** @type {{ text?: string }} */
	let { text = '' } = $props();

	const carta = new Carta({ sanitizer: DOMPurify.sanitize });

	let html = $state(/** @type {string|null} */ (null));
	let lastText = $state('');

	$effect(() => {
		const src = text ?? '';
		if (src === lastText && html !== null) return;
		lastText = src;
		if (!src.trim()) {
			html = '';
			return;
		}
		let cancelled = false;
		html = null;
		carta.render(src).then((rendered) => {
			if (!cancelled) html = rendered;
		}).catch(() => {
			if (!cancelled) html = src;
		});
		return () => {
			cancelled = true;
		};
	});
</script>

{#if !text?.trim()}
	<!-- empty -->
{:else if html}
	<div class="markdown-body">{@html html}</div>
{:else}
	<div class="markdown-body-raw">{text}</div>
{/if}

<style>
	.markdown-body {
		padding: 4px 0;
		font-size: 13px;
		line-height: 1.6;
		color: var(--text-primary);
		overflow-x: hidden;
		word-break: break-word;
		min-width: 0;
	}

	.markdown-body :global(p) { margin: 4px 0; }

	.markdown-body :global(pre) {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 8px 12px;
		margin: 6px 0;
		font-size: 12px;
		overflow-x: auto;
	}

	.markdown-body :global(code) {
		background: var(--bg-tertiary);
		padding: 1px 4px;
		border-radius: 3px;
		font-size: 12px;
	}

	.markdown-body :global(pre code) {
		background: none;
		padding: 0;
	}

	.markdown-body :global(ul),
	.markdown-body :global(ol) {
		margin: 4px 0;
		padding-left: 20px;
	}

	.markdown-body :global(li) { margin: 2px 0; }

	.markdown-body :global(h1),
	.markdown-body :global(h2),
	.markdown-body :global(h3),
	.markdown-body :global(h4) {
		margin: 8px 0 4px;
		font-size: 14px;
	}

	.markdown-body :global(h1) { font-size: 16px; }
	.markdown-body :global(h2) { font-size: 15px; }
	.markdown-body :global(strong) { color: var(--text-primary); }
	.markdown-body :global(a) { color: var(--accent); }

	.markdown-body :global(blockquote) {
		border-left: 3px solid var(--border);
		padding-left: 12px;
		color: var(--text-secondary);
		margin: 4px 0;
	}

	.markdown-body :global(table) {
		border-collapse: collapse;
		margin: 6px 0;
		font-size: 12px;
		display: block;
		overflow-x: auto;
	}

	.markdown-body :global(th),
	.markdown-body :global(td) {
		border: 1px solid var(--border);
		padding: 4px 8px;
	}

	.markdown-body :global(th) {
		background: var(--bg-tertiary);
	}

	.markdown-body-raw {
		padding: 4px 0;
		line-height: 1.6;
		color: var(--text-primary);
		font-size: 13px;
		white-space: pre-wrap;
		word-break: break-word;
	}
</style>
