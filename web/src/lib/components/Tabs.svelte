<script>
	let { tabs = [], value = '', onChange = () => {}, ariaLabel = 'Tabs' } = $props();
</script>

<div class="tabs" role="tablist" aria-label={ariaLabel}>
	{#each tabs as t}
		<button
			type="button"
			class="tab-btn"
			class:active={value === t.value}
			role="tab"
			aria-selected={value === t.value}
			onclick={() => onChange(t.value)}
		>
			<span class="tab-label">{t.label}</span>
			{#if t.count != null}
				<span class="tab-count">{t.count}</span>
			{/if}
		</button>
	{/each}
</div>

<style>
	.tabs {
		display: inline-flex;
		gap: 2px;
		background: var(--bg-2);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 2px;
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
		border-radius: var(--radius-sm);
		cursor: pointer;
		transition: color 0.15s, background 0.15s;
	}

	.tab-btn:hover {
		color: var(--text-primary);
	}

	.tab-btn.active {
		background: var(--bg-3);
		color: var(--accent);
		font-weight: var(--fw-semibold);
	}

	.tab-count {
		font-size: var(--fs-2xs);
		font-family: var(--font-mono);
		padding: 1px 6px;
		background: var(--bg-3);
		border-radius: var(--radius-full);
		color: var(--text-tertiary);
		min-width: 18px;
		text-align: center;
	}

	.tab-btn.active .tab-count {
		background: var(--accent-soft);
		color: var(--accent);
	}

	@media (max-width: 480px) {
		.tabs {
			width: 100%;
			overflow-x: auto;
			scrollbar-width: none;
		}
		.tabs::-webkit-scrollbar { display: none; }
		.tab-btn {
			flex-shrink: 0;
		}
	}
</style>
