<script>
	import { tick } from 'svelte';

	let { tabs = [], value = '', onChange = () => {}, ariaLabel = 'Tabs', size = 'md' } = $props();

	let root = $state(null);
	let pill = $state({ x: 0, w: 0, ready: false });

	// Slide the lit pill under the active segment. Measured after layout so
	// it also follows font loading and container resizes.
	async function measure() {
		await tick();
		const el = root?.querySelector('[aria-selected="true"]');
		if (!el) {
			pill = { ...pill, w: 0 };
			return;
		}
		pill = { x: el.offsetLeft, w: el.offsetWidth, ready: true };
	}

	$effect(() => {
		value;
		tabs;
		measure();
	});

	$effect(() => {
		if (!root || typeof ResizeObserver === 'undefined') return;
		const ro = new ResizeObserver(() => measure());
		ro.observe(root);
		return () => ro.disconnect();
	});

	function onKeydown(e) {
		if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
		const i = tabs.findIndex((t) => t.value === value);
		const next = tabs[(i + (e.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length];
		if (!next) return;
		e.preventDefault();
		onChange(next.value);
		tick().then(() => root?.querySelector('[aria-selected="true"]')?.focus());
	}
</script>

<div class="tabs" class:sm={size === 'sm'} role="tablist" aria-label={ariaLabel} bind:this={root} onkeydown={onKeydown} tabindex="-1">
	<span
		class="pill"
		class:ready={pill.ready}
		style="--x: {pill.x}px; --w: {pill.w}px; opacity: {pill.w ? 1 : 0}"
		aria-hidden="true"
	></span>
	{#each tabs as t (t.value)}
		<button
			type="button"
			class="tab-btn"
			class:active={value === t.value}
			role="tab"
			aria-selected={value === t.value}
			tabindex={value === t.value ? 0 : -1}
			onclick={() => onChange(t.value)}
		>
			<span class="tab-label">{t.label}</span>
			{#if t.count != null}
				<span class="tab-count num">{t.count}</span>
			{/if}
		</button>
	{/each}
</div>

<style>
	/* Segmented control: a black track; the selected item is a yellow block
	   with black words that hops (steps) between positions. */
	.tabs {
		position: relative;
		display: inline-flex;
		gap: 2px;
		padding: 3px;
		background: #000;
		border: 0;
		border-radius: 0;
		isolation: isolate;
	}

	.pill {
		position: absolute;
		top: 3px;
		bottom: 3px;
		left: 0;
		width: var(--w);
		transform: translateX(var(--x));
		border-radius: 0;
		background: var(--highlight);
		z-index: -1;
		pointer-events: none;
	}

	.pill.ready {
		transition: transform var(--dur-2) steps(4), width var(--dur-2) steps(4);
	}

	.tab-btn {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 5px 12px;
		border: none;
		background: transparent;
		color: var(--text-secondary);
		font-size: var(--fs-sm);
		font-weight: var(--fw-regular);
		border-radius: 0;
		cursor: pointer;
		white-space: nowrap;
	}

	.sm .tab-btn {
		padding: 3px 10px;
		font-size: var(--fs-xs);
	}

	.tab-btn:hover {
		color: var(--text-primary);
		background: var(--bg-2);
	}

	.tab-btn.active,
	.tab-btn.active:hover {
		color: #000;
		background: transparent;
	}

	/* Outside the button: a yellow ring on the yellow pill would vanish */
	.tab-btn:focus-visible {
		outline-offset: 3px;
	}

	.tab-count {
		font-size: var(--fs-2xs);
		font-family: var(--font-mono);
		padding: 0 6px;
		line-height: 17px;
		background: var(--well);
		border-radius: 0;
		color: var(--text-tertiary);
		min-width: 18px;
		text-align: center;
	}

	.tab-btn.active .tab-count {
		background: #000;
		color: var(--highlight);
	}

	@media (max-width: 480px) {
		.tabs {
			max-width: 100%;
			overflow-x: auto;
			scrollbar-width: none;
		}
		.tabs::-webkit-scrollbar { display: none; }
		.tab-btn {
			flex-shrink: 0;
		}
	}
</style>
