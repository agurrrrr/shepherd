<script>
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';
	import { assembleMagiPanel } from '$lib/magiPanel.js';

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

	/**
	 * Core assembly (task #7270 / #7273): accumulate streaming lines per-slot
	 * into ONE continuous string WITH newlines between successive complete lines.
	 *
	 * - #7270: joining WITHOUT a separator glued headings/tables in model cards.
	 * - #7273: DB-persisted lines keep trailing '\n'; re-joining without stripping
	 *   invented blank lines between every row and broke GFM tables in the
	 *   MAGI 심의 box. assembleMagiPanel normalizes trailing NL then re-joins.
	 * See web/src/lib/magiPanel.js.
	 */
	let panelData = $derived.by(() => assembleMagiPanel(lines));

	// ── Dynamic persona name detection ──
	// The orchestrator emits "[MAGI:N] 🧩 <emoji> <name>" lines at startup
	// to announce each proposer's display name. Parse these to override the
	// default personaInfo so custom names appear in panel headers.
	$effect(() => {
		for (const raw of lines) {
			const line = String(raw ?? '').replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
			const m = line.match(/^\[MAGI:(\d)\]\s*🧩\s+(\S+)\s+(.+)$/);
			if (m) {
				const slot = parseInt(m[1], 10);
				const emoji = m[2];
				const name = m[3].trim();
				if (slot >= 0 && slot <= 2) {
					if (!dynamicPersonaInfo) {
						dynamicPersonaInfo = defaultPersonaInfo.map((p) => ({ ...p }));
					}
					if (
						dynamicPersonaInfo[slot].name !== name ||
						dynamicPersonaInfo[slot].emoji !== emoji
					) {
						dynamicPersonaInfo[slot] = {
							...dynamicPersonaInfo[slot],
							emoji,
							name,
						};
					}
				}
			}
		}
	});

	// ── Markdown rendering cache (LRU-capped, task #7209) ──
	const MD_CACHE_LIMIT = 100;
	let mdCacheKeys = [];

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
		mdCacheKeys.push(key);
		while (mdCacheKeys.length > MD_CACHE_LIMIT) {
			const oldKey = mdCacheKeys.shift();
			delete mdCache[oldKey];
		}
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

	// Render markdown for unified panel entries and all proposer texts
	// (including in-progress). Completed cards always want MD; streaming
	// cards upgrade from pre-wrap once the async render finishes.
	// Throttle: only re-render when the text changes (keyed by hash).
	$effect(() => {
		for (const t of panelData.unifiedLines) {
			ensureRendered(t);
		}
		for (let i = 0; i < 3; i++) {
			const t = panelData.proposerTexts[i];
			if (t && t.trim()) {
				ensureRendered(t);
			}
		}
	});

	// ── Auto-scroll ──
	// Only trigger scroll when the actual content length changes, not on
	// every panelData recompute (which fires for every SSE token even when
	// the proposer text is unchanged due to completed[] guard).
	let propContainers = $state([null, null, null]);
	let unifiedContainer = $state(null);
	let lastScrollLengths = [0, 0, 0];
	let lastUnifiedLen = 0;

	$effect(() => {
		for (let i = 0; i < 3; i++) {
			const el = propContainers[i];
			const len = panelData.proposerTexts[i].length;
			if (el && len > 0 && len !== lastScrollLengths[i]) {
				lastScrollLengths[i] = len;
				requestAnimationFrame(() => {
					el.scrollTop = el.scrollHeight;
				});
			}
		}
		const unifiedLen = panelData.unifiedLines.length;
		if (unifiedContainer && unifiedLen > 0 && unifiedLen !== lastUnifiedLen) {
			lastUnifiedLen = unifiedLen;
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
							{@const html = getRendered(panelData.proposerTexts[i])}
							{#if html}
								<div class="proposer-md markdown-body">{@html html}</div>
							{:else}
								<div class="proposer-text">{panelData.proposerTexts[i]}</div>
							{/if}
							{#if panelData.proposerSummaries[i]}
								<div
									class="proposer-summary"
									class:fail={panelData.proposerSummaries[i].includes('응답 실패')}
								>
									{panelData.proposerSummaries[i]}
								</div>
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
	/* Takes ~50% of the panel height; the unified (MAGI review) panel
	   takes the other ~50% so its content stays visible even when
	   streaming output grows long in the proposer cards. */
	.proposer-row {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: 8px;
		flex: 1 1 50%;
		min-height: 120px;
		overflow: hidden;
	}

	.proposer-panel {
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-top: 3px solid var(--persona-color);
		border-radius: var(--radius);
		display: flex;
		flex-direction: column;
		min-height: 0;
		max-height: 100%;
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
		font-family: var(--font-sans, system-ui, sans-serif);
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

	/* Streaming fallback — monospace pre-wrap so newlines & spaces are preserved */
	.proposer-text {
		white-space: pre-wrap;
		word-break: break-word;
		color: var(--text-primary);
		font-family: var(--font-mono);
	}

	/* Completed / rendered markdown */
	.proposer-md {
		font-size: 12px;
		line-height: 1.55;
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
	.proposer-md :global(table) {
		border-collapse: collapse;
		margin: 6px 0;
		font-size: 11px;
		display: block;
		overflow-x: auto;
		width: 100%;
	}
	.proposer-md :global(th),
	.proposer-md :global(td) {
		border: 1px solid var(--border);
		padding: 3px 6px;
	}
	.proposer-md :global(th) {
		background: var(--bg-tertiary);
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
	/* Takes ~50% of the panel height so MAGI review output is always
	   visible regardless of how tall the proposer cards grow. */
	.unified-panel {
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		display: flex;
		flex-direction: column;
		flex: 1 1 50%;
		min-height: 120px;
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
		padding: 4px 0;
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

	/* Responsive: stack panels vertically only on clearly portrait narrow screens.
	   When aspect ratio is close to 1:1 (square-ish) or landscape, keep horizontal layout
	   so the three proposer cards stay side by side. */
	@media (max-width: 768px) and (max-aspect-ratio: 4/5) {
		.proposer-row {
			grid-template-columns: 1fr;
		}

		.proposer-panel {
			max-height: none;
		}
	}
</style>
