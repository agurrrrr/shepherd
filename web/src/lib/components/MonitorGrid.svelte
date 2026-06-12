<script>
	import { onMount, onDestroy, tick } from 'svelte';
	import { onSSE } from '$lib/sse.js';

	// `working` is the list of currently-working sheep ({ name, project, provider, status }).
	// Parent keeps it fresh via dashboard SSE refreshes; this component layers live
	// output streaming on top via its own 'output' subscription.
	export let working = [];
	export let onClose = () => {};

	let unsubscribers = [];
	// sheep name → array of streamed lines
	let outputs = $state({});

	// Viewport-driven layout
	let vw = 0;
	let vh = 0;

	// Cap at 9 tiles (3×3). Extra working sheep are noted but not rendered.
	$: tiles = (working || []).slice(0, 9);
	$: hiddenCount = Math.max(0, (working || []).length - tiles.length);

	$: grid = computeGrid(tiles.length, vw, vh);
	// Font size scales with the smaller tile dimension so text stays readable but
	// packs tightly as more tiles appear.
	$: tileW = grid.cols > 0 ? vw / grid.cols : vw;
	$: tileH = grid.rows > 0 ? vh / grid.rows : vh;
	$: fontPx = clamp(9, Math.round(Math.min(tileW / 48, tileH / 26)), 16);
	$: headerPx = clamp(10, fontPx + 1, 17);

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
	<button class="monitor-close" onclick={onClose} title="모니터링 종료 (Esc)">✕</button>

	{#if tiles.length === 0}
		<div class="monitor-empty">
			<span class="empty-dot"></span>
			작업 중인 양이 없습니다
		</div>
	{:else}
		<div
			class="monitor-grid"
			style="grid-template-columns: repeat({grid.cols}, 1fr); grid-template-rows: repeat({grid.rows}, 1fr);"
		>
			{#each tiles as s (s.name)}
				<div class="tile">
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
							<div class="tile-line l-status">대기 중…</div>
						{/each}
					</div>
				</div>
			{/each}
		</div>
		{#if hiddenCount > 0}
			<div class="monitor-more">+{hiddenCount} more (최대 9개 표시)</div>
		{/if}
	{/if}
</div>

<style>
	.monitor-overlay {
		position: fixed;
		inset: 0;
		z-index: 1000;
		background: var(--bg-1, #0b0e14);
		overflow: hidden;
	}

	.monitor-close {
		position: fixed;
		top: 6px;
		right: 8px;
		z-index: 1002;
		width: 28px;
		height: 28px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm, 4px);
		background: rgba(0, 0, 0, 0.45);
		color: var(--text-secondary);
		font-size: 14px;
		line-height: 1;
		cursor: pointer;
		opacity: 0.5;
		transition: opacity 0.15s, color 0.15s;
	}
	.monitor-close:hover {
		opacity: 1;
		color: var(--text-primary);
	}

	.monitor-grid {
		display: grid;
		width: 100vw;
		height: 100vh;
		gap: 0;
	}

	.tile {
		position: relative;
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
		border: 1px solid var(--border, #1c2230);
		background: var(--bg-2, #11151d);
		overflow: hidden;
	}

	.tile-head {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 2px 8px;
		background: var(--bg-3, #161b25);
		border-bottom: 1px solid var(--border);
		font-size: var(--header);
		line-height: 1.4;
		flex-shrink: 0;
		min-width: 0;
	}

	.tile-dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--live, #56d4dd);
		box-shadow: 0 0 6px var(--live, #56d4dd);
		flex-shrink: 0;
		animation: pulse 1.6s ease-in-out infinite;
	}

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.35; }
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
		font-size: 0.85em;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.tile-provider {
		margin-left: auto;
		font-family: var(--font-mono);
		color: var(--text-tertiary);
		font-size: 0.8em;
		text-transform: uppercase;
		letter-spacing: 0.03em;
		flex-shrink: 0;
	}

	.tile-body {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 4px 8px;
		font-family: var(--font-mono);
		font-size: var(--font);
		line-height: 1.35;
		scrollbar-width: thin;
	}
	.tile-body::-webkit-scrollbar {
		width: 5px;
	}
	.tile-body::-webkit-scrollbar-thumb {
		background: var(--border);
		border-radius: 3px;
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
		font-size: 15px;
	}
	.empty-dot {
		width: 9px;
		height: 9px;
		border-radius: 50%;
		background: var(--text-tertiary);
	}

	.monitor-more {
		position: fixed;
		bottom: 6px;
		right: 8px;
		z-index: 1002;
		padding: 2px 8px;
		font-size: 11px;
		color: var(--text-secondary);
		background: rgba(0, 0, 0, 0.45);
		border-radius: var(--radius-sm, 4px);
	}
</style>
