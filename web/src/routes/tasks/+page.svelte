<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import Tabs from '$lib/components/Tabs.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import MarkdownBody from '$lib/components/MarkdownBody.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';

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
	let detailChanges = null;

	onMount(async () => {
		await loadTasks();

		unsubs.push(onSSE('task_complete', () => loadTasks()));
		unsubs.push(onSSE('task_start', () => loadTasks()));
		unsubs.push(onSSE('task_fail', () => loadTasks()));
		unsubs.push(onSSE('task_stop', () => loadTasks()));
		unsubs.push(onSSE('task_update', (data) => {
			loadTasks();
			if (selectedTask && data.task_id === selectedTask.id) {
				selectedTask = { ...selectedTask, reverted_at: data.reverted_at || '' };
				loadDetailChanges(selectedTask.id);
			}
		}));
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
			detailChanges = null;
			loadDetailChanges(taskId);
		}
	}

	async function loadDetailChanges(taskId) {
		const res = await apiGet(`/api/tasks/${taskId}/changes`);
		if (res?.data && selectedTask?.id === taskId) {
			detailChanges = res.data;
		}
	}

	function closeDetail() {
		showDetail = false;
		selectedTask = null;
		detailChanges = null;
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

	// Duration in HH:MM:SS format.
	function fmtDuration(sec) {
		const s = Math.max(0, Math.floor(Number(sec) || 0));
		const hh = String(Math.floor(s / 3600)).padStart(2, '0');
		const mm = String(Math.floor((s % 3600) / 60)).padStart(2, '0');
		const ss = String(s % 60).padStart(2, '0');
		return `${hh}:${mm}:${ss}`;
	}

	// Compact token count for inline badges.
	function fmtTokensShort(n) {
		const v = Number(n) || 0;
		if (v >= 1000000) return (v / 1000000).toFixed(1) + 'M';
		if (v >= 1000) return (v / 1000).toFixed(1) + 'K';
		return String(v);
	}

	function statusIcon(status) {
		switch (status) {
			case 'completed': return { name: 'check', tone: 'success' };
			case 'failed': return { name: 'x', tone: 'danger' };
			case 'running': return { name: 'zap', tone: 'live' };
			case 'pending': return { name: 'hourglass', tone: 'queued' };
			case 'stopped': return { name: 'square', tone: 'warning' };
			default: return { name: 'circle', tone: 'idle' };
		}
	}

	const statusTabs = [
		{ value: '', label: 'All' },
		{ value: 'running', label: 'Running' },
		{ value: 'pending', label: 'Pending' },
		{ value: 'completed', label: 'Completed' },
		{ value: 'failed', label: 'Failed' },
		{ value: 'stopped', label: 'Stopped' }
	];
</script>

<div class="page">
	<PageHeader
		eyebrow="Watch · Log"
		title="Tasks"
		count={total.toLocaleString()}
		subtitle="Every task the flock has taken on, newest first. Open one to read its prompt, summary and changes."
	/>

	<!-- Status tabs + Search -->
	<div class="toolbar">
		<Tabs tabs={statusTabs} value={statusFilter} onChange={setStatus} ariaLabel="Status filter" />
		<div class="search-box">
			<input
				class="input input-search search-input"
				type="text"
				bind:value={searchQuery}
				placeholder="Search prompts and summaries…"
				aria-label="Search tasks"
				onkeydown={(e) => e.key === 'Enter' && applyFilters()}
			/>
		</div>
	</div>

	{#if !loaded}
		<div class="task-table" aria-busy="true">
			{#each Array(8) as _, i}
				<div class="task-row skeleton-row">
					<span class="skeleton" style="width: 22px; height: 22px"></span>
					<span class="skeleton" style="height: 12px; width: 48px"></span>
					<span class="skeleton" style="height: 12px; width: {40 + ((i * 37) % 45)}%"></span>
				</div>
			{/each}
		</div>
	{:else if tasks.length === 0}
		<div class="card">
			<EmptyState
				icon="inbox"
				title={statusFilter || searchQuery ? 'No matches' : 'No tasks yet'}
				description={statusFilter || searchQuery ? 'Try clearing filters or searching differently.' : 'Tasks you start from the dashboard or schedules will appear here.'}
			/>
		</div>
	{:else}
		<!-- Compact Task List -->
		<div class="task-table">
			{#each tasks as t, i (t.id)}
				{@const icn = statusIcon(t.status)}
				<div
					class="task-row reveal"
					class:running={t.status === 'running'}
					style="--i: {i}"
					role="button"
					tabindex="0"
					aria-label={`Task #${t.id}, ${t.status}`}
					onclick={() => viewDetail(t.id)}
					onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), viewDetail(t.id))}
				>
					<span class="row-icon" data-tone={icn.tone}>
						<Icon name={icn.name} size={12} stroke={2.4} label={t.status} />
					</span>
					<span class="row-id mono num">#{t.id}</span>
					<div class="row-body">
						<span class="row-prompt">{truncate(t.summary || t.prompt, 140)}</span>
						<div class="row-tags">
							{#if t.project}
								<span class="chip">{t.project}</span>
							{:else if t.shepherd_kind}
								<span class="chip chip-accent" title="목자 작업">목자</span>
							{/if}
							{#if t.sheep}
								<span class="row-sheep">{t.sheep}</span>
							{/if}
							{#if t.reverted_at}
								<span class="chip chip-queued" title={`되돌림 ${t.reverted_at}`}>되돌림</span>
							{/if}
						</div>
					</div>
					<span class="metric mono num" title="실행 시간">{t.duration_sec > 0 ? fmtDuration(t.duration_sec) : ''}</span>
					<span class="metric mono num" title="토큰 사용량">{t.total_tokens > 0 ? fmtTokensShort(t.total_tokens) : ''}</span>
					<span class="row-time">{timeAgo(t.created_at)}</span>
				</div>
			{/each}
		</div>

		<!-- Pagination -->
		{#if totalPages > 1}
			<nav class="pagination" aria-label="Pagination">
				<button class="btn btn-sm btn-ghost" disabled={page <= 1} onclick={() => gotoPage(page - 1)}>
					<Icon name="arrow-left" size={13} /> Newer
				</button>
				<span class="page-info mono num">{page} <span>/</span> {totalPages}</span>
				<button class="btn btn-sm btn-ghost" disabled={page >= totalPages} onclick={() => gotoPage(page + 1)}>
					Older <Icon name="arrow-right" size={13} />
				</button>
			</nav>
		{/if}
	{/if}

	<!-- Detail Modal -->
	{#if showDetail && selectedTask}
		<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
		<div class="modal-overlay" onclick={closeDetail}>
			<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
			<div class="modal-content card" role="dialog" aria-modal="true" aria-label={`Task #${selectedTask.id}`} onclick={(e) => e.stopPropagation()}>
				<div class="modal-header">
					<div class="modal-title-row">
						<h2>Task #{selectedTask.id}</h2>
						<StatusBadge status={selectedTask.status} />
						{#if selectedTask.reverted_at}
							<span class="chip chip-queued">되돌림</span>
						{/if}
					</div>
					<button class="btn btn-sm btn-ghost btn-icon" onclick={closeDetail} aria-label="Close"><Icon name="x" size={15} /></button>
				</div>

				<div class="detail-section">
					<div class="detail-label">Prompt</div>
					<pre class="detail-value mono">{selectedTask.prompt}</pre>
				</div>

				{#if selectedTask.summary}
					<div class="detail-section">
						<div class="detail-label">Summary</div>
						<div class="detail-value">
							<MarkdownBody text={selectedTask.summary} />
						</div>
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
					{#if selectedTask.duration_sec > 0}
						<div class="meta-item"><span class="meta-label">Duration</span> {fmtDuration(selectedTask.duration_sec)}</div>
					{/if}
					{#if selectedTask.total_tokens > 0}
						<div class="meta-item"><span class="meta-label">Tokens</span> {selectedTask.total_tokens.toLocaleString()} ({(selectedTask.prompt_tokens ?? 0).toLocaleString()} in / {(selectedTask.completion_tokens ?? 0).toLocaleString()} out)</div>
					{/if}
	
				</div>

				<div class="detail-section changes-detail">
					<div class="detail-label">Changes</div>
					{#if detailChanges?.available}
						<div class="changes-summary">
							{detailChanges.files.length} files · +{detailChanges.additions} −{detailChanges.deletions}
							{#if detailChanges.commits_during_task?.length}
								<span class="commit-badge">커밋 {detailChanges.commits_during_task.length}개 포함</span>
							{/if}
						</div>
					{:else if detailChanges}
						<div class="changes-hint">
							{#if selectedTask.shepherd_kind}
								목자 작업은 체크포인트를 남기지 않습니다
							{:else}
								{detailChanges.reason || '변경 사항을 볼 수 없습니다.'}
							{/if}
						</div>
					{/if}
					<a class="detail-link" href={`/tasks/${selectedTask.id}`}>변경 사항 상세 보기 <Icon name="arrow-right" size={12} /></a>
				</div>

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
	.page { max-width: var(--content-max); }

	/* Toolbar */
	.toolbar {
		display: flex;
		gap: 12px;
		margin-bottom: 16px;
		align-items: center;
		flex-wrap: wrap;
	}

	.search-box { flex: 1; min-width: 200px; max-width: 420px; margin-left: auto; }
	.search-input { width: 100%; padding-top: 7px; padding-bottom: 7px; font-size: var(--fs-sm); }

	/* Task table — one surface, hairline rows */
	.task-table {
		display: flex;
		flex-direction: column;
		padding: 4px;
		border: 0;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel);
	}

	.task-row {
		position: relative;
		display: grid;
		grid-template-columns: 22px 54px minmax(0, 1fr) 66px 52px 64px;
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		border-radius: 0;
		cursor: pointer;
		transition: background var(--dur-1) steps(2);
	}

	.task-row + .task-row {
		box-shadow: 0 -2px 0 0 var(--lo);
	}

	.task-row:hover,
	.task-row:focus-visible {
		background: var(--bg-3);
	}

	.task-row:focus-visible {
		outline-offset: -3px;
	}

	.task-row.running {
		background: var(--live-soft);
	}

	.task-row.running::before {
		content: '';
		position: absolute;
		left: -4px;
		top: 10px;
		bottom: 10px;
		width: 3px;
		border-radius: 0;
		background: var(--live);
	}

	.skeleton-row {
		cursor: default;
		grid-template-columns: 22px 54px 1fr;
	}

	.row-icon {
		display: grid;
		place-items: center;
		width: 22px;
		height: 22px;
		border-radius: 0;
		color: #000;
		background: var(--p-dgray);
		box-shadow: var(--px-ring-2);
	}

	/* Status fill with a black glyph */
	.row-icon[data-tone="success"] { background: var(--success); }
	.row-icon[data-tone="live"] { background: var(--live); }
	.row-icon[data-tone="danger"] { background: var(--danger); }
	.row-icon[data-tone="queued"] { background: var(--queued); }
	.row-icon[data-tone="warning"] { background: var(--warning); }

	.row-id {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.row-body {
		min-width: 0;
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.row-prompt {
		flex: 1;
		min-width: 0;
		font-size: var(--fs-sm);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.row-tags {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-shrink: 0;
		max-width: 40%;
	}

	.row-sheep {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.metric {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		text-align: right;
		white-space: nowrap;
	}

	.row-time {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		text-align: right;
		white-space: nowrap;
	}

	/* Pagination */
	.pagination {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 16px;
		margin-top: 20px;
	}

	.page-info {
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.page-info span {
		color: var(--text-tertiary);
		margin: 0 2px;
	}

	/* Modal */
	.modal-content {
		max-width: 760px;
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 20px;
	}

	.modal-title-row {
		display: flex;
		align-items: center;
		gap: 12px;
	}

	.modal-title-row h2 {
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		font-weight: 400;
		letter-spacing: 0;
	}

	.detail-section {
		margin-bottom: 18px;
	}

	.detail-label {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		margin-bottom: 6px;
		text-transform: uppercase;
		letter-spacing: var(--tracking-caps);
	}

	.detail-value {
		white-space: pre-wrap;
		word-break: break-word;
		font-size: var(--fs-sm);
		line-height: 1.6;
		margin: 0;
	}

	pre.detail-value {
		padding: 12px 14px;
		border-radius: 0;
		background: #000;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		max-height: 260px;
		overflow: auto;
	}

	.error-text { color: var(--danger-text); }

	.detail-meta {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
		gap: 12px 16px;
		margin-bottom: 18px;
		padding: 14px 0;
		border-top: 1px solid var(--border-subtle);
		border-bottom: 1px solid var(--border-subtle);
	}

	.meta-item {
		display: flex;
		flex-direction: column;
		gap: 2px;
		font-size: var(--fs-xs);
		color: var(--text-primary);
	}

	.meta-label {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.changes-summary {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		font-family: var(--font-mono);
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.commit-badge {
		font-size: var(--fs-2xs);
		padding: 1px 7px;
		border-radius: 0;
		background: var(--accent-soft);
		color: var(--accent);
	}

	.changes-hint {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
	}

	.detail-link {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-top: 8px;
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		color: var(--accent);
	}

	.output-container {
		max-height: 300px;
		overflow-y: auto;
		background: #000;
		border-radius: 0;
		padding: 10px 12px;
	}

	.output-line {
		margin: 0;
		white-space: pre-wrap;
		word-break: break-all;
		font-size: var(--fs-2xs);
		line-height: 1.5;
		color: var(--text-secondary);
	}

	@media (max-width: 960px) {
		.row-tags .row-sheep {
			display: none;
		}
	}

	@media (max-width: 768px) {
		.toolbar {
			flex-direction: column;
			align-items: stretch;
		}

		.search-box {
			width: 100%;
			max-width: none;
			min-width: 0;
			margin-left: 0;
		}

		.task-row {
			grid-template-columns: 22px minmax(0, 1fr) auto;
			gap: 10px;
			padding: 10px 8px;
		}

		.row-id,
		.metric {
			display: none;
		}

		.row-body {
			flex-direction: column;
			align-items: flex-start;
			gap: 6px;
		}

		.row-prompt {
			white-space: normal;
			display: -webkit-box;
			-webkit-line-clamp: 2;
			-webkit-box-orient: vertical;
		}

		.row-tags {
			max-width: 100%;
		}

		.modal-content {
			padding: 18px;
			max-height: 90vh;
		}
	}
</style>
