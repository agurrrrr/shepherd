<script>
	import { onMount, onDestroy, tick } from 'svelte';
	import { onSSE } from '$lib/sse.js';
	import { t } from '$lib/i18n/index.js';
	import Icon from './Icon.svelte';

	// `working` is the list of currently-working sheep ({ name, project, provider, status }).
	// Parent keeps it fresh via dashboard SSE refreshes; this component layers live
	// output streaming on top via its own 'output' subscription.
	let { working = [], onClose = () => {} } = $props();

	let unsubscribers = [];
	// sheep name → array of streamed lines
	let outputs = $state({});

	// Viewport-driven layout
	let vw = $state(0);
	let vh = $state(0);

	// Cap at 9 tiles (3×3). Extra working sheep are noted but not rendered.
	const tiles = $derived((working || []).slice(0, 9));
	const hiddenCount = $derived(Math.max(0, (working || []).length - tiles.length));

	const grid = $derived(computeGrid(tiles.length, vw, vh));
	// Font size scales with the smaller tile dimension so text stays readable but
	// packs tightly as more tiles appear.
	const tileW = $derived(grid.cols > 0 ? vw / grid.cols : vw);
	const tileH = $derived(grid.rows > 0 ? vh / grid.rows : vh);
	const fontPx = $derived(clamp(9, Math.round(Math.min(tileW / 48, tileH / 26)), 16));
	const headerPx = $derived(clamp(10, fontPx + 1, 17));

	function clamp(min, v, max) {
		return Math.max(min, Math.min(max, v));
	}

	// Choose cols/rows (each ≤3) that best fill the viewport: maximize tile area
	// while keeping the tile aspect ratio close to a comfortable ~1.4 landscape.
	function computeGrid(n, W, H) {
		if (n <= 0) return { cols: 1, rows: 1 };
		let best = { cols: 1, rows: n, score: -Infinity };
		const maxCols = Math.min(3, n);
		for (let cols = 1; cols <= maxCols; cols++) {
			const rows = Math.ceil(n / cols);
			if (rows > 3) continue;
			const tw = W / cols;
			const th = H / rows;
			const area = tw * th;
			const aspect = th > 0 ? tw / th : 1;
			const aspectPenalty = Math.abs(Math.log(aspect / 1.4));
			const score = area * (1 - Math.min(0.6, aspectPenalty * 0.18));
			if (score > best.score) best = { cols, rows, score };
		}
		return best;
	}

	function stripAnsi(text) {
		return (text || '').replace(/\x1B\[[0-9;]*[a-zA-Z]/g, '');
	}

	// Lightweight per-line classification for tinting (full markdown rendering is
	// skipped here — 9 live tiles need to stay cheap and dense).
	function lineClass(raw) {
		const line = stripAnsi(raw);
		if (/^[🟠🟢🔵⚪]\s/.test(line)) return 'l-sheep';
		if (/^(🚀|✅|⏸)/.test(line)) return 'l-status';
		if (line.startsWith('🔧 ')) return 'l-tool';
		if (line.startsWith('❓')) return 'l-question';
		if (/^\s{2,}/.test(line)) return 'l-result';
		return 'l-text';
	}

	function pushOutput(name, text) {
		const prev = outputs[name] || [];
		outputs[name] = [...prev.slice(-200), text];
		outputs = outputs; // trigger reactivity
		scheduleScroll(name);
	}

	// Auto-scroll each tile to the bottom on new output.
	let bodyEls = {};
	let scrollPending = {};
	function scheduleScroll(name) {
		if (scrollPending[name]) return;
		scrollPending[name] = true;
		tick().then(() => {
			scrollPending[name] = false;
			const el = bodyEls[name];
			if (el) el.scrollTop = el.scrollHeight;
		});
	}

	function onKeydown(e) {
		if (e.key === 'Escape') onClose();
	}

	function measure() {
		vw = window.innerWidth;
		vh = window.innerHeight;
	}

	onMount(() => {
		measure();
		window.addEventListener('resize', measure);
		window.addEventListener('keydown', onKeydown);

		unsubscribers.push(
			onSSE('output', (data) => {
				const key = data.sheep_name;
				if (!key) return;
				pushOutput(key, data.text);
			})
		);
	});

	onDestroy(() => {
		window.removeEventListener('resize', measure);
		window.removeEventListener('keydown', onKeydown);
		unsubscribers.forEach((fn) => fn && fn());
	});

	function lastLines(name, count) {
		const arr = outputs[name] || [];
		return arr.slice(-count).map(stripAnsi);
	}
</script>

<div class="monitor-overlay" style="--font: {fontPx}px; --header: {headerPx}px;">
	<button class="monitor-close" onclick={onClose} title={$t('monitor.closeTitle')} aria-label={$t('monitor.closeAria')}><Icon name="x" size={14} stroke={2.2} /></button>

	{#if tiles.length === 0}
		<div class="monitor-empty">
			<span class="empty-dot"></span>
			{$t('monitor.empty')}
		</div>
	{:else}
		<div
			class="monitor-grid"
			style="grid-template-columns: repeat({grid.cols}, 1fr); grid-template-rows: repeat({grid.rows}, 1fr);"
		>
			{#each tiles as s, i (s.name)}
				<!-- The top-right tile leaves room in its header for the fixed close button -->
				<div class="tile" class:corner={i === grid.cols - 1}>
					<div class="tile-head">
						<span class="tile-dot" aria-hidden="true"></span>
						<span class="tile-project">{s.project || s.name}</span>
						{#if s.project}
							<span class="tile-sheep">{s.name}</span>
						{/if}
						{#if s.provider}
							<span class="tile-provider">{s.provider}</span>
						{/if}
					</div>
					<div class="tile-body" bind:this={bodyEls[s.name]}>
						{#each lastLines(s.name, 200) as line, i (i)}
							<div class="tile-line {lineClass(line)}">{line || ' '}</div>
						{:else}
							<div class="tile-line l-status">{$t('monitor.waiting')}</div>
						{/each}
					</div>
				</div>
			{/each}
		</div>
		{#if hiddenCount > 0}
			<div class="monitor-more">{$t('monitor.more', { count: hiddenCount })}</div>
		{/if}
	{/if}
</div>

<style>
	.monitor-overlay {
		position: fixed;
		inset: 0;
		z-index: 1000;
		background: var(--bg-base);
		overflow: hidden;
		animation: fade-in var(--dur-2) ease backwards;
	}

	.monitor-close {
		position: fixed;
		top: 4px;
		right: 6px;
		z-index: 1002;
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border: 1px solid var(--border);
		border-radius: 0;
		background: var(--bg-3);
		color: var(--text-secondary);
		cursor: pointer;
		opacity: 0.6;
		transition: opacity var(--dur-1) steps(2), color var(--dur-1) steps(2), border-color var(--dur-1) steps(2);
	}

	.monitor-close:hover,
	.monitor-close:focus-visible {
		opacity: 1;
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	.tile.corner .tile-head {
		padding-right: 40px;
	}

	.monitor-grid {
		display: grid;
		width: 100vw;
		height: 100vh;
		gap: 1px;
		background: var(--border);
	}

	.tile {
		position: relative;
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
		background: var(--bg-1);
		overflow: hidden;
	}

	.tile-head {
		display: flex;
		align-items: center;
		gap: 8px;
		min-height: 34px;
		padding: 4px 10px;
		background: linear-gradient(var(--live-soft), var(--live-soft)), var(--bg-2);
		border-bottom: 1px solid var(--border-subtle);
		font-size: var(--header);
		line-height: 1.4;
		flex-shrink: 0;
		min-width: 0;
	}

	.tile-dot {
		width: 7px;
		height: 7px;
		border-radius: 0;
		background: var(--live);
		flex-shrink: 0;
		animation: px-blink 1s steps(1) infinite;
	}

	.tile-project {
		font-weight: 600;
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}

	.tile-sheep {
		font-family: var(--font-mono);
		color: var(--text-secondary);
		font-size: var(--fs-xs);
		white-space: nowrap;
		flex-shrink: 0;
	}

	.tile-provider {
		margin-left: auto;
		font-family: var(--font-mono);
		color: var(--text-tertiary);
		font-size: var(--fs-xs);
		text-transform: uppercase;
		letter-spacing: var(--tracking-caps);
		flex-shrink: 0;
	}

	.tile-body {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 8px 10px;
		font-family: var(--font-mono);
		font-size: var(--font);
		line-height: 1.4;
		scrollbar-width: thin;
	}
	.tile-body::-webkit-scrollbar {
		width: 5px;
	}
	.tile-body::-webkit-scrollbar-thumb {
		background: var(--border);
		border-radius: 0;
	}

	.tile-line {
		white-space: pre-wrap;
		word-break: break-word;
		color: var(--text-primary);
	}

	.l-sheep { font-weight: 600; color: var(--text-primary); }
	.l-status { color: var(--text-secondary); }
	.l-tool { color: var(--accent); }
	.l-question { color: var(--warning); font-weight: 600; }
	.l-result { color: var(--text-tertiary); }
	.l-text { color: var(--text-primary); }

	.monitor-empty {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 10px;
		width: 100%;
		height: 100%;
		color: var(--text-secondary);
		font-size: var(--fs-md);
	}
	.empty-dot {
		width: 9px;
		height: 9px;
		border-radius: 0;
		background: var(--text-tertiary);
	}

	.monitor-more {
		position: fixed;
		bottom: 6px;
		right: 8px;
		z-index: 1002;
		padding: 2px 9px;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-secondary);
		background: var(--bg-3);
		border: 1px solid var(--border);
		border-radius: 0;
	}
</style>
