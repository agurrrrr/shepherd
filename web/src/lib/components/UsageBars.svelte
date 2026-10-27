<script>
	// Single-series horizontal bar list. Each row is name · bar · value · sub,
	// so the list doubles as its own table view; hover reveals the full record.
	//
	// items: { key, label, prefix?, value, display, sub?, muted?, details?: [label, value][] }
	let { items = [], limit = 8, label = 'Usage' } = $props();

	let expanded = $state(false);
	let hover = $state(null);

	let max = $derived(items.reduce((m, it) => Math.max(m, Number(it.value) || 0), 0));
	let shown = $derived(expanded ? items : items.slice(0, limit));
	let hidden = $derived(Math.max(0, items.length - limit));
</script>

<div class="bars" role="table" aria-label={label}>
	{#each shown as it, i (it.key)}
		{@const ratio = max > 0 ? (Number(it.value) || 0) / max : 0}
		<div
			class="row reveal"
			class:hot={hover === it.key}
			style="--i: {i}"
			role="row"
			tabindex="0"
			onpointerenter={() => (hover = it.key)}
			onpointerleave={() => (hover = null)}
			onfocus={() => (hover = it.key)}
			onblur={() => (hover = null)}
		>
			<span class="name" class:muted={it.muted} role="cell" title={it.prefix ? `${it.prefix}/${it.label}` : it.label}>
				{#if it.prefix}<span class="pre">{it.prefix}/</span>{/if}{it.label}
			</span>
			<div class="track" role="cell">
				<span class="fill" style="--r: {Math.max(ratio, 0.006)}" aria-hidden="true"></span
				>{#if hover === it.key && it.details?.length}
					<div class="tip" role="tooltip" style="--at: {Math.min(Math.max(ratio, 0.2), 0.8)}">
						<span class="tip-title">{it.prefix ? `${it.prefix}/` : ''}{it.label}</span>
						<dl>
							{#each it.details as [k, v]}
								<dt>{k}</dt><dd class="num">{v}</dd>
							{/each}
						</dl>
					</div>
				{/if}
			</div>
			<span class="val num" role="cell">{it.display}</span>
			{#if it.sub != null}
				<span class="sub num" role="cell">{it.sub}</span>
			{/if}
		</div>
	{/each}

	{#if hidden > 0}
		<button type="button" class="more" onclick={() => (expanded = !expanded)} aria-expanded={expanded}>
			{expanded ? 'Show fewer' : `Show all ${items.length}`}
			<span class="more-chev" class:up={expanded} aria-hidden="true">
				<svg width="10" height="6" viewBox="0 0 5 3" shape-rendering="crispEdges"><path fill="currentColor" d="M0 0h5v1H0zM1 1h3v1H1zM2 2h1v1H2z"/></svg>
			</span>
		</button>
	{/if}
</div>

<style>
	.bars {
		display: flex;
		flex-direction: column;
	}

	.row {
		position: relative;
		display: grid;
		grid-template-columns: minmax(0, 1.15fr) minmax(60px, 1fr) 62px 58px;
		align-items: center;
		gap: 14px;
		padding: 7px 10px;
		margin: 0 -10px;
		border-radius: 0;
	}

	.row.hot,
	.row:focus-visible {
		background: var(--bg-3);
	}

	/* Bar colours cycle through the palette: pink, blue, green, orange, lavender */
	.row { --bar: var(--accent); }
	.row:nth-child(5n + 2) { --bar: var(--live); }
	.row:nth-child(5n + 3) { --bar: var(--success); }
	.row:nth-child(5n + 4) { --bar: var(--warning); }
	.row:nth-child(5n + 5) { --bar: var(--queued); }

	.name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.name.muted {
		font-style: normal;
		color: var(--text-secondary);
	}

	.pre {
		color: var(--text-tertiary);
	}

	/* Black 14px track; the fill is a run of 6px cells inset by 2px. It
	   grows by width, not scaleX, so the cells keep their size. */
	.track {
		position: relative;
		height: 14px;
		background: #000;
	}

	.fill {
		position: absolute;
		left: 2px;
		top: 2px;
		bottom: 2px;
		width: calc((100% - 4px) * var(--r));
		border-radius: 0;
		background: repeating-linear-gradient(90deg, var(--bar) 0 6px, transparent 6px 8px);
		animation: grow 900ms steps(8) backwards;
		animation-delay: calc(min(var(--i, 0), 14) * 28ms + 80ms);
	}

	.row.hot .fill {
		background: repeating-linear-gradient(90deg, var(--text-primary) 0 6px, transparent 6px 8px);
	}

	@keyframes grow {
		from { width: 0; }
	}

	.val {
		text-align: right;
		font-family: var(--font-mono);
		font-size: var(--fs-sm);
		font-weight: var(--fw-regular);
		color: var(--text-primary);
	}

	.sub {
		text-align: right;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.tip {
		position: absolute;
		z-index: 5;
		bottom: calc(100% + 12px);
		left: calc(var(--at) * 100%);
		min-width: 180px;
		padding: 9px 12px 10px;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel), var(--shadow-2);
		transform: translateX(-50%);
		pointer-events: none;
		animation: tip-in var(--dur-1) steps(2) both;
	}

	.tip-title {
		display: block;
		margin-bottom: 6px;
		font-size: var(--fs-xs);
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
		white-space: nowrap;
	}

	dl {
		display: grid;
		grid-template-columns: auto auto;
		gap: 3px 16px;
		margin: 0;
	}

	dt {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	dd {
		margin: 0;
		text-align: right;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-primary);
	}

	@keyframes tip-in {
		from { opacity: 0; transform: translate(-50%, 4px); }
	}

	.more {
		align-self: flex-start;
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-top: 8px;
		padding: 4px 0;
		border: none;
		background: none;
		color: var(--highlight);
		font-size: var(--fs-xs);
		font-weight: var(--fw-regular);
		cursor: pointer;
	}

	.more:hover {
		color: var(--highlight-hover);
	}

	.more-chev {
		display: inline-flex;
	}

	.more-chev.up {
		transform: rotate(180deg);
	}

	@media (max-width: 480px) {
		.row {
			grid-template-columns: minmax(0, 1fr) minmax(40px, 0.8fr) 56px;
			gap: 10px;
		}

		.sub {
			display: none;
		}
	}
</style>
