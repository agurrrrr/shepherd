<script>
	import Icon from './Icon.svelte';

	let { page = 1, totalPages = 1, total = 0, limit = 20, onChange = () => {} } = $props();

	let pages = $derived((() => {
		const arr = [];
		let start = Math.max(1, page - 2);
		let end = Math.min(totalPages, start + 4);
		start = Math.max(1, end - 4);
		for (let i = start; i <= end; i++) arr.push(i);
		return arr;
	})());
</script>

{#if totalPages > 1}
	<div class="pagination">
		<button class="btn page-btn" aria-label="Previous page" disabled={page <= 1} onclick={() => onChange(page - 1)}>
			<Icon name="chevron-left" size={14} />
		</button>

		{#each pages as p}
			<button class="btn page-btn" class:active={p === page} onclick={() => onChange(p)}>{p}</button>
		{/each}

		<button class="btn page-btn" aria-label="Next page" disabled={page >= totalPages} onclick={() => onChange(page + 1)}>
			<Icon name="chevron-right" size={14} />
		</button>

		<span class="page-info mono">{total.toLocaleString()} items · {limit}/page</span>
	</div>
{/if}

<style>
	.pagination {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-top: var(--space-5);
		flex-wrap: wrap;
	}

	.page-btn {
		min-width: 32px;
		height: 32px;
		padding: 0 8px;
		font-size: var(--fs-sm);
		font-variant-numeric: tabular-nums;
		background: transparent;
		border-color: transparent;
		box-shadow: none;
		color: var(--text-secondary);
	}

	.page-btn:hover:not(:disabled) {
		background: var(--bg-hover);
		border-color: var(--border);
		color: var(--text-primary);
	}

	.page-btn.active {
		background: var(--accent-soft);
		border-color: var(--accent-line);
		color: var(--accent);
		font-weight: var(--fw-semibold);
	}

	.page-info {
		margin-left: 12px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}
</style>
