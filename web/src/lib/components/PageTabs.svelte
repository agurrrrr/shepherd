<script>
	import { tick } from 'svelte';
	import Icon from '$lib/components/Icon.svelte';

	// Underline tab bar for full-page section navigation (#621). This is the
	// bar the project screen uses: text + icon tabs with a sliding ink line
	// under the active tab, horizontal scrolling when the row overflows, and
	// optional live dot / count badge per tab.
	//
	// NOT the same thing as Tabs.svelte: that one is the pill-shaped split
	// control (dashboard, task list, skills screens). Keep them separate —
	// this component is for page-level section tabs, Tabs.svelte for compact
	// in-card filters.
	//
	// tabs:     [{ id, label, icon, live?, badge? }] — `live` shows the pulsing
	//           dot (Live Output while working), `badge` > 0 shows a count chip
	//           styled like the sidebar's .nav-badge.
	// value:    active tab id.
	// onChange: called with the clicked/moved-to tab id.
	// ariaLabel: aria-label for the nav[role=tablist].
	//
	// Keyboard: ArrowLeft/ArrowRight move to the previous/next tab (same
	// roving-tabindex pattern as Tabs.svelte). Each button carries id="tab-<id>"
	// so panels can point at it with aria-labelledby (used in stage 2/4).
	let { tabs = [], value = '', onChange = () => {}, ariaLabel = 'Tabs' } = $props();

	// Underline that slides to the active tab.
	let tabsEl = $state(null);
	let tabInk = $state({ x: 0, w: 0, ready: false });
	$effect(() => {
		value;
		if (!tabsEl) return;
		const measure = () => {
			const el = tabsEl?.querySelector('.tab.active');
			if (el) tabInk = { x: el.offsetLeft, w: el.offsetWidth, ready: true };
		};
		requestAnimationFrame(measure);
		const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(measure) : null;
		ro?.observe(tabsEl);
		return () => ro?.disconnect();
	});

	function onKeydown(e) {
		if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
		const i = tabs.findIndex((t) => t.id === value);
		const next = tabs[(i + (e.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length];
		if (!next) return;
		e.preventDefault();
		onChange(next.id);
		tick().then(() => document.getElementById(`tab-${next.id}`)?.focus());
	}
</script>

<nav class="tabs" role="tablist" aria-label={ariaLabel} bind:this={tabsEl} onkeydown={onKeydown}>
	<span class="tab-ink" style="--x: {tabInk.x}px; --w: {tabInk.w}px" class:ready={tabInk.ready} aria-hidden="true"></span>
	{#each tabs as t (t.id)}
		<button id="tab-{t.id}" type="button" class="tab" class:active={value === t.id}
			role="tab" aria-selected={value === t.id}
			tabindex={value === t.id ? 0 : -1}
			onclick={() => onChange(t.id)}>
			{#if t.icon}<Icon name={t.icon} size={14} stroke={1.9} />{/if}
			<span>{t.label}</span>
			{#if t.live}
				<span class="live-dot tab-live" aria-label="live"></span>
			{/if}
			{#if t.badge > 0}
				<!-- #624: the bare number is unreadable out of context — screen
				     readers get "확인 필요 3건" (label + count) instead. -->
				<span class="tab-badge mono num" aria-label="{t.label} {t.badge}건">{t.badge}</span>
			{/if}
		</button>
	{/each}
</nav>

<style>
	.tabs {
		position: relative;
		display: flex;
		gap: 2px;
		flex-shrink: 0;
		margin-bottom: 14px;
		box-shadow: inset 0 -3px 0 0 var(--lo);
		overflow-x: auto;
		scrollbar-width: none;
		-webkit-overflow-scrolling: touch;
	}

	.tabs::-webkit-scrollbar {
		display: none;
	}

	.tab-ink {
		position: absolute;
		left: 0;
		bottom: 0;
		height: 3px;
		width: var(--w);
		transform: translateX(var(--x));
		border-radius: 0;
		background: var(--highlight);
		pointer-events: none;
	}

	/* The bar hops to the active tab in four steps */
	.tab-ink.ready {
		transition: transform var(--dur-2) steps(4), width var(--dur-2) steps(4);
	}

	.tab {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		padding: 9px 12px 11px;
		border: none;
		background: none;
		color: var(--text-secondary);
		font-size: var(--fs-sm);
		font-weight: var(--fw-regular);
		white-space: nowrap;
		flex-shrink: 0;
		cursor: pointer;
		border-radius: 0;
	}

	.tab:hover {
		color: var(--text-primary);
		background: var(--bg-2);
	}

	.tab.active,
	.tab.active :global(.icon) {
		color: var(--highlight);
	}

	/* Inside the scroller: an outset ring would be clipped */
	.tab:focus-visible {
		outline-offset: -3px;
	}

	.tab-live {
		width: 6px;
		height: 6px;
	}

	/* Attention count on a tab: black chip, red count. */
	.tab-badge {
		font-size: var(--fs-2xs);
		font-weight: var(--fw-regular);
		color: var(--danger-text);
		background: #000;
		padding: 0 6px;
		border-radius: 0;
		line-height: 18px;
	}

	@media (max-width: 768px) {
		.tabs {
			margin-bottom: 10px;
		}

		.tab {
			padding: 8px 10px 10px;
			font-size: var(--fs-xs);
		}
	}

	@media (max-height: 600px) {
		.tabs {
			margin-bottom: 8px;
		}

		.tab {
			padding: 6px 10px 8px;
		}
	}
</style>
