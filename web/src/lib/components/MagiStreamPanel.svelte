<script>
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';

	let { lines = [], maxHeight = 'none' } = $props();

	const carta = new Carta({ sanitizer: DOMPurify.sanitize });

	// Persona metadata per slot — defaults, overridden by 🧩 announcement lines
	const defaultPersonaInfo = [
		{ emoji: '🔬', name: 'MELCHIOR-1', color: '#58a6ff' },
		{ emoji: '📜', name: 'BALTHASAR-2', color: '#f0883e' },
		{ emoji: '⚡', name: 'CASPER-3', color: '#56d364' },
	];

	// Dynamic persona info — updated when the orchestrator emits 🧩 lines
	let dynamicPersonaInfo = $state(null);

	let personaInfo = $derived(dynamicPersonaInfo || defaultPersonaInfo);

	let mdCache = $state({});

	function stripAnsi(text) {
		return text.replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
	}

	/**
	 * Parse a raw line into { slot, text }.
	 * slot: 0|1|2|'*'|null
	 *   - null means "no [MAGI:] prefix" → continuation of previous slot
	 *     (happens when a streaming token contained \n and the SSE handler
	 *     split it into multiple lines).
	 */
	function parseLine(raw) {
		const line = stripAnsi(raw);
		const m = line.match(/^\[MAGI:(\d|\*)\]\s?(.*)$/s);
		if (m) {
			if (m[1] === '*') return { slot: '*', text: m[2] };
			return { slot: parseInt(m[1], 10), text: m[2] };
		}
		return { slot: null, text: line };
	}

	/**
	 * Core fix: accumulate streaming tokens per-slot into ONE continuous
	 * string instead of rendering each token as a separate <div>.
	 *
	 * Each SSE event delivers a small token fragment prefixed with [MAGI:N].
	 * Previously every fragment became its own DOM element, producing output
	 * like:
	 *   # 번
	 *   역 단
	 *   위 변경
	 *
	 * Now we concatenate all fragments for a given slot so they read as:
	 *   # 번역 단위 변경
	 */
	let panelData = $derived.by(() => {
		const proposerTexts = ['', '', ''];
		const proposerSummaries = ['', '', ''];
		const unifiedLines = [];
		const completed = [false, false, false];
		let lastSlot = '*';

		for (const raw of lines) {
			const { slot, text } = parseLine(raw);

			if (slot === null) {
				// Continuation — append to whichever slot was last active.
				if (lastSlot === '*') {
					if (unifiedLines.length > 0) {
						unifiedLines[unifiedLines.length - 1] += '\n' + text;
					} else {
						unifiedLines.push(text);
					}
				} else if (lastSlot >= 0 && lastSlot <= 2 && !completed[lastSlot]) {
					proposerTexts[lastSlot] += '\n' + text;
				}
				continue;
			}

			if (slot === '*') {
				lastSlot = '*';
				unifiedLines.push(text);
			} else if (slot >= 0 && slot <= 2) {
				lastSlot = slot;

				if (text.includes('응답 완료') || text.includes('응답 실패')) {
					completed[slot] = true;
					proposerSummaries[slot] += (proposerSummaries[slot] ? '\n' : '') + text.trim();
				} else if (!completed[slot]) {
					// Raw token fragment — concatenate directly (no separator).
					proposerTexts[slot] += text;
				}
			}
		}

		return { proposerTexts, proposerSummaries, unifiedLines, completed };
	});

	// ── Dynamic persona name detection ──
	// The orchestrator emits "[MAGI:N] 🧩 <emoji> <name>" lines at startup
	// to announce each proposer's display name. Parse these to override the
	// default personaInfo so custom names appear in panel headers.
	$effect(() => {
		for (const raw of lines) {
			const line = stripAnsi(raw);
			const m = line.match(/^\[MAGI:(\d)\]\s*🧩\s+(\S+)\s+(.+)$/);
			if (m) {
				const slot = parseInt(m[1], 10);
				const emoji = m[2];
				const name = m[3].trim();
				if (slot >= 0 && slot <= 2) {
					if (!dynamicPersonaInfo) {
						dynamicPersonaInfo = defaultPersonaInfo.map(p => ({ ...p }));
					}
					if (dynamicPersonaInfo[slot].name !== name || dynamicPersonaInfo[slot].emoji !== emoji) {
						dynamicPersonaInfo[slot] = { ...dynamicPersonaInfo[slot], emoji, name };
					}
				}
			}
		}
	});

	// ── Markdown rendering cache ──
	function hashText(t) {
		let h = 0;
		for (let i = 0; i < t.length; i++) h = ((h << 5) - h + t.charCodeAt(i)) | 0;
		return 'md_' + h + '_' + t.length;
	}

	async function ensureRendered(text) {
		if (!text || !text.trim()) return;
		const key = hashText(text);
		if (mdCache[key] !== undefined) return;
		mdCache[key] = null;
		try {
			const html = await carta.render(text);
			mdCache[key] = html;
		} catch {
			mdCache[key] = text;
		}
	}

	function getRendered(text) {
		return mdCache[hashText(text)] ?? null;
	}

	// Render markdown for unified panel entries and completed proposer texts.
	$effect(() => {
		for (const t of panelData.unifiedLines) {
			ensureRendered(t);
		}
		for (let i = 0; i < 3; i++) {
			if (panelData.completed[i]) {
				ensureRendered(panelData.proposerTexts[i]);
			}
		}
	});

	// ── Auto-scroll ──
	let propContainers = $state([null, null, null]);
	let unifiedContainer = $state(null);

	$effect(() => {
		for (let i = 0; i < 3; i++) {
			const el = propContainers[i];
			if (el && panelData.proposerTexts[i].length > 0) {
				requestAnimationFrame(() => {
					el.scrollTop = el.scrollHeight;
				});
			}
		}
		if (unifiedContainer && panelData.unifiedLines.length > 0) {
			requestAnimationFrame(() => {
				unifiedContainer.scrollTop = unifiedContainer.scrollHeight;
			});
		}
	});

	let hasProposerOutput = $derived(
		panelData.proposerTexts.some((t) => t.length > 0)
	);
</script>

<div class="magi-panel">
	<!-- Proposer panels (top row) -->
	{#if hasProposerOutput}
		<div class="proposer-row">
			{#each personaInfo as persona, i}
				<div class="proposer-panel" style="--persona-color: {persona.color}">
					<div class="proposer-header">
						<span class="proposer-emoji">{persona.emoji}</span>
						<span class="proposer-name">{persona.name}</span>
						{#if panelData.completed[i]}
							<span class="proposer-status done">✓</span>
						{:else if panelData.proposerTexts[i].length > 0}
							<span class="proposer-status streaming">●</span>
						{/if}
					</div>
					<div class="proposer-content" bind:this={propContainers[i]}>
						{#if panelData.proposerTexts[i].length === 0}
							<span class="proposer-idle">대기 중...</span>
						{:else}
							{#if panelData.completed[i]}
								{@const html = getRendered(panelData.proposerTexts[i])}
								{#if html}
									<div class="proposer-md markdown-body">{@html html}</div>
								{:else}
									<div class="proposer-text">{panelData.proposerTexts[i]}</div>
								{/if}
								{#if panelData.proposerSummaries[i]}
									<div class="proposer-summary" class:fail="{panelData.proposerSummaries[i].includes('응답 실패')}">
										{panelData.proposerSummaries[i]}
									</div>
								{/if}
							{:else}
								<div class="proposer-text">{panelData.proposerTexts[i]}</div>
							{/if}
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<!-- Unified panel (bottom) -->
	<div class="unified-panel">
		<div class="unified-header">
			<span class="unified-icon">🧠</span>
			<span class="unified-title">MAGI 심의</span>
		</div>
		<div class="unified-content" bind:this={unifiedContainer} style="max-height: {maxHeight}">
			{#if panelData.unifiedLines.length === 0 && !hasProposerOutput}
				<span class="empty-output">No output yet</span>
			{:else}
				{#each panelData.unifiedLines as text, i (i)}
					{@const html = getRendered(text)}
					{#if html}
						<div class="block-text markdown-body">{@html html}</div>
					{:else if text.trim()}
						<div class="block-text-raw">{text}</div>
					{/if}
				{/each}
			{/if}
		</div>
	</div>
</div>

<style>
	.magi-panel {
		display: flex;
		flex-direction: column;
		gap: 8px;
		height: 100%;
		min-height: 0;
	}

	/* ── Proposer row ── */
	.proposer-row {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: 8px;
		flex-shrink: 0;
	}

	.proposer-panel {
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-top: 3px solid var(--persona-color);
		border-radius: var(--radius);
		display: flex;
		flex-direction: column;
		min-height: 180px;
		max-height: 280px;
		overflow: hidden;
	}

	.proposer-header {
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

	.proposer-emoji { font-size: 14px; }
	.proposer-name { color: var(--persona-color); flex: 1; }

	.proposer-status {
		font-size: 11px;
		padding: 1px 5px;
		border-radius: 3px;
	}

	.proposer-status.done {
		color: var(--success, #56d364);
		background: rgba(86, 211, 100, 0.15);
	}

	.proposer-status.streaming {
		color: var(--warning, #f0883e);
		background: rgba(240, 136, 62, 0.15);
		animation: pulse 1.5s ease-in-out infinite;
	}

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	.proposer-content {
		flex: 1;
		overflow-y: auto;
		padding: 8px 10px;
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 1.5;
		color: var(--text-secondary);
		word-break: break-word;
		min-height: 0;
	}

	.proposer-idle {
		color: var(--text-secondary);
		font-style: italic;
		font-size: 11px;
	}

/* Streaming text — monospace pre-wrap so newlines & spaces are preserved */
.proposer-text {
white-space: pre-wrap;
word-break: break-word;
color: var(--text-primary);
}

/* Completed text rendered as markdown */
.proposer-md {
font-size: 12px;
line-height: 1.5;
color: var(--text-primary);
word-break: break-word;
}
.proposer-md :global(p) { margin: 4px 0; }
.proposer-md :global(pre) {
background: var(--bg-secondary);
border: 1px solid var(--border);
border-radius: var(--radius);
padding: 6px 8px;
margin: 4px 0;
font-size: 11px;
overflow-x: auto;
}
.proposer-md :global(code) {
background: var(--bg-tertiary);
padding: 1px 4px;
border-radius: 3px;
font-size: 11px;
}
.proposer-md :global(pre code) { background: none; padding: 0; }
.proposer-md :global(ul),
.proposer-md :global(ol) { margin: 4px 0; padding-left: 18px; }
.proposer-md :global(li) { margin: 2px 0; }
.proposer-md :global(h1),
.proposer-md :global(h2),
.proposer-md :global(h3),
.proposer-md :global(h4) { margin: 6px 0 4px; font-size: 13px; }
.proposer-md :global(strong) { color: var(--text-primary); }
.proposer-md :global(blockquote) {
border-left: 3px solid var(--border);
padding-left: 10px;
color: var(--text-secondary);
margin: 4px 0;
}

.proposer-summary {
margin-top: 6px;
padding: 4px 6px;
border-radius: var(--radius);
background: var(--bg-tertiary);
font-size: 11px;
font-weight: 600;
color: var(--text-primary);
white-space: pre-wrap;
}

.proposer-summary.fail {
color: var(--danger, #f85149);
}

/* ── Unified panel ── */
.unified-panel {
background: var(--bg-primary);
border: 1px solid var(--border);
border-radius: var(--radius);
display: flex;
flex-direction: column;
flex: 1;
min-height: 0;
}

.unified-header {
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

.unified-icon { font-size: 15px; }

.unified-content {
flex: 1;
overflow-y: auto;
padding: 12px;
display: flex;
flex-direction: column;
gap: 4px;
min-width: 0;
}

.block-text {
padding: 4px
;
		line-height: 1.6;
		color: var(--text-primary);
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

	.empty-output {
		color: var(--text-secondary);
		font-size: 12px;
	}

	/* Responsive: stack panels on narrow screens */
	@media (max-width: 768px) {
		.proposer-row {
			grid-template-columns: 1fr;
		}

		.proposer-panel {
			max-height: 200px;
		}
	}
</style>
