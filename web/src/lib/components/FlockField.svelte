<script>
	// The flock at a glance: every sheep is a pixel sheep in a night pasture.
	// Working sheep carry a music bubble, error sheep burn pink with a "!" and a
	// shake, resting sheep close their eyes and float a lazy "z". Positions come
	// from a stable name hash so the pasture never reshuffles on refresh. A hover
	// card names the sheep. (The pointer-following lantern is gone — pixel art.)
	import { SHEEP, SHEEP_REST, DECO } from '$lib/pixel/sprites.js';

	let { sheep = [] } = $props();

	let host = $state(null);
	let width = $state(0);
	let hovered = $state(null);

	// Pixel sheep: 16×14 cells → 48×42 at 3×, 32×28 at 2×. Cells keep the flock
	// from overlapping (issue #636 pitfall 9).
	let SCALE = $derived(width > 0 && width < 480 ? 2 : 3);
	let CELL = $derived(width > 0 && width < 480 ? 36 : 52);
	const PAD = 14;

	// Stable per-name hash → jitter and blink phase never reshuffle on refresh.
	function hash(str) {
		let h = 2166136261;
		for (let i = 0; i < str.length; i++) {
			h ^= str.charCodeAt(i);
			h = Math.imul(h, 16777619);
		}
		return (h >>> 0) / 4294967295;
	}

	let cols = $derived(Math.max(4, Math.floor((width - PAD * 2) / CELL)));
	let rows = $derived(Math.max(1, Math.ceil(sheep.length / cols)));
	let height = $derived(Math.max(rows * CELL + PAD * 2, 150));

	let points = $derived(
		sheep.map((s, i) => {
			const r = Math.floor(i / cols);
			const c = i % cols;
			const h = hash(s.name || String(i));
			const h2 = hash((s.name || '') + '·');
			// Offset alternate rows by half a cell and jitter a little, so the
			// grid relaxes into a pasture instead of a spreadsheet.
			const span = (width - PAD * 2) / cols;
			const x = PAD + c * span + span / 2 + (r % 2 ? span * 0.25 : -span * 0.25) + (h - 0.5) * span * 0.34;
			const y = PAD + r * CELL + CELL - 4 + (h2 - 0.5) * CELL * 0.18;
			return {
				...s,
				x: Math.round(x),
				y: Math.round(y),
				delay: (h * 3).toFixed(2),
				tone: s.status === 'working' ? 'working' : s.status === 'error' ? 'error' : 'idle'
			};
		})
	);

	let awake = $derived(points.filter((p) => p.tone === 'working'));

	$effect(() => {
		if (!host || typeof ResizeObserver === 'undefined') return;
		const ro = new ResizeObserver((entries) => {
			width = entries[0].contentRect.width;
		});
		ro.observe(host);
		return () => ro.disconnect();
	});

	function rects(map, scale) {
		let out = '';
		map.forEach((row, y) => {
			let x = 0;
			while (x < row.length) {
				const c = row[x];
				if (c === '.') { x++; continue; }
				let run = 1;
				while (row[x + run] === c) run++;
				const fill = c === 'x' ? 'currentColor' : `var(--px-${c})`;
				out += `<rect x="${x * scale}" y="${y * scale}" width="${run * scale}" height="${scale}" fill="${fill}"/>`;
				x += run;
			}
		});
		return out;
	}

	function spriteOf(map) {
		return rects(map, SCALE);
	}

	function decoPos(list) {
		return list.map(([x, y]) => ({ x, y }));
	}

	const stars = decoPos([[5, 14], [18, 40], [27, 10], [44, 30], [58, 12], [70, 46], [80, 20], [97, 62]]);
	const tufts = decoPos([[4, 78], [21, 90], [39, 74], [55, 86], [71, 76], [87, 92], [9, 96], [52, 98], [96, 80], [30, 82]]);

	function href(p) {
		return p.project ? `/projects/${encodeURIComponent(p.project)}` : '/sheep';
	}
</script>

<div
	class="field"
	bind:this={host}
	style="height: {height}px"
	role="img"
	aria-label={`${sheep.length} sheep: ${awake.length} working, ${points.filter((p) => p.tone === 'error').length} in error, the rest resting`}
>
	<div class="field-sky"></div>
	<div class="field-ground"></div>

	{#each stars as s, i (i)}
		<div class="field-deco star s{i % 3}" style="left: {s.x}%; top: {s.y}px">
			<svg viewBox="0 0 {i % 3 === 0 ? DECO.starBig[0].length : DECO.star[0].length} {i % 3 === 0 ? DECO.starBig.length : DECO.star.length}"
				width={(i % 3 === 0 ? DECO.starBig[0].length : DECO.star[0].length) * 3}
				height={(i % 3 === 0 ? DECO.starBig.length : DECO.star.length) * 3}
				shape-rendering="crispEdges"
			>{@html rects(i % 3 === 0 ? DECO.starBig : DECO.star, 3)}</svg>
		</div>
	{/each}

	<div class="field-deco moon" style="right: 7%; top: 14px">
		<svg viewBox="0 0 {DECO.moon[0].length} {DECO.moon.length}" width={DECO.moon[0].length * 4} height={DECO.moon.length * 4} shape-rendering="crispEdges">
			{@html rects(DECO.moon, 4)}
		</svg>
	</div>

	<div class="field-fence field-deco" style="top: {Math.max(0, height * 0.42 - 24)}px">
		{#each Array(Math.ceil(width / 20)) as _, i (i)}
			<svg viewBox="0 0 {DECO.fence[0].length} {DECO.fence.length}" width={DECO.fence[0].length * 2} height={DECO.fence.length * 2} shape-rendering="crispEdges">
				{@html rects(DECO.fence, 2)}
			</svg>
		{/each}
	</div>

	{#each tufts as t, i (i)}
		<div class="field-deco tuft" style="left: {t.x}%; top: {t.y}%">
			<svg viewBox="0 0 {DECO.tuft[0].length} {DECO.tuft.length}" width={DECO.tuft[0].length * 3} height={DECO.tuft.length * 3} shape-rendering="crispEdges">
				{@html rects(DECO.tuft, 3)}
			</svg>
		</div>
	{/each}

	{#if width > 0}
		{#each points as p (p.name)}
			<a
				href={href(p)}
				class="flock-sheep {p.tone}"
				class:hover={hovered?.name === p.name}
				style="left: {p.x}px; top: {p.y}px"
				onpointerenter={() => (hovered = p)}
				onpointerleave={() => (hovered = null)}
				onfocus={() => (hovered = p)}
				onblur={() => (hovered = null)}
			>
				{#if p.tone === 'working'}
					<span class="bubble">♪</span>
				{:else if p.tone === 'error'}
					<span class="bubble err">!</span>
				{:else}
					<span class="bubble zz">z</span>
				{/if}
				<span class="sprite">
					<svg viewBox="0 0 {16 * SCALE} {14 * SCALE}" width={16 * SCALE} height={14 * SCALE} shape-rendering="crispEdges">
						{@html spriteOf(p.tone === 'idle' ? SHEEP_REST : SHEEP)}
					</svg>
				</span>
				{#if p.tone !== 'idle'}
					<span class="tag">{p.name}</span>
				{/if}
			</a>
		{/each}

		{#if hovered}
			<div class="tip" style="left: {Math.min(Math.max(hovered.x, 84), Math.max(width - 84, 84))}px; top: {hovered.y - 14 * SCALE}px" role="tooltip">
				<span class="tip-name">{hovered.name}</span>
				<span class="tip-row">
					<span class="tip-state {hovered.tone === 'working' ? 'live' : hovered.tone === 'error' ? 'danger' : 'rest'}">{hovered.status}</span>
					<span class="tip-sep">·</span>
					<span class="mono">{hovered.provider}</span>
				</span>
				{#if hovered.project}
					<span class="tip-project mono">{hovered.project}</span>
				{/if}
			</div>
		{/if}
	{/if}
</div>

<style>
	.field {
		position: relative;
		width: 100%;
		margin: 3px;
		border-radius: 0;
		box-shadow: 0 0 0 3px #000;
		overflow: hidden;
		touch-action: pan-y;
	}

	.field > * {
		position: absolute;
	}

	/* Night sky over a dark pasture (mockup 06 lines 152–158). */
	.field-sky {
		inset: 0 0 auto 0;
		height: 42%;
		background: linear-gradient(180deg, #000 0 36%, #161618 36% 70%, var(--bg-2) 70%);
	}

	.field-ground {
		inset: auto 0 0 0;
		height: 58%;
		background:
			linear-gradient(180deg, var(--p-green) 0 3px, #00a85a 3px 6px, transparent 6px),
			radial-gradient(circle at 2px 2px, #006e43 1.5px, transparent 2px) 0 0 / 16px 12px,
			var(--p-dgreen);
	}

	.field-deco {
		pointer-events: none;
	}

	.field-deco.star {
		animation: px-blink 1.6s steps(1) infinite;
	}

	.field-deco.star.s1 { animation-delay: 0.5s; }
	.field-deco.star.s2 { animation-delay: 1.1s; }

	.field-fence {
		left: 0;
		right: 0;
		display: flex;
		overflow: hidden;
	}

	.flock-sheep {
		transform: translate(-50%, -100%);
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 3px;
		z-index: 2;
		cursor: pointer;
		outline: none;
	}

	.flock-sheep .sprite svg {
		display: block;
		image-rendering: pixelated;
	}

	.flock-sheep .tag {
		font-family: var(--font-sans);
		font-size: var(--fs-2xs);
		line-height: 1.5;
		padding: 1px 5px;
		white-space: nowrap;
		background: #000;
		color: var(--p-white);
	}

	.flock-sheep.working .tag {
		background: var(--p-blue);
		color: #000;
	}

	.flock-sheep.error .tag {
		background: var(--p-red);
		color: var(--p-white);
	}

	/* Error sheep glow pink instead of wool white. */
	.flock-sheep.error {
		--px-w: #ffb3c8;
		--px-s: #ff77a8;
	}

	.flock-sheep.working .sprite {
		animation: px-bob 0.7s steps(2, jump-none) infinite;
	}

	.flock-sheep.error .sprite {
		animation: px-shake 0.4s steps(2) infinite;
	}

	.flock-sheep .bubble {
		position: absolute;
		bottom: calc(100% - 6px);
		left: 60%;
		font-size: var(--fs-2xs);
		padding: 0 4px;
		line-height: 1.4;
		white-space: nowrap;
		background: var(--p-white);
		color: #000;
		box-shadow: 0 0 0 2px #000;
	}

	.flock-sheep .bubble.err {
		background: var(--p-red);
		color: var(--p-white);
	}

	.flock-sheep .bubble.zz {
		background: none;
		box-shadow: none;
		color: var(--p-lav);
		animation: px-float 2.4s steps(6) infinite;
	}

	@keyframes px-bob {
		0%, 100% { transform: translateY(0); }
		50% { transform: translateY(-2px); }
	}

	@keyframes px-shake {
		0%, 100% { transform: translateX(0); }
		50% { transform: translateX(2px); }
	}

	@keyframes px-float {
		0% { transform: translateY(0); opacity: 0.4; }
		50% { transform: translateY(-4px); opacity: 1; }
		100% { transform: translateY(-8px); opacity: 0; }
	}

	/* Hover card */
	.tip {
		z-index: 3;
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 140px;
		padding: 8px 11px;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel), var(--shadow-2);
		transform: translate(-50%, -100%);
		pointer-events: none;
		animation: fade-in var(--dur-2) steps(2) both;
	}

	.tip-name {
		font-size: var(--fs-sm);
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
	}

	.tip-row {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: var(--fs-2xs);
		color: var(--text-secondary);
	}

	.tip-state {
		text-transform: capitalize;
	}

	.tip-state.live { color: var(--live); }
	.tip-state.danger { color: var(--danger-text); }
	.tip-state.rest { color: var(--text-tertiary); }

	.tip-sep {
		color: var(--text-tertiary);
	}

	.tip-project {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	@media (max-width: 480px) {
		.flock-sheep .tag {
			padding: 0 3px;
		}
	}
</style>
