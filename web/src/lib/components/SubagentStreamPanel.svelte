<script>
	import { assembleSubagentPanel } from '$lib/subagentPanel.js';
	import OutputViewer from './OutputViewer.svelte';

	let { lines = [], maxHeight = 'none' } = $props();

	/**
	 * Core assembly: accumulate streaming lines per-agent.
	 * Slot/general bodies are rendered via OutputViewer so 🔧/💭/result
	 * protocol markers still get tool boxes (not a markdown wall).
	 * See web/src/lib/subagentPanel.js.
	 */
	let panelData = $derived.by(() => assembleSubagentPanel(lines));

	// ── Auto-scroll ──
	let slotContainers = $state({});
	let generalContainer = $state(null);
	let lastScrollLengths = {};
	let lastGeneralLen = 0;

	$effect(() => {
		for (const slot of panelData.slots) {
			const el = slotContainers[slot.name];
			const len = slot.lines.length;
			if (el && len > 0 && len !== (lastScrollLengths[slot.name] || 0)) {
				lastScrollLengths[slot.name] = len;
				requestAnimationFrame(() => {
					el.scrollTop = el.scrollHeight;
				});
			}
		}
		const generalLen = panelData.generalLines.length;
		if (generalContainer && generalLen > 0 && generalLen !== lastGeneralLen) {
			lastGeneralLen = generalLen;
			requestAnimationFrame(() => {
				generalContainer.scrollTop = generalContainer.scrollHeight;
			});
		}
	});

	let hasOutput = $derived(
		panelData.generalLines.length > 0 ||
			panelData.slots.some((s) => s.lines.length > 0)
	);

	function statusColor(status) {
		switch (status) {
			// app.css palette: --success / --danger / --live (running = alive)
			case 'done': return 'var(--success)';
			case 'error': return 'var(--danger)';
			default: return 'var(--live)';
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
		{#if panelData.generalLines.length > 0}
			<div class="general-panel">
				<div class="general-header">
					<span class="general-icon">🧠</span>
					<span class="general-title">서브에이전트 통합</span>
				</div>
				<div class="general-content" bind:this={generalContainer} style="max-height: {maxHeight}">
					<OutputViewer lines={panelData.generalLines} embedded={true} />
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
							{#if slot.lines.length === 0}
								<span class="slot-idle">대기 중...</span>
							{:else}
								<OutputViewer lines={slot.lines} embedded={true} />
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
		font-size: var(--fs-xs);
	}

	/* ── General panel ── */
	.general-panel {
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-radius: 0;
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
		font-size: var(--fs-sm);
		font-weight: 600;
		color: var(--text-primary);
		flex-shrink: 0;
	}

	.general-icon { font-size: var(--fs-md); }

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
		border-radius: 0;
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
		font-size: var(--fs-xs);
		font-weight: 600;
		flex-shrink: 0;
	}

	.slot-emoji { font-size: var(--fs-base); }
	.slot-name {
		color: var(--slot-color);
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.slot-status {
		font-size: var(--fs-2xs);
		padding: 1px 5px;
		border-radius: 0;
	}

	.status-running {
		color: var(--live);
		background: var(--live-soft);
		animation: px-blink 1.5s steps(1) infinite;
	}

	.status-done {
		color: var(--success);
		background: var(--success-soft);
	}

	.status-error {
		color: var(--danger-text);
		background: var(--danger-soft);
	}

	@keyframes subagent-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	.slot-content {
		flex: 1;
		overflow-y: auto;
		padding: 8px 10px;
		font-size: var(--fs-xs);
		line-height: 1.5;
		color: var(--text-secondary);
		word-break: break-word;
		min-height: 0;
	}

	.slot-idle {
		color: var(--text-secondary);
		font-style: normal;
		font-size: var(--fs-2xs);
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
