<script>
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';

	let { lines = [], maxHeight = '500px' } = $props();
	let container;

	const carta = new Carta({ sanitizer: DOMPurify.sanitize });

	// Cache: text content hash → rendered HTML
	let mdCache = $state({});

	function stripAnsi(text) {
		return text.replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
	}

	function classifyLine(raw) {
		const line = stripAnsi(raw);
		if (/^[🟠🟢🔵⚪]\s/.test(line)) return 'sheep';
		if (line.startsWith('🚀 ')) return 'status';
		if (line.startsWith('✅ ')) return 'status';
		if (line.startsWith('⏸')) return 'status';
		if (line.startsWith('🔧 ')) return 'tool';
		if (line.startsWith('❓')) return 'question';
		if (line.startsWith('  ▸ ')) return 'question-option';
		if (/^\s{2,}/.test(line)) return 'result';
		return 'text';
	}

	function groupLines(lines) {
		const blocks = [];
		let currentResult = null;
		let currentText = null;
		let currentQuestion = null;

		function flushAll() {
			if (currentText) { blocks.push(currentText); currentText = null; }
			if (currentResult) { blocks.push(currentResult); currentResult = null; }
			if (currentQuestion) { blocks.push(currentQuestion); currentQuestion = null; }
		}

		for (const raw of lines) {
			const type = classifyLine(raw);
			const text = stripAnsi(raw);

			if (type === 'question') {
				// Flush others, start new question block
				if (currentText) { blocks.push(currentText); currentText = null; }
				if (currentResult) { blocks.push(currentResult); currentResult = null; }
				if (currentQuestion) { blocks.push(currentQuestion); }
				// Parse: "❓ [Header] Question?" or "❓ Question?"
				const hm = text.match(/^❓\s+\[(.+?)\]\s+(.+)/);
				currentQuestion = {
					type: 'question',
					header: hm ? hm[1] : '',
					question: hm ? hm[2] : text.replace(/^❓\s*/, ''),
					options: []
				};
			} else if (type === 'question-option' && currentQuestion) {
				// Parse: "  ▸ Label — Description" or "  ▸ Label"
				const om = text.match(/^\s+▸\s+(.+?)\s+—\s+(.+)/);
				if (om) {
					currentQuestion.options.push({ label: om[1], description: om[2] });
				} else {
					currentQuestion.options.push({ label: text.replace(/^\s+▸\s+/, ''), description: '' });
				}
			} else if (type === 'result') {
				if (currentQuestion) { blocks.push(currentQuestion); currentQuestion = null; }
				if (currentText) { blocks.push(currentText); currentText = null; }
				if (currentResult) {
					currentResult.lines.push(text);
				} else {
					currentResult = { type: 'result', lines: [text] };
				}
			} else if (type === 'text') {
				if (currentQuestion) { blocks.push(currentQuestion); currentQuestion = null; }
				if (currentResult) { blocks.push(currentResult); currentResult = null; }
				if (currentText) {
					currentText.text += '\n' + text;
				} else {
					currentText = { type: 'text', text };
				}
			} else {
				flushAll();
				blocks.push({ type, text });
			}
		}
		flushAll();
		return blocks;
	}

	// Simple hash for cache key
	function hashText(t) {
		let h = 0;
		for (let i = 0; i < t.length; i++) h = ((h << 5) - h + t.charCodeAt(i)) | 0;
		return 'md_' + h + '_' + t.length;
	}

	// Render markdown and cache result
	async function ensureRendered(text) {
		const key = hashText(text);
		if (mdCache[key] !== undefined) return;
		mdCache[key] = null; // mark as pending
		try {
			const html = await carta.render(text);
			mdCache[key] = html;
		} catch {
			mdCache[key] = text;
		}
	}

	function getRendered(text) {
		return mdCache[hashText(text)] || null;
	}

	let blocks = $derived(groupLines(lines));

	// Trigger markdown rendering for text blocks
	$effect(() => {
		for (const b of blocks) {
			if (b.type === 'text' && b.text.trim()) {
				ensureRendered(b.text);
			}
		}
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
		if (text.includes('TodoWrite')) return '☑️';
		if (text.includes('Bash')) return '⌨️';
		if (text.includes('Read')) return '📖';
		if (text.includes('Edit')) return '✏️';
		if (text.includes('Write')) return '📝';
		if (text.includes('Grep')) return '🔍';
		if (text.includes('Glob')) return '📂';
		if (text.includes('Task')) return '📋';
		if (text.includes('AskUserQuestion')) return '❓';
		return '🔧';
	}

	function getToolLabel(text) {
		const m = text.match(/^🔧\s+(\S+)(?:\s+→\s+(.*))?/);
		if (m) return { name: m[1], detail: m[2] || '' };
		return { name: text.replace('🔧 ', ''), detail: '' };
	}
</script>

<div class="output-viewer" style="max-height: {maxHeight}" bind:this={container}>
	{#each blocks as block, i (i)}
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

		{:else if block.type === 'text'}
			{@const html = getRendered(block.text)}
			{#if html}
				<div class="block-text markdown-body">{@html html}</div>
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
		flex-shrink: 0;
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
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.block-result code {
		background: none;
		padding: 0;
		font-size: inherit;
	}

	/* AI text: markdown rendered */
	.block-text {
		padding: 4px 0;
		font-size: 13px;
		line-height: 1.6;
		color: var(--text-primary);
		overflow-x: auto;
		word-break: break-word;
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

	.empty-output {
		color: var(--text-secondary);
		font-size: 12px;
	}
</style>
