<script>
	// The flock at a glance: every sheep is a point in a loose pasture.
	// Resting sheep breathe slowly in wool-grey, working sheep glow mint and
	// carry a direct label, sheep in trouble burn red. A lantern follows the
	// pointer so the field reads as something you look *over*, not a table.
	let { sheep = [] } = $props();

	let host = $state(null);
	let width = $state(0);
	let hovered = $state(null);
	let lamp = $state({ x: -999, y: -999, on: false });

	const PAD = 18;
	// Phones get a tighter pasture so a large flock doesn't push the rest of the
	// dashboard a full screen down.
	let CELL = $derived(width > 0 && width < 480 ? 27 : 34);

	// Stable per-name hash → jitter and breathing phase never reshuffle on refresh.
	function hash(str) {
		let h = 2166136261;
		for (let i = 0; i < str.length; i++) {
			h ^= str.charCodeAt(i);
			h = Math.imul(h, 16777619);
		}
		return (h >>> 0) / 4294967295;
	}

	let cols = $derived(Math.max(6, Math.floor((width - PAD * 2) / CELL)));
	let rows = $derived(Math.max(1, Math.ceil(sheep.length / cols)));
	let height = $derived(rows * CELL + PAD * 2 - 6);

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
			const y = PAD + r * CELL + CELL / 2 - 3 + (h2 - 0.5) * CELL * 0.3;
			return {
				...s,
				x,
				y,
				delay: (h * 6).toFixed(2),
				period: (4.2 + h2 * 3).toFixed(2),
				tone: s.status === 'working' ? 'live' : s.status === 'error' ? 'danger' : 'rest'
			};
		})
	);

	let awake = $derived(points.filter((p) => p.tone === 'live'));

	$effect(() => {
		if (!host || typeof ResizeObserver === 'undefined') return;
		const ro = new ResizeObserver((entries) => {
			width = entries[0].contentRect.width;
		});
		ro.observe(host);
		return () => ro.disconnect();
	});

	function onMove(e) {
		const r = host.getBoundingClientRect();
		lamp = { x: e.clientX - r.left, y: e.clientY - r.top, on: true };
	}

	function labelSide(p) {
		return p.x > width - 170 ? 'left' : 'right';
	}

	function href(p) {
		return p.project ? `/projects/${encodeURIComponent(p.project)}` : '/sheep';
	}
</script>

<div
	class="field"
	bind:this={host}
	style="height: {height}px; --lx: {lamp.x}px; --ly: {lamp.y}px"
	class:lit={lamp.on}
	onpointermove={onMove}
	onpointerleave={() => { lamp = { ...lamp, on: false }; hovered = null; }}
	role="img"
	aria-label={`${sheep.length} sheep: ${awake.length} working, ${points.filter((p) => p.tone === 'danger').length} in error, the rest resting`}
>
	{#if width > 0}
		<svg {width} {height} viewBox="0 0 {width} {height}" aria-hidden="true">
			{#each points as p (p.name)}
				<a
					href={href(p)}
					tabindex="-1"
					class="sheep {p.tone}"
					class:hover={hovered?.name === p.name}
					style="--d: {p.delay}s; --t: {p.period}s"
					onpointerenter={() => (hovered = p)}
					onfocus={() => (hovered = p)}
				>
					<circle class="hit" cx={p.x} cy={p.y} r="15" />
					{#if p.tone === 'live'}
						<circle class="ring" cx={p.x} cy={p.y} r="6" />
						<circle class="ring r2" cx={p.x} cy={p.y} r="6" />
					{/if}
					<circle class="dot" cx={p.x} cy={p.y} r={p.tone === 'rest' ? 3.6 : 5} />
				</a>
			{/each}
		</svg>

		<!-- Direct labels for the few that are awake — the story of the field -->
		{#each awake.slice(0, 4) as p (p.name)}
			<span
				class="tag {labelSide(p)}"
				style="left: {p.x}px; top: {p.y}px"
				aria-hidden="true"
			>
				<b>{p.name}</b><span>{p.project}</span>
			</span>
		{/each}

		{#if hovered}
			<div class="tip" style="left: {Math.min(Math.max(hovered.x, 84), width - 84)}px; top: {hovered.y}px" role="tooltip">
				<span class="tip-name">{hovered.name}</span>
				<span class="tip-row">
					<span class="tip-state {hovered.tone}">{hovered.status}</span>
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
		border-radius: var(--radius-md);
		background:
			radial-gradient(260px 200px at var(--lx) var(--ly), rgba(244, 184, 96, 0.075), transparent 70%),
			radial-gradient(120% 140% at 50% 120%, rgba(95, 224, 194, 0.035), transparent 60%);
		transition: background-position var(--dur-1) linear;
		touch-action: pan-y;
	}

	.field:not(.lit) {
		background:
			radial-gradient(120% 140% at 50% 120%, rgba(95, 224, 194, 0.035), transparent 60%);
	}

	svg {
		display: block;
		overflow: visible;
	}

	.hit {
		fill: transparent;
	}

	.sheep {
		cursor: pointer;
		outline: none;
	}

	.dot {
		transform-box: fill-box;
		transform-origin: center;
		transition: transform var(--dur-2) var(--ease-spring), fill var(--dur-1) ease;
	}

	.rest .dot {
		fill: var(--wool);
		opacity: 0.32;
		animation: breathe var(--t) ease-in-out var(--d) infinite;
	}

	.live .dot {
		fill: var(--live);
		filter: drop-shadow(0 0 6px rgba(95, 224, 194, 0.85));
	}

	.danger .dot {
		fill: var(--danger);
		filter: drop-shadow(0 0 5px rgba(255, 107, 97, 0.8));
	}

	.sheep.hover .dot,
	.sheep:hover .dot {
		transform: scale(1.7);
		opacity: 1;
		animation-play-state: paused;
	}

	.rest.hover .dot,
	.rest:hover .dot {
		fill: var(--text-primary);
	}

	.ring {
		fill: none;
		stroke: var(--live);
		stroke-width: 1.2;
		transform-box: fill-box;
		transform-origin: center;
		animation: ripple 2.6s var(--ease-out) infinite;
	}

	.ring.r2 {
		animation-delay: 1.3s;
	}

	@keyframes breathe {
		0%, 100% { opacity: 0.22; }
		50% { opacity: 0.5; }
	}

	@keyframes ripple {
		0% { transform: scale(1); opacity: 0.7; }
		100% { transform: scale(3.4); opacity: 0; }
	}

	/* Direct label riding beside a working sheep */
	.tag {
		position: absolute;
		display: inline-flex;
		align-items: baseline;
		gap: 6px;
		padding: 2px 8px;
		border-radius: var(--radius-full);
		background: rgba(9, 9, 10, 0.82);
		border: 1px solid var(--live-line);
		font-size: var(--fs-2xs);
		line-height: 1.6;
		white-space: nowrap;
		pointer-events: none;
		transform: translate(14px, -50%);
		animation: fade-in var(--dur-3) ease backwards;
		backdrop-filter: blur(4px);
	}

	.tag.left {
		transform: translate(calc(-100% - 14px), -50%);
	}

	.tag b {
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
	}

	.tag span {
		font-family: var(--font-mono);
		color: var(--live);
	}

	/* Hover card */
	.tip {
		position: absolute;
		z-index: 3;
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 140px;
		padding: 8px 11px;
		border-radius: var(--radius);
		background: rgba(20, 20, 22, 0.96);
		border: 1px solid var(--border-strong);
		box-shadow: var(--shadow-2);
		transform: translate(-50%, calc(-100% - 16px));
		pointer-events: none;
		animation: tip-in var(--dur-2) var(--ease-out) both;
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
	.tip-state.danger { color: var(--danger); }

	.tip-sep {
		color: var(--text-tertiary);
	}

	.tip-project {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	@keyframes tip-in {
		from { opacity: 0; transform: translate(-50%, calc(-100% - 10px)); }
	}

	@media (hover: none) {
		.field {
			background:
				radial-gradient(120% 140% at 50% 120%, rgba(95, 224, 194, 0.035), transparent 60%);
		}
	}
</style>
