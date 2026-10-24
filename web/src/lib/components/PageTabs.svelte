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
		border-bottom: 1px solid var(--border);
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
		height: 2px;
		width: var(--w);
		transform: translateX(var(--x));
		border-radius: 2px 2px 0 0;
		background: var(--accent);
		box-shadow: 0 0 12px var(--accent-glow);
		pointer-events: none;
	}

	.tab-ink.ready {
		transition: transform 380ms var(--ease-out), width 380ms var(--ease-out);
	}

	.tab {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		padding: 9px 12px 11px;
		border: none;
		background: none;
		color: var(--text-tertiary);
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
		white-space: nowrap;
		flex-shrink: 0;
		cursor: pointer;
		border-radius: var(--radius) var(--radius) 0 0;
		transition: color var(--dur-1) ease, background var(--dur-1) ease;
	}

	.tab:hover {
		color: var(--text-primary);
		background: rgba(237, 230, 218, 0.03);
	}

	.tab.active {
		color: var(--text-primary);
	}

	.tab.active :global(.icon) {
		color: var(--accent);
	}

	.tab-live {
		width: 5px;
		height: 5px;
	}

	/* Count chip matching the sidebar's .nav-badge (+layout.svelte). */
	.tab-badge {
		font-size: 10.5px;
		font-weight: var(--fw-semibold);
		color: var(--warning-ink, var(--warning));
		background: var(--warning-soft, rgba(244, 184, 96, 0.14));
		border: 1px solid var(--warning-line, rgba(244, 184, 96, 0.25));
		padding: 0 7px;
		border-radius: var(--radius-full);
		line-height: 17px;
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
