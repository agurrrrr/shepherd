<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';

	let tasks = [];
	let total = 0;
	let page = 1;
	let limit = 30;
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

	function setStatus(s) {
		statusFilter = s;
		page = 1;
		loadTasks();
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
		const runes = [...str];
		return runes.length > max ? runes.slice(0, max).join('') + '...' : str;
	}

	function timeAgo(dateStr) {
		const date = new Date(dateStr.replace(' ', 'T'));
		const now = new Date();
		const diff = Math.floor((now - date) / 1000);
		if (diff < 60) return diff + 's ago';
		if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
		if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
		return Math.floor(diff / 86400) + 'd ago';
	}

	function statusIcon(status) {
		switch (status) {
			case 'completed': return '\u2705';
			case 'failed': return '\u274C';
			case 'running': return '\u26A1';
			case 'pending': return '\u23F3';
			default: return '\u2022';
		}
	}

	const statusTabs = [
		{ value: '', label: 'All' },
		{ value: 'running', label: 'Running' },
		{ value: 'pending', label: 'Pending' },
		{ value: 'completed', label: 'Completed' },
		{ value: 'failed', label: 'Failed' }
	];
</script>

<div class="page">
	<div class="page-header">
		<h1 class="page-title">Tasks</h1>
		<span class="total-badge">{total}</span>
	</div>

	<!-- Status tabs + Search -->
	<div class="toolbar">
		<div class="status-tabs">
			{#each statusTabs as tab}
				<button
					class="tab-btn"
					class:active={statusFilter === tab.value}
					onclick={() => setStatus(tab.value)}
				>{tab.label}</button>
			{/each}
		</div>
		<div class="search-box">
			<input
				class="input search-input"
				type="text"
				bind:value={searchQuery}
				placeholder="Search..."
				onkeydown={(e) => e.key === 'Enter' && applyFilters()}
			/>
		</div>
	</div>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if tasks.length === 0}
		<div class="card empty-state">
			<p>No tasks found.</p>
		</div>
	{:else}
		<!-- Compact Task List -->
		<div class="task-table">
			{#each tasks as t (t.id)}
				<div class="task-row" class:running={t.status === 'running'} onclick={() => viewDetail(t.id)}>
					<span class="row-icon">{statusIcon(t.status)}</span>
					<span class="row-id">#{t.id}</span>
					<div class="row-body">
						<span class="row-prompt">{truncate(t.summary || t.prompt, 80)}</span>
						<div class="row-tags">
							{#if t.project}
								<span class="tag tag-project">{t.project}</span>
							{/if}
							{#if t.sheep}
								<span class="tag tag-sheep">{t.sheep}</span>
							{/if}
						</div>
					</div>
					<span class="row-time">{timeAgo(t.created_at)}</span>
				</div>
			{/each}
		</div>

		<!-- Pagination -->
		{#if totalPages > 1}
			<div class="pagination">
				<button class="btn btn-sm" disabled={page <= 1} onclick={() => gotoPage(page - 1)}>Prev</button>
				<span class="page-info">Page {page} / {totalPages}</span>
				<button class="btn btn-sm" disabled={page >= totalPages} onclick={() => gotoPage(page + 1)}>Next</button>
			</div>
		{/if}
	{/if}

	<!-- Detail Modal -->
	{#if showDetail && selectedTask}
		<div class="modal-overlay" onclick={closeDetail}>
			<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
				<div class="modal-header">
					<div class="modal-title-row">
						<h2>Task #{selectedTask.id}</h2>
						<StatusBadge status={selectedTask.status} />
					</div>
					<button class="btn btn-sm" onclick={closeDetail}>Close</button>
				</div>

				<div class="detail-section">
					<div class="detail-label">Prompt</div>
					<pre class="detail-value mono">{selectedTask.prompt}</pre>
				</div>

				{#if selectedTask.summary}
					<div class="detail-section">
						<div class="detail-label">Summary</div>
						<pre class="detail-value mono">{selectedTask.summary}</pre>
					</div>
				{/if}

				{#if selectedTask.error}
					<div class="detail-section">
						<div class="detail-label">Error</div>
						<pre class="detail-value error-text">{selectedTask.error}</pre>
					</div>
				{/if}

				<div class="detail-meta">
					{#if selectedTask.sheep}
						<div class="meta-item"><span class="meta-label">Sheep</span> {selectedTask.sheep}</div>
					{/if}
					{#if selectedTask.project}
						<div class="meta-item"><span class="meta-label">Project</span> {selectedTask.project}</div>
					{/if}
					<div class="meta-item"><span class="meta-label">Created</span> {selectedTask.created_at}</div>
					{#if selectedTask.started_at}
						<div class="meta-item"><span class="meta-label">Started</span> {selectedTask.started_at}</div>
					{/if}
					{#if selectedTask.completed_at}
						<div class="meta-item"><span class="meta-label">Completed</span> {selectedTask.completed_at}</div>
					{/if}
				</div>

				{#if selectedTask.files_modified?.length}
					<div class="detail-section">
						<div class="detail-label">Files Modified ({selectedTask.files_modified.length})</div>
						<div class="files-list mono">
							{#each selectedTask.files_modified as f}
								<div>{f}</div>
							{/each}
						</div>
					</div>
				{/if}

				{#if selectedTask.output?.length}
					<div class="detail-section">
						<div class="detail-label">Output ({selectedTask.output.length} lines)</div>
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
	.page { max-width: 1200px; }

	.page-header {
		display: flex;
		align-items: center;
		gap: 10px;
		margin-bottom: 16px;
	}

	.page-title { font-size: 20px; font-weight: 600; }

	.total-badge {
		font-size: 12px;
		font-family: var(--font-mono);
		background: var(--bg-tertiary);
		padding: 2px 8px;
		border-radius: 10px;
		color: var(--text-secondary);
	}

	.text-muted { color: var(--text-secondary); }

	/* Toolbar */
	.toolbar {
		display: flex;
		gap: 10px;
		margin-bottom: 12px;
		align-items: center;
		flex-wrap: wrap;
	}

	.status-tabs {
		display: flex;
		gap: 2px;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 2px;
	}

	.tab-btn {
		padding: 5px 12px;
		border: none;
		background: transparent;
		color: var(--text-secondary);
		font-size: 13px;
		border-radius: 4px;
		cursor: pointer;
		transition: all 0.15s;
	}

	.tab-btn:hover {
		color: var(--text-primary);
	}

	.tab-btn.active {
		background: var(--bg-tertiary);
		color: var(--accent);
		font-weight: 600;
	}

	.search-box { flex: 1; min-width: 150px; }
	.search-input { width: 100%; padding: 6px 10px; }

	/* Task Table */
	.task-table {
		display: flex;
		flex-direction: column;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
	}

	.task-row {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 8px 12px;
		border-bottom: 1px solid var(--border);
		cursor: pointer;
		transition: background 0.1s;
		background: var(--bg-secondary);
	}

	.task-row:last-child {
		border-bottom: none;
	}

	.task-row:hover {
		background: var(--bg-tertiary);
	}

	.task-row.running {
		border-left: 3px solid var(--accent);
	}

	.row-icon {
		flex-shrink: 0;
		font-size: 13px;
		width: 18px;
		text-align: center;
	}

	.row-id {
		flex-shrink: 0;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--text-secondary);
		width: 50px;
	}

	.row-body {
		flex: 1;
		min-width: 0;
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.row-prompt {
		font-size: 13px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		flex: 1;
		min-width: 0;
	}

	.row-tags {
		display: flex;
		gap: 4px;
		flex-shrink: 0;
	}

	.tag {
		font-size: 11px;
		padding: 1px 6px;
		border-radius: 3px;
		white-space: nowrap;
	}

	.tag-project {
		background: rgba(88, 166, 255, 0.12);
		color: var(--accent);
	}

	.tag-sheep {
		background: var(--bg-tertiary);
		color: var(--text-secondary);
	}

	.row-time {
		flex-shrink: 0;
		font-size: 11px;
		font-family: var(--font-mono);
		color: var(--text-secondary);
		width: 55px;
		text-align: right;
	}

	/* Pagination */
	.pagination {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 12px;
		margin-top: 16px;
	}

	.btn-sm { padding: 4px 12px; font-size: 12px; }
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
		padding: 20px;
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 16px;
	}

	.modal-title-row {
		display: flex;
		align-items: center;
		gap: 10px;
	}

	.modal-title-row h2 { font-size: 16px; }

	.detail-section {
		margin-bottom: 14px;
	}

	.detail-label {
		font-size: 11px;
		color: var(--text-secondary);
		font-weight: 500;
		margin-bottom: 4px;
		text-transform: uppercase;
		letter-spacing: 0.03em;
	}

	.detail-value {
		white-space: pre-wrap;
		word-break: break-word;
		font-size: 13px;
		line-height: 1.5;
		margin: 0;
	}

	.error-text { color: var(--danger); }

	.detail-meta {
		display: flex;
		gap: 16px;
		flex-wrap: wrap;
		margin-bottom: 14px;
		padding: 8px 0;
		border-top: 1px solid var(--border);
		border-bottom: 1px solid var(--border);
	}

	.meta-item {
		font-size: 13px;
	}

	.meta-label {
		font-size: 11px;
		color: var(--text-secondary);
		margin-right: 4px;
	}

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
		.toolbar {
			flex-direction: column;
		}

		.status-tabs {
			width: 100%;
			overflow-x: auto;
		}

		.search-box {
			width: 100%;
			min-width: 0;
		}

		.row-tags {
			display: none;
		}

		.row-id {
			width: 40px;
		}

		.task-row {
			padding: 8px 10px;
		}

		.modal-content {
			padding: 14px;
			max-height: 90vh;
		}

		.detail-meta {
			flex-direction: column;
			gap: 6px;
		}
	}
</style>
