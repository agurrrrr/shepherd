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
	.tabs {
		position: relative;
		display: inline-flex;
		gap: 2px;
		padding: 3px;
		background: rgba(237, 230, 218, 0.03);
		border: 1px solid var(--border);
		border-radius: 10px;
		box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.35);
		isolation: isolate;
	}

	.tabs:focus-visible {
		outline-offset: 2px;
	}

	.pill {
		position: absolute;
		top: 3px;
		bottom: 3px;
		left: 0;
		width: var(--w);
		transform: translateX(var(--x));
		border-radius: 7px;
		background:
			linear-gradient(180deg, rgba(255, 250, 240, 0.07), rgba(255, 250, 240, 0.02)),
			var(--bg-4);
		box-shadow:
			inset 0 1px 0 rgba(255, 250, 240, 0.08),
			0 1px 2px rgba(0, 0, 0, 0.5),
			0 0 0 1px rgba(237, 230, 218, 0.06);
		z-index: -1;
		pointer-events: none;
	}

	.pill.ready {
		transition: transform 360ms var(--ease-out), width 360ms var(--ease-out), opacity var(--dur-1) ease;
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
		font-weight: var(--fw-medium);
		border-radius: 7px;
		cursor: pointer;
		white-space: nowrap;
		transition: color var(--dur-1) ease;
	}

	.sm .tab-btn {
		padding: 3px 10px;
		font-size: var(--fs-xs);
	}

	.tab-btn:hover {
		color: var(--text-primary);
	}

	.tab-btn.active {
		color: var(--text-primary);
	}

	.tab-btn:focus-visible {
		outline-offset: -2px;
	}

	.tab-count {
		font-size: 10.5px;
		font-family: var(--font-mono);
		padding: 0 6px;
		line-height: 17px;
		background: rgba(237, 230, 218, 0.06);
		border-radius: var(--radius-full);
		color: var(--text-tertiary);
		min-width: 18px;
		text-align: center;
		transition: background var(--dur-1) ease, color var(--dur-1) ease;
	}

	.tab-btn.active .tab-count {
		background: var(--accent-soft);
		color: var(--accent);
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
