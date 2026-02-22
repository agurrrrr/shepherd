<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';

	let tasks = [];
	let total = 0;
	let page = 1;
	let limit = 20;
	let totalPages = 1;
	let loaded = false;
	let statusFilter = '';
	let projectFilter = '';
	let searchQuery = '';
	let unsubs = [];

	// Detail view
	let selectedTask = null;
	let showDetail = false;

	onMount(async () => {
		await loadTasks();

		unsubs.push(onSSE('task_complete', () => loadTasks()));
		unsubs.push(onSSE('task_start', () => loadTasks()));
		unsubs.push(onSSE('task_fail', () => loadTasks()));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	async function loadTasks() {
		const params = new URLSearchParams();
		params.set('page', page);
		params.set('limit', limit);
		if (statusFilter) params.set('status', statusFilter);
		if (projectFilter) params.set('project', projectFilter);
		if (searchQuery) params.set('q', searchQuery);

		const res = await apiGet(`/api/tasks?${params}`);
		if (res) {
			tasks = res.data || [];
			total = res.total || 0;
			totalPages = res.total_pages || 1;
		}
		loaded = true;
	}

	function applyFilters() {
		page = 1;
		loadTasks();
	}

	function gotoPage(p) {
		page = p;
		loadTasks();
	}

	async function viewDetail(taskId) {
		const res = await apiGet(`/api/tasks/${taskId}`);
		if (res?.data) {
			selectedTask = res.data;
			showDetail = true;
		}
	}

	function closeDetail() {
		showDetail = false;
		selectedTask = null;
	}

	function truncate(str, max) {
		if (!str) return '';
		return str.length > max ? str.substring(0, max) + '...' : str;
	}
</script>

<div class="page">
	<h1 class="page-title">Tasks</h1>

	<!-- Filters -->
	<div class="filters card">
		<select class="input filter-select" bind:value={statusFilter} onchange={applyFilters}>
			<option value="">All Status</option>
			<option value="pending">Pending</option>
			<option value="running">Running</option>
			<option value="completed">Completed</option>
			<option value="failed">Failed</option>
		</select>
		<input
			class="input filter-search"
			type="text"
			bind:value={searchQuery}
			placeholder="Search prompts..."
			onkeydown={(e) => e.key === 'Enter' && applyFilters()}
		/>
		<button class="btn" onclick={applyFilters}>Search</button>
	</div>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if tasks.length === 0}
		<div class="card empty-state">
			<p>No tasks found.</p>
		</div>
	{:else}
		<!-- Task List -->
		<div class="task-list">
			{#each tasks as t (t.id)}
				<div class="card task-item" onclick={() => viewDetail(t.id)}>
					<div class="task-header">
						<span class="task-id">#{t.id}</span>
						<StatusBadge status={t.status} />
					</div>
					<div class="task-prompt">{truncate(t.prompt, 120)}</div>
					<div class="task-meta">
						{#if t.sheep}
							<span>{t.sheep}</span>
						{/if}
						{#if t.project}
							<span class="task-project">{t.project}</span>
						{/if}
						<span class="task-time">{t.created_at}</span>
					</div>
					{#if t.summary}
						<div class="task-summary">{truncate(t.summary, 100)}</div>
					{/if}
				</div>
			{/each}
		</div>

		<!-- Pagination -->
		{#if totalPages > 1}
			<div class="pagination">
				<button class="btn" disabled={page <= 1} onclick={() => gotoPage(page - 1)}>Prev</button>
				<span class="page-info">Page {page} / {totalPages} ({total} total)</span>
				<button class="btn" disabled={page >= totalPages} onclick={() => gotoPage(page + 1)}>Next</button>
			</div>
		{/if}
	{/if}

	<!-- Detail Modal -->
	{#if showDetail && selectedTask}
		<div class="modal-overlay" onclick={closeDetail}>
			<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
				<div class="modal-header">
					<h2>Task #{selectedTask.id}</h2>
					<button class="btn" onclick={closeDetail}>Close</button>
				</div>

				<div class="detail-row">
					<span class="detail-label">Status</span>
					<StatusBadge status={selectedTask.status} />
				</div>

				<div class="detail-row">
					<span class="detail-label">Prompt</span>
					<pre class="detail-value mono">{selectedTask.prompt}</pre>
				</div>

				{#if selectedTask.sheep}
					<div class="detail-row">
						<span class="detail-label">Sheep</span>
						<span>{selectedTask.sheep}</span>
					</div>
				{/if}

				{#if selectedTask.project}
					<div class="detail-row">
						<span class="detail-label">Project</span>
						<span>{selectedTask.project}</span>
					</div>
				{/if}

				{#if selectedTask.summary}
					<div class="detail-row">
						<span class="detail-label">Summary</span>
						<pre class="detail-value mono">{selectedTask.summary}</pre>
					</div>
				{/if}

				{#if selectedTask.error}
					<div class="detail-row">
						<span class="detail-label">Error</span>
						<pre class="detail-value error-text">{selectedTask.error}</pre>
					</div>
				{/if}

				{#if selectedTask.files_modified?.length}
					<div class="detail-row">
						<span class="detail-label">Files Modified</span>
						<div class="files-list mono">
							{#each selectedTask.files_modified as f}
								<div>{f}</div>
							{/each}
						</div>
					</div>
				{/if}

				<div class="detail-row">
					<span class="detail-label">Created</span>
					<span>{selectedTask.created_at}</span>
				</div>
				{#if selectedTask.started_at}
					<div class="detail-row">
						<span class="detail-label">Started</span>
						<span>{selectedTask.started_at}</span>
					</div>
				{/if}
				{#if selectedTask.completed_at}
					<div class="detail-row">
						<span class="detail-label">Completed</span>
						<span>{selectedTask.completed_at}</span>
					</div>
				{/if}

				{#if selectedTask.output?.length}
					<div class="detail-row">
						<span class="detail-label">Output ({selectedTask.output.length} lines)</span>
						<div class="output-container mono">
							{#each selectedTask.output.slice(-50) as line}
								<pre class="output-line">{line}</pre>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.page-title { font-size: 20px; font-weight: 600; margin-bottom: 20px; }
	.text-muted { color: var(--text-secondary); }

	.filters {
		display: flex;
		gap: 8px;
		margin-bottom: 16px;
		align-items: center;
		flex-wrap: wrap;
	}

	.filter-select { width: 150px; }
	.filter-search { flex: 1; min-width: 200px; }

	.task-list {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.task-item {
		cursor: pointer;
		transition: border-color 0.15s;
	}

	.task-item:hover {
		border-color: var(--accent);
	}

	.task-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 6px;
	}

	.task-id {
		font-weight: 600;
		font-family: var(--font-mono);
		color: var(--text-secondary);
	}

	.task-prompt {
		font-size: 14px;
		margin-bottom: 6px;
		line-height: 1.4;
	}

	.task-meta {
		display: flex;
		gap: 12px;
		font-size: 12px;
		color: var(--text-secondary);
	}

	.task-project { color: var(--accent); }

	.task-summary {
		font-size: 12px;
		color: var(--text-secondary);
		margin-top: 4px;
		font-style: italic;
	}

	.pagination {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 12px;
		margin-top: 20px;
	}

	.page-info { font-size: 13px; color: var(--text-secondary); }

	.empty-state { text-align: center; padding: 40px 20px; }

	/* Modal */
	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 100;
		padding: 20px;
	}

	.modal-content {
		width: 100%;
		max-width: 700px;
		max-height: 80vh;
		overflow-y: auto;
		padding: 24px;
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 20px;
	}

	.modal-header h2 { font-size: 18px; }

	.detail-row {
		margin-bottom: 12px;
	}

	.detail-label {
		display: block;
		font-size: 12px;
		color: var(--text-secondary);
		font-weight: 500;
		margin-bottom: 4px;
		text-transform: uppercase;
	}

	.detail-value {
		white-space: pre-wrap;
		word-break: break-word;
		font-size: 13px;
		line-height: 1.5;
		margin: 0;
	}

	.error-text { color: var(--danger); }

	.files-list {
		font-size: 12px;
		color: var(--accent);
	}

	.output-container {
		max-height: 300px;
		overflow-y: auto;
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-radius: 4px;
		padding: 8px;
	}

	.output-line {
		margin: 0;
		white-space: pre-wrap;
		word-break: break-all;
		font-size: 11px;
		line-height: 1.4;
	}

	@media (max-width: 768px) {
		.filter-select {
			width: 100%;
		}

		.filter-search {
			min-width: 0;
			width: 100%;
		}

		.filters {
			flex-direction: column;
		}

		.modal-content {
			padding: 16px;
			max-height: 90vh;
		}

		.task-meta {
			flex-wrap: wrap;
			gap: 6px;
		}

		.pagination {
			gap: 8px;
		}

		.page-info {
			font-size: 12px;
		}
	}
</style>
