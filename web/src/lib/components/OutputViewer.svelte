<script>
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';
	import { groupLines } from '$lib/outputLines.js';
	import { createMdStreamCache } from '$lib/mdStream.js';

	let { lines = [], maxHeight = '500px' } = $props();
	let container;

	const carta = new Carta({ sanitizer: DOMPurify.sanitize });

	// Sticky stream cache: avoids raw↔HTML flicker while Grok (or any
	// high-frequency provider) grows the last text block token-by-token.
	// See web/src/lib/mdStream.js and wiki grok-live-output-fragmentation.
	// `tick` forces Svelte to re-read resolve() after async renders land.
	let mdTick = $state(0);
	const mdStream = createMdStreamCache({
		limit: 100,
		render: (text) => carta.render(text),
	});

	async function ensureRendered(text, id) {
		await mdStream.ensure(text, id);
		mdTick++;
	}

	function viewFor(id, text) {
		// Depend on mdTick so sticky/exact updates re-render the block.
		void mdTick;
		return mdStream.resolve(id, text);
	}

	let blocks = $derived(groupLines(lines));

	// Sealed text/thinking blocks render immediately; the growing tail is
	// debounced. While waiting, resolve() shows sticky HTML + plain tail
	// (no mode flip). Thinking shares the same sticky path so 💭 streams
	// do not flicker raw↔HTML either.
	$effect(() => {
		const mdBlocks = [];
		for (let i = 0; i < blocks.length; i++) {
			const b = blocks[i];
			if ((b.type === 'text' || b.type === 'thinking') && b.text.trim()) {
				mdBlocks.push({ id: b.type + ':' + i, text: b.text });
			}
		}
		for (let i = 0; i < mdBlocks.length - 1; i++) {
			ensureRendered(mdBlocks[i].text, mdBlocks[i].id);
		}
		const last = mdBlocks[mdBlocks.length - 1];
		if (!last) return;
		const t = setTimeout(() => ensureRendered(last.text, last.id), 280);
		return () => clearTimeout(t);
	});

	// Auto-scroll
	$effect(() => {
		if (lines.length && container) {
			requestAnimationFrame(() => {
				container.scrollTop = container.scrollHeight;
			});
		}
	});

	function getToolIcon(text) {
		// Lowercased so it matches both PascalCase (Claude/opencode: Bash, Read)
		// and the embedded provider's snake_case names (bash, read_file, ...).
		const t = text.toLowerCase();
		if (t.includes('todowrite')) return '☑️';
		if (t.includes('bash')) return '⌨️';
		if (t.includes('read')) return '📖';
		if (t.includes('edit')) return '✏️';
		if (t.includes('write')) return '📝';
		if (t.includes('grep')) return '🔍';
		if (t.includes('glob')) return '📂';
		if (t.includes('task')) return '📋';
		if (t.includes('askuserquestion')) return '❓';
		return '🔧';
	}

	function getToolLabel(text) {
		const m = text.match(/^🔧\s+(\S+)(?:\s+→\s+(.*))?/);
		if (m) return { name: m[1], detail: m[2] || '' };
		return { name: text.replace('🔧 ', ''), detail: '' };
	}
</script>

<div class="output-viewer" style="max-height: {maxHeight}" bind:this={container}>
	{#each blocks as block, i (block.type + ':' + i)}
		{#if block.type === 'sheep'}
			<div class="block-sheep">{block.text.trim()}</div>

		{:else if block.type === 'status'}
			<div class="block-status">{block.text.trim()}</div>

		{:else if block.type === 'tool'}
			{@const tool = getToolLabel(block.text)}
			<div class="block-tool">
				<span class="tool-icon">{getToolIcon(block.text)}</span>
				<span class="tool-name">{tool.name}</span>
				{#if tool.detail}
					<span class="tool-detail">{tool.detail}</span>
				{/if}
			</div>

		{:else if block.type === 'question'}
			<div class="block-question">
				<div class="question-header">
					<span class="question-icon">❓</span>
					{#if block.header}
						<span class="question-tag">{block.header}</span>
					{/if}
					<span class="question-text">{block.question}</span>
				</div>
				{#if block.options.length > 0}
					<div class="question-options">
						{#each block.options as opt}
							<div class="question-option">
								<span class="option-label">{opt.label}</span>
								{#if opt.description}
									<span class="option-desc">{opt.description}</span>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			</div>

		{:else if block.type === 'result'}
			<pre class="block-result"><code>{block.lines.map(l => l.replace(/^\s{2,3}/, '')).join('\n')}</code></pre>

		{:else if block.type === 'thinking'}
			{@const view = viewFor('thinking:' + i, block.text)}
			<details class="block-thinking" open>
				<summary class="thinking-summary">
					<span class="thinking-icon">💭</span>
					<span class="thinking-label">Thinking</span>
				</summary>
				{#if view.kind === 'exact'}
					<div class="thinking-body markdown-body">{@html view.html}</div>
				{:else if view.kind === 'sticky'}
					<div class="thinking-body markdown-body">
						{@html view.html}<span class="md-stream-tail">{view.tail}</span>
					</div>
				{:else}
					<div class="thinking-body-raw">{block.text}</div>
				{/if}
			</details>

		{:else if block.type === 'text'}
			{@const view = viewFor('text:' + i, block.text)}
			{#if view.kind === 'exact'}
				<div class="block-text markdown-body">{@html view.html}</div>
			{:else if view.kind === 'sticky'}
				<div class="block-text markdown-body">
					{@html view.html}<span class="md-stream-tail">{view.tail}</span>
				</div>
			{:else}
				<div class="block-text-raw">{block.text}</div>
			{/if}
		{/if}
	{/each}
	{#if lines.length === 0}
		<span class="empty-output">No output yet</span>
	{/if}
</div>

<style>
	.output-viewer {
		background: var(--bg-primary);
		font-size: 13px;
		overflow-y: auto;
		overflow-x: hidden;
		max-height: 500px;
		padding: 12px;
		border-radius: var(--radius);
		border: 1px solid var(--border);
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;
	}

	.block-sheep {
		font-weight: 600;
		font-size: 14px;
		padding: 4px 0;
		color: var(--text-primary);
	}

	.block-status {
		font-size: 12px;
		color: var(--text-secondary);
		padding: 2px 0;
	}

	.block-tool {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 8px;
		margin-top: 4px;
		background: var(--bg-tertiary);
		border-radius: var(--radius);
		font-family: var(--font-mono);
		font-size: 12px;
		min-height: 28px;
		min-width: 0;
		flex-shrink: 0;
		overflow: hidden;
	}

	.tool-icon {
		font-size: 13px;
		flex-shrink: 0;
	}

	.tool-name {
		font-weight: 600;
		color: var(--accent);
		flex-shrink: 0;
	}

	.tool-detail {
		flex: 1;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	/* Question card */
	.block-question {
		margin-top: 4px;
		padding: 10px 12px;
		background: var(--bg-tertiary);
		border-left: 3px solid var(--warning);
		border-radius: 0 var(--radius) var(--radius) 0;
		min-width: 0;
		overflow: hidden;
		flex-shrink: 0;
	}

	.question-header {
		display: flex;
		align-items: baseline;
		gap: 6px;
		flex-wrap: wrap;
	}

	.question-icon {
		font-size: 14px;
		flex-shrink: 0;
	}

	.question-tag {
		font-size: 11px;
		font-weight: 600;
		color: var(--warning);
		background: rgba(210, 153, 34, 0.15);
		padding: 1px 6px;
		border-radius: 3px;
		flex-shrink: 0;
	}

	.question-text {
		font-size: 13px;
		font-weight: 600;
		color: var(--text-primary);
		line-height: 1.4;
	}

	.question-options {
		margin-top: 8px;
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding-left: 22px;
	}

	.question-option {
		display: flex;
		align-items: baseline;
		gap: 6px;
		font-size: 12px;
		line-height: 1.4;
	}

	.question-option::before {
		content: '▸';
		color: var(--warning);
		flex-shrink: 0;
		font-weight: 600;
	}

	.option-label {
		font-weight: 600;
		color: var(--accent);
		flex-shrink: 0;
	}

	.option-desc {
		color: var(--text-secondary);
	}

	.block-result {
		margin: 0;
		padding: 8px 12px;
		background: var(--bg-secondary);
		border-left: 3px solid var(--border);
		border-radius: 0 var(--radius) var(--radius) 0;
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 1.5;
		white-space: pre-wrap;
		word-break: break-word;
		overflow-wrap: break-word;
		color: var(--text-secondary);
		min-width: 0;
		max-width: 100%;
		flex-shrink: 0;
	}

	.block-result code {
		background: none;
		padding: 0;
		font-size: inherit;
		white-space: inherit;
		word-break: inherit;
		overflow-wrap: inherit;
	}

	/* Reasoning / thinking (💭) — collapsible, dimmed, markdown body */
	.block-thinking {
		margin-top: 4px;
		padding: 0;
		background: var(--bg-secondary);
		border-left: 3px solid color-mix(in srgb, var(--accent) 45%, var(--border));
		border-radius: 0 var(--radius) var(--radius) 0;
		min-width: 0;
		overflow: hidden;
		flex-shrink: 0;
	}

	.thinking-summary {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 10px;
		cursor: pointer;
		user-select: none;
		list-style: none;
		color: var(--text-secondary);
		font-size: 12px;
		font-weight: 600;
	}

	.thinking-summary::-webkit-details-marker {
		display: none;
	}

	.thinking-summary::before {
		content: '▸';
		font-size: 10px;
		color: var(--text-secondary);
		transition: transform 0.12s ease;
	}

	.block-thinking[open] > .thinking-summary::before {
		transform: rotate(90deg);
	}

	.thinking-icon {
		font-size: 13px;
		flex-shrink: 0;
	}

	.thinking-label {
		letter-spacing: 0.02em;
	}

	.thinking-body {
		padding: 0 12px 10px;
		font-size: 12.5px;
		line-height: 1.55;
		/* color + transparent bg: do not let github-markdown light scheme
		   paint a white strip under --text-secondary (OS light mode). */
		color: var(--text-secondary);
		background: transparent;
		overflow-x: hidden;
		word-break: break-word;
		min-width: 0;
	}

	.thinking-body :global(p) { margin: 3px 0; }
	.thinking-body :global(strong) { color: var(--text-primary); font-weight: 600; }
	.thinking-body :global(code) {
		background: var(--bg-tertiary);
		padding: 1px 4px;
		border-radius: 3px;
		font-size: 11.5px;
	}
	.thinking-body :global(pre) {
		background: var(--bg-tertiary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 6px 10px;
		margin: 4px 0;
		font-size: 11.5px;
		overflow-x: auto;
	}
	.thinking-body :global(pre code) {
		background: none;
		padding: 0;
	}
	.thinking-body :global(ul),
	.thinking-body :global(ol) {
		margin: 3px 0;
		padding-left: 18px;
	}

	.thinking-body-raw {
		padding: 0 12px 10px;
		font-size: 12.5px;
		line-height: 1.55;
		color: var(--text-secondary);
		white-space: pre-wrap;
		word-break: break-word;
	}

	/* AI text: markdown rendered */
	.block-text {
		padding: 4px 0;
		font-size: 13px;
		line-height: 1.6;
		color: var(--text-primary);
		background: transparent;
		overflow-x: hidden;
		word-break: break-word;
		min-width: 0;
		flex-shrink: 0;
	}

	.block-text :global(p) { margin: 4px 0; }

	.block-text :global(pre) {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 8px 12px;
		margin: 6px 0;
		font-size: 12px;
		overflow-x: auto;
	}

	.block-text :global(code) {
		background: var(--bg-tertiary);
		padding: 1px 4px;
		border-radius: 3px;
		font-size: 12px;
	}

	.block-text :global(pre code) {
		background: none;
		padding: 0;
	}

	.block-text :global(ul),
	.block-text :global(ol) {
		margin: 4px 0;
		padding-left: 20px;
	}

	.block-text :global(li) { margin: 2px 0; }

	.block-text :global(h1),
	.block-text :global(h2),
	.block-text :global(h3),
	.block-text :global(h4) {
		margin: 8px 0 4px;
		font-size: 14px;
	}

	.block-text :global(h1) { font-size: 16px; }
	.block-text :global(h2) { font-size: 15px; }
	.block-text :global(strong) { color: var(--text-primary); }
	.block-text :global(a) { color: var(--accent); }

	.block-text :global(blockquote) {
		border-left: 3px solid var(--border);
		padding-left: 12px;
		color: var(--text-secondary);
		margin: 4px 0;
	}

	.block-text :global(table) {
		border-collapse: collapse;
		margin: 6px 0;
		font-size: 12px;
		display: block;
		overflow-x: auto;
	}

	.block-text :global(th),
	.block-text :global(td) {
		border: 1px solid var(--border);
		padding: 4px 8px;
	}

	.block-text :global(th) {
		background: var(--bg-tertiary);
	}

	/* AI text: raw fallback */
	.block-text-raw {
		padding: 4px 0;
		line-height: 1.6;
		color: var(--text-primary);
		font-size: 13px;
		white-space: pre-wrap;
		word-break: break-word;
		flex-shrink: 0;
	}

	/* Unrendered streaming suffix after sticky HTML — same type metrics so
	   the tail does not jump when the next full markdown render lands. */
	.md-stream-tail {
		white-space: pre-wrap;
		word-break: break-word;
	}

	.empty-output {
		color: var(--text-secondary);
		font-size: 12px;
	}
</style>
