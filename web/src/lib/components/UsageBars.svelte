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
				<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>
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
		border-radius: var(--radius-sm);
		transition: background var(--dur-1) ease;
		outline: none;
	}

	.row.hot,
	.row:focus-visible {
		background: rgba(237, 230, 218, 0.035);
	}

	.row:focus-visible {
		box-shadow: inset 0 0 0 1px var(--accent-line);
	}

	.name {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.name.muted {
		font-style: italic;
		color: var(--text-secondary);
	}

	.pre {
		color: var(--text-tertiary);
	}

	.track {
		position: relative;
		height: 10px;
		/* Hairline baseline on the left; the bar grows from it. */
		border-left: 1px solid var(--border-strong);
	}

	.fill {
		position: absolute;
		left: 0;
		top: 0;
		bottom: 0;
		width: 100%;
		border-radius: 0 4px 4px 0;
		background: linear-gradient(90deg, rgba(244, 184, 96, 0.55), var(--accent));
		transform-origin: left center;
		transform: scaleX(var(--r));
		transition: transform 600ms var(--ease-out), filter var(--dur-1) ease;
		animation: grow 900ms var(--ease-out) backwards;
		animation-delay: calc(min(var(--i, 0), 14) * 28ms + 80ms);
	}

	.row.hot .fill {
		filter: brightness(1.12) drop-shadow(0 0 8px var(--accent-glow));
	}

	@keyframes grow {
		from { transform: scaleX(0); }
	}

	.val {
		text-align: right;
		font-family: var(--font-mono);
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
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
		border-radius: var(--radius);
		background: rgba(20, 20, 22, 0.97);
		border: 1px solid var(--border-strong);
		box-shadow: var(--shadow-2);
		transform: translateX(-50%);
		pointer-events: none;
		animation: tip-in var(--dur-2) var(--ease-out) both;
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
		color: var(--text-secondary);
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		cursor: pointer;
		transition: color var(--dur-1) ease;
	}

	.more:hover {
		color: var(--accent);
	}

	.more-chev {
		display: inline-flex;
		transition: transform var(--dur-2) var(--ease-out);
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
