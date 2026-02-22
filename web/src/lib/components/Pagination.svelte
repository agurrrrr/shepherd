<script>
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
		<button class="btn page-btn" disabled={page <= 1} onclick={() => onChange(page - 1)}>&#x25C0;</button>

		{#each pages as p}
			<button class="btn page-btn" class:active={p === page} onclick={() => onChange(p)}>{p}</button>
		{/each}

		<button class="btn page-btn" disabled={page >= totalPages} onclick={() => onChange(page + 1)}>&#x25B6;</button>

		<span class="page-info">{total} items ({limit}/page)</span>
	</div>
{/if}

<style>
	.pagination {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-top: 16px;
		flex-wrap: wrap;
	}

	.page-btn {
		min-width: 32px;
		height: 32px;
		padding: 0 8px;
		font-size: 13px;
		display: flex;
		align-items: center;
		justify-content: center;
	}

	.page-btn.active {
		background: var(--accent);
		border-color: var(--accent);
		color: #fff;
	}

	.page-info {
		margin-left: 12px;
		font-size: 12px;
		color: var(--text-secondary);
	}
</style>
