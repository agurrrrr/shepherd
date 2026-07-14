<script>
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';
	import { assembleSubagentPanel } from '$lib/subagentPanel.js';
	import { createMdStreamCache } from '$lib/mdStream.js';

	let { lines = [], maxHeight = 'none' } = $props();

	const carta = new Carta({ sanitizer: DOMPurify.sanitize });

	/**
	 * Core assembly: accumulate streaming lines per-agent into continuous
	 * strings with newlines. Pure function — no mutable state across calls.
	 * See web/src/lib/subagentPanel.js.
	 */
	let panelData = $derived.by(() => assembleSubagentPanel(lines));

	// Sticky MD stream cache — same anti-flicker path as OutputViewer (#7274).
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
		void mdTick;
		return mdStream.resolve(id, text);
	}

	// Render completed slots and general messages ASAP.
	// Still-streaming slots: debounce so we don't thrash carta on every token.
	$effect(() => {
		if (panelData.general && panelData.general.trim()) {
			ensureRendered(panelData.general, 'general');
		}
		const timers = [];
		for (const slot of panelData.slots) {
			if (!slot.text || !slot.text.trim()) continue;
			const id = 'slot:' + slot.name;
			if (slot.status !== 'running') {
				ensureRendered(slot.text, id);
			} else {
				timers.push(setTimeout(() => ensureRendered(slot.text, id), 280));
			}
		}
		return () => {
			for (const t of timers) clearTimeout(t);
		};
	});

	// ── Auto-scroll ──
	let slotContainers = $state({});
	let generalContainer = $state(null);
	let lastScrollLengths = {};
	let lastGeneralLen = 0;

	$effect(() => {
		for (const slot of panelData.slots) {
			const el = slotContainers[slot.name];
			const len = slot.text.length;
			if (el && len > 0 && len !== (lastScrollLengths[slot.name] || 0)) {
				lastScrollLengths[slot.name] = len;
				requestAnimationFrame(() => {
					el.scrollTop = el.scrollHeight;
				});
			}
		}
		const generalLen = panelData.general.length;
		if (generalContainer && generalLen > 0 && generalLen !== lastGeneralLen) {
			lastGeneralLen = generalLen;
			requestAnimationFrame(() => {
				generalContainer.scrollTop = generalContainer.scrollHeight;
			});
		}
	});

	let hasOutput = $derived(
		panelData.general.length > 0 ||
			panelData.slots.some((s) => s.text.length > 0)
	);

	function statusColor(status) {
		switch (status) {
			case 'done': return '#22c55e';
			case 'error': return '#ef4444';
			default: return '#3b82f6';
		}
	}

	function statusIcon(status) {
		switch (status) {
			case 'done': return '✓';
			case 'error': return '✗';
			default: return '●';
		}
	}
</script>

<div class="subagent-panel">
	{#if !hasOutput}
		<span class="empty-output">No output yet</span>
	{:else}
		{#if panelData.general}
			<div class="general-panel">
				<div class="general-header">
					<span class="general-icon">🧠</span>
					<span class="general-title">서브에이전트 통합</span>
				</div>
				<div class="general-content" bind:this={generalContainer} style="max-height: {maxHeight}">
					{#if viewFor('general', panelData.general).kind === 'exact'}
						{@const view = viewFor('general', panelData.general)}
						<div class="block-text markdown-body">{@html view.html}</div>
					{:else if viewFor('general', panelData.general).kind === 'sticky'}
						{@const view = viewFor('general', panelData.general)}
						<div class="block-text markdown-body">
							{@html view.html}<span class="md-stream-tail">{view.tail}</span>
						</div>
					{:else if panelData.general.trim()}
						<div class="block-text-raw">{panelData.general}</div>
					{/if}
				</div>
			</div>
		{/if}

		{#if panelData.slots.length > 0}
			<div class="slots-row" style="--slot-count: {panelData.slots.length}">
				{#each panelData.slots as slot (slot.name)}
					<div
						class="slot-panel"
						style="--slot-color: {statusColor(slot.status)}"
					>
						<div class="slot-header">
							<span class="slot-emoji">🔍</span>
							<span class="slot-name">{slot.name}</span>
							<span class="slot-status status-{slot.status}">
								{statusIcon(slot.status)}
							</span>
						</div>
						<div class="slot-content" bind:this={slotContainers[slot.name]}>
							{#if slot.text.length === 0}
								<span class="slot-idle">대기 중...</span>
							{:else}
								{@const view = viewFor('slot:' + slot.name, slot.text)}
								{#if view.kind === 'exact'}
									<div class="slot-md markdown-body">{@html view.html}</div>
								{:else if view.kind === 'sticky'}
									<div class="slot-md markdown-body">
										{@html view.html}<span class="md-stream-tail">{view.tail}</span>
									</div>
								{:else}
									<div class="slot-text-raw">{slot.text}</div>
								{/if}
							{/if}
						</div>
					</div>
				{/each}
			</div>
		{/if}
	{/if}
</div>

<style>
	.subagent-panel {
		display: flex;
		flex-direction: column;
		gap: 8px;
		height: 100%;
		min-height: 0;
	}

	.empty-output {
		color: var(--text-secondary);
		font-size: 12px;
	}

	/* ── General panel ── */
	.general-panel {
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		display: flex;
		flex-direction: column;
		flex: 1 1 50%;
		min-height: 120px;
	}

	.general-header {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 12px;
		background: var(--bg-tertiary);
		border-bottom: 1px solid var(--border);
		font-size: 13px;
		font-weight: 600;
		color: var(--text-primary);
		flex-shrink: 0;
	}

	.general-icon { font-size: 15px; }

	.general-content {
		flex: 1;
		overflow-y: auto;
		padding: 12px;
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;
	}

	/* ── Slot row ── */
	.slots-row {
		display: grid;
		grid-template-columns: repeat(var(--slot-count), 1fr);
		gap: 8px;
		flex: 1 1 50%;
		min-height: 120px;
	}

	.slot-panel {
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-top: 3px solid var(--slot-color);
		border-radius: var(--radius);
		display: flex;
		flex-direction: column;
		min-height: 0;
		max-height: 100%;
	}

	.slot-header {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 10px;
		background: var(--bg-tertiary);
		border-bottom: 1px solid var(--border);
		font-size: 12px;
		font-weight: 600;
		flex-shrink: 0;
	}

	.slot-emoji { font-size: 14px; }
	.slot-name {
		color: var(--slot-color);
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.slot-status {
		font-size: 11px;
		padding: 1px 5px;
		border-radius: 3px;
	}

	.status-running {
		color: #3b82f6;
		background: rgba(59, 130, 246, 0.15);
		animation: subagent-pulse 1.5s ease-in-out infinite;
	}

	.status-done {
		color: #22c55e;
		background: rgba(34, 197, 94, 0.15);
	}

	.status-error {
		color: #ef4444;
		background: rgba(239, 68, 68, 0.15);
	}

	@keyframes subagent-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	.slot-content {
		flex: 1;
		overflow-y: auto;
		padding: 8px 10px;
		font-family: var(--font-sans, system-ui, sans-serif);
		font-size: 12px;
		line-height: 1.5;
		color: var(--text-secondary);
		word-break: break-word;
		min-height: 0;
	}

	.slot-idle {
		color: var(--text-secondary);
		font-style: italic;
		font-size: 11px;
	}

	/* Streaming fallback — monospace pre-wrap */
	.slot-text-raw {
		white-space: pre-wrap;
		word-break: break-word;
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: 12px;
	}

	/* Rendered markdown */
	.slot-md {
		font-size: 12px;
		line-height: 1.55;
		color: var(--text-primary);
		background: transparent;
		word-break: break-word;
	}
	.slot-md :global(p) { margin: 4px 0; }
	.slot-md :global(pre) {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 6px 8px;
		margin: 4px 0;
		font-size: 11px;
		overflow-x: auto;
	}
	.slot-md :global(code) {
		background: var(--bg-tertiary);
		padding: 1px 4px;
		border-radius: 3px;
		font-size: 11px;
	}
	.slot-md :global(pre code) { background: none; padding: 0; }
	.slot-md :global(ul),
	.slot-md :global(ol) { margin: 4px 0; padding-left: 18px; }
	.slot-md :global(li) { margin: 2px 0; }
	.slot-md :global(h1),
	.slot-md :global(h2),
	.slot-md :global(h3),
	.slot-md :global(h4) { margin: 6px 0 4px; font-size: 13px; }
	.slot-md :global(strong) { color: var(--text-primary); }
	.slot-md :global(blockquote) {
		border-left: 3px solid var(--border);
		padding-left: 10px;
		color: var(--text-secondary);
		margin: 4px 0;
	}
	.slot-md :global(table) {
		border-collapse: collapse;
		margin: 6px 0;
		font-size: 11px;
		display: block;
		overflow-x: auto;
		width: 100%;
	}
	.slot-md :global(th),
	.slot-md :global(td) {
		border: 1px solid var(--border);
		padding: 3px 6px;
	}
	.slot-md :global(th) {
		background: var(--bg-tertiary);
	}

	/* ── General content markdown ── */
	.block-text {
		padding: 4px 0;
		line-height: 1.6;
		color: var(--text-primary);
		background: transparent;
		word-break: break-word;
		min-width: 0;
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

	.block-text-raw {
		padding: 4px 0;
		line-height: 1.6;
		color: var(--text-primary);
		font-size: 13px;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.md-stream-tail {
		white-space: pre-wrap;
		word-break: break-word;
	}

	/* Responsive: stack panels vertically on narrow portrait screens */
	@media (max-width: 768px) and (max-aspect-ratio: 4/5) {
		.slots-row {
			grid-template-columns: 1fr;
		}

		.slot-panel {
			max-height: none;
		}
	}
</style>
