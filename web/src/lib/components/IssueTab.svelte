<script lang="ts">
	import { apiGet, apiPost } from '$lib/api.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import IssueDetailModal from '$lib/components/IssueDetailModal.svelte';

	let { projectName }: { projectName: string } = $props();

	let issues = $state([]);
	let loading = $state(false);
	let total = 0;
	let page = $state(1);
	let totalPages = $state(1);
	const limit = 20;
	let loadSeq = 0;

	// Filters
	let filterStatus = $state('');
	let filterType = $state('');
	let searchQ = $state('');

	// Create form
	let showCreate = $state(false);
	let createForm = $state({ title: '', type: 'feature', body: '', goal: '', parent_id: '' });

	// Detail modal — markup lives in IssueDetailModal (#599) so the Shepherd
	// board can reuse it with the shepherd controls (감시/회차/실행).
	let selectedIssueId = $state(null);
	let showingDetail = $state(false);

	const statusLabels = {
		todo: '작업전',
		in_progress: '작업중',
		testing: '테스트',
		failed: '실패',
		done: '성공'
	};

	const statusColors = {
		todo: 'gray',
		in_progress: 'blue',
		testing: 'yellow',
		failed: 'red',
		done: 'green'
	};

	const typeLabels = {
		design: '설계',
		feature: '기능',
		bug: '버그'
	};

	const typeIcons = {
		design: 'layers',
		feature: 'sparkles',
		bug: 'bug'
	};

	async function loadIssues() {
		const seq = ++loadSeq;
		loading = true;
		try {
			let url = `/api/projects/${encodeURIComponent(projectName)}/issues?page=${page}&limit=${limit}`;
			if (filterStatus) url += `&status=${filterStatus}`;
			if (filterType) url += `&type=${filterType}`;
			if (searchQ) url += `&q=${encodeURIComponent(searchQ)}`;

			const res = await apiGet(url);
			// Ignore stale responses so a slow earlier request cannot overwrite
			// the results of the latest filter selection.
			if (seq !== loadSeq) return;
			if (res.success) {
				issues = res.data || [];
				total = res.total || 0;
				totalPages = res.total_pages || 1;
			}
		} catch (e) {
			if (seq === loadSeq) console.error('Failed to load issues:', e);
		} finally {
			if (seq === loadSeq) loading = false;
		}
	}

	async function createIssue() {
		if (!createForm.title.trim()) return;
		try {
			const payload = {
				title: createForm.title,
				type: createForm.type,
				body: createForm.body,
				goal: createForm.goal
			};
			if (createForm.parent_id) {
				const pid = parseInt(createForm.parent_id, 10);
				if (!Number.isNaN(pid) && pid > 0) payload.parent_id = pid;
			}
			const res = await apiPost(`/api/projects/${encodeURIComponent(projectName)}/issues`, payload);
			if (res.success) {
				createForm = { title: '', type: 'feature', body: '', goal: '', parent_id: '' };
				showCreate = false;
				page = 1;
				await loadIssues();
			}
		} catch (e) {
			console.error('Failed to create issue:', e);
		}
	}

	async function openDetail(issueId) {
		selectedIssueId = issueId;
		showingDetail = true;
	}

	function onDetailChanged() {
		loadIssues();
	}

	function openCreate() {
		showCreate = true;
		createForm = { title: '', type: 'feature', body: '', goal: '', parent_id: '' };
	}

	/** Root-ish options for parent picker (current list page; best-effort). */
	function parentOptions() {
		return (issues || []).filter((i) => !i.parent_id);
	}

	function closeCreate() {
		showCreate = false;
	}

	function closeDetail() {
		showingDetail = false;
		selectedIssueId = null;
	}

	function handlePageChange(newPage) {
		page = newPage;
		loadIssues();
	}

	function applyFilters() {
		page = 1;
		loadIssues();
	}

	function resetFilters() {
		filterStatus = '';
		filterType = '';
		searchQ = '';
		page = 1;
		loadIssues();
	}

	onMount(() => loadIssues());

	import { onMount } from 'svelte';
</script>

<div class="issue-tab">
	<!-- Header -->
	<div class="issue-header">
		<h2>이슈트래커</h2>
		<button class="btn btn-primary" onclick={openCreate}><Icon name="plus" size={14} stroke={2.2} /> 이슈 생성</button>
	</div>

	<!-- Filters -->
	<div class="issue-filters">
		<input
			type="text"
			placeholder="제목 검색..."
			class="input input-search search-input"
			bind:value={searchQ}
			oninput={applyFilters}
		/>
		<select class="input" bind:value={filterStatus} onchange={applyFilters} aria-label="상태 필터">
			<option value="">모든 상태</option>
			<option value="todo">작업전</option>
			<option value="in_progress">작업중</option>
			<option value="testing">테스트</option>
			<option value="failed">실패</option>
			<option value="done">성공</option>
		</select>
		<select class="input" bind:value={filterType} onchange={applyFilters} aria-label="타입 필터">
			<option value="">모든 타입</option>
			<option value="design">설계</option>
			<option value="feature">기능</option>
			<option value="bug">버그</option>
		</select>
		<button class="btn btn-ghost" onclick={resetFilters}><Icon name="rotate-ccw" size={13} /> 초기화</button>
	</div>

	<!-- Create Form Modal — only Cancel/× close (no outside-click dismiss; avoid data loss while typing) -->
	{#if showCreate}
	<div class="modal-overlay">
		<div class="modal modal-lg">
			<div class="modal-header">
				<h3>새 이슈 생성</h3>
				<button class="btn btn-ghost btn-icon btn-sm" onclick={closeCreate} aria-label="닫기"><Icon name="x" size={15} /></button>
			</div>
			<div class="modal-body">
				<div class="form-group">
					<label>제목 *</label>
					<input type="text" bind:value={createForm.title} placeholder="이슈 제목을 입력하세요" />
				</div>
				<div class="form-row">
					<div class="form-group">
						<label>타입</label>
						<select bind:value={createForm.type}>
							<option value="feature">기능</option>
							<option value="design">설계</option>
							<option value="bug">버그</option>
						</select>
					</div>
					<div class="form-group">
						<label>상위 이슈 (선택)</label>
						<select bind:value={createForm.parent_id}>
							<option value="">없음 (루트)</option>
							{#each parentOptions() as p}
								<option value={String(p.id)}>#{p.id} {p.title}</option>
							{/each}
						</select>
					</div>
				</div>
				<div class="form-group">
					<label>본문</label>
					<textarea bind:value={createForm.body} placeholder="이슈 상세 내용 (마크다운 지원)" rows="6"></textarea>
				</div>
				<div class="form-group">
					<label>목표 (완료 기준)</label>
					<textarea bind:value={createForm.goal} placeholder="성공 여부를 판단할 기준을 작성하세요" rows="3"></textarea>
				</div>
			</div>
			<div class="modal-footer">
				<button class="btn" onclick={closeCreate}>취소</button>
				<button class="btn btn-primary" onclick={createIssue}>생성</button>
			</div>
		</div>
	</div>
	{/if}

	<!-- Issue Detail Modal — shared component (#599) -->
	{#if showingDetail && selectedIssueId != null}
		<IssueDetailModal
			projectName={projectName}
			issueId={selectedIssueId}
			onClose={closeDetail}
			onChanged={onDetailChanged}
		/>
	{/if}

	<!-- Issue List -->
	{#if loading}
		<div class="issue-list" aria-busy="true">
			{#each Array(4) as _}
				<div class="issue-card"><div class="skeleton" style="height: 14px; width: 55%"></div><div class="skeleton" style="height: 10px; width: 30%; margin-top: 12px"></div></div>
			{/each}
		</div>
	{:else if issues.length === 0}
		<div class="empty-state">
			<span class="empty-glyph" aria-hidden="true"><Icon name="inbox" size={22} /></span>
			<p class="empty-title">아직 이슈가 없습니다</p>
			<p class="empty-sub">설계, 기능, 버그를 이슈로 남기면 양에게 바로 수행을 맡길 수 있습니다.</p>
			<button class="btn btn-primary" onclick={openCreate}><Icon name="plus" size={14} stroke={2.2} /> 첫 번째 이슈 생성</button>
		</div>
	{:else}
		<div class="issue-list">
			{#each issues as issue}
			<div
				class="issue-card"
				class:is-child={!!issue.parent_id}
				role="button"
				tabindex="0"
				onclick={() => openDetail(issue.id)}
				onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openDetail(issue.id); } }}
			>
				<div class="issue-card-header">
					<span class="type-icon {issue.type}" aria-hidden="true"><Icon name={typeIcons[issue.type] || 'file-text'} size={14} /></span>
					<span class="issue-id mono">#{issue.id}</span>
					<span class="issue-title">{issue.title}</span>
					<span class="status-badge {issue.status}">{statusLabels[issue.status] || issue.status}</span>
				</div>
				<div class="issue-card-footer">
					<span class="issue-type-label">{typeLabels[issue.type] || issue.type}</span>
					{#if issue.parent_id}
						<span class="parent-tag">↳ 상위 #{issue.parent_id}</span>
					{/if}
					{#if issue.task_count > 0}
						<span class="task-count">작업 {issue.task_count}개</span>
					{/if}
					<span class="issue-date mono">{issue.created_at}</span>
				</div>
			</div>
			{/each}
		</div>

		<!-- Pagination -->
		{#if totalPages > 1}
		<div class="pagination-bar">
			<button class="btn btn-sm btn-ghost" disabled={page <= 1} onclick={() => handlePageChange(page - 1)}><Icon name="chevron-left" size={13} /> 이전</button>
			<span class="page-info mono">{page} / {totalPages}</span>
			<button class="btn btn-sm btn-ghost" disabled={page >= totalPages} onclick={() => handlePageChange(page + 1)}>다음 <Icon name="chevron-right" size={13} /></button>
		</div>
		{/if}
	{/if}
</div>

<style>
	.issue-tab {
		height: 100%;
		display: flex;
		flex-direction: column;
	}

	.issue-header {
		display: flex;
		justify-content: space-between;
		align-items: flex-end;
		gap: 12px;
		padding: 4px 0 16px;
	}

	.issue-header h2 {
		margin: 0;
		font-family: var(--font-display);
		font-size: 30px;
		font-weight: 400;
		letter-spacing: -0.01em;
	}

	.issue-filters {
		display: flex;
		gap: 8px;
		margin-bottom: 16px;
		flex-wrap: wrap;
	}

	.search-input {
		flex: 1;
		min-width: 180px;
		font-size: var(--fs-sm);
		min-height: 34px;
	}

	.issue-filters select {
		min-height: 34px;
		padding-top: 6px;
		padding-bottom: 6px;
		font-size: var(--fs-sm);
	}

	.issue-list {
		flex: 1;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding-bottom: 16px;
	}

	.issue-card {
		position: relative;
		padding: 12px 16px;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-2);
		box-shadow: var(--sheen);
		cursor: pointer;
		transition: border-color var(--dur-1) ease, background var(--dur-1) ease;
	}

	.issue-card:hover {
		border-color: var(--border-strong);
		background: var(--bg-3);
	}

	.issue-card-header {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
	}

	.type-icon {
		display: grid;
		place-items: center;
		flex-shrink: 0;
		width: 26px;
		height: 26px;
		border-radius: 8px;
		background: rgba(237, 230, 218, 0.05);
		color: var(--text-secondary);
	}

	.type-icon.feature { background: var(--accent-soft); color: var(--accent); }
	.type-icon.bug { background: var(--danger-soft); color: var(--danger); }
	.type-icon.design { background: var(--queued-soft); color: var(--queued); }

	.issue-id {
		flex-shrink: 0;
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		font-variant-numeric: tabular-nums;
	}

	.issue-title {
		flex: 1;
		min-width: 0;
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Sub-issues hang off their parent on a thin thread instead of a thick bar */
	.issue-card.is-child {
		margin-left: 22px;
	}

	.issue-card.is-child::before {
		content: '';
		position: absolute;
		left: -13px;
		top: -7px;
		bottom: 50%;
		width: 10px;
		border-left: 1px solid var(--border-strong);
		border-bottom: 1px solid var(--border-strong);
		border-bottom-left-radius: 8px;
	}

	.parent-tag {
		color: var(--text-secondary);
	}

	.issue-card-footer {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 6px 12px;
		margin-top: 8px;
		padding-left: 36px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.status-badge {
		flex-shrink: 0;
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 1px 9px;
		border: 1px solid transparent;
		border-radius: var(--radius-full);
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		line-height: 1.7;
		white-space: nowrap;
	}

	.status-badge::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: currentColor;
	}

	.status-badge.todo { background: rgba(237, 230, 218, 0.06); color: var(--text-secondary); border-color: var(--border-subtle); }
	.status-badge.todo::before { background: transparent; box-shadow: inset 0 0 0 1.5px currentColor; }
	.status-badge.in_progress { background: var(--live-soft); color: var(--live); border-color: var(--live-line); }
	.status-badge.in_progress::before { animation: px-blink 1s steps(1) infinite; }
	.status-badge.testing { background: var(--queued-soft); color: var(--queued); border-color: var(--queued-line); }
	.status-badge.failed { background: var(--danger-soft); color: var(--danger); border-color: var(--danger-line); }
	.status-badge.done { background: var(--success-soft); color: var(--success); border-color: var(--success-line); }

	.issue-type-label {
		padding: 0 7px;
		line-height: 18px;
		border-radius: var(--radius-full);
		border: 1px solid var(--border-subtle);
		background: rgba(237, 230, 218, 0.04);
		color: var(--text-secondary);
	}

	.task-count {
		color: var(--accent);
	}


	.modal-overlay {
		position: fixed;
		inset: 0;
		z-index: 1000;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 24px;
		background: var(--bg-overlay);
		backdrop-filter: blur(6px) saturate(120%);
		-webkit-backdrop-filter: blur(6px) saturate(120%);
		animation: fade-in var(--dur-2) ease backwards;
	}

	.modal {
		display: flex;
		flex-direction: column;
		width: 100%;
		max-width: 600px;
		max-height: min(86vh, 900px);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-lg);
		background:
			linear-gradient(180deg, rgba(255, 250, 240, 0.03), rgba(255, 250, 240, 0) 160px),
			var(--bg-2);
		box-shadow: var(--shadow-3), var(--sheen);
		animation: modal-in var(--dur-3) var(--ease-out) backwards;
	}

	.modal-lg { max-width: 700px; }

	.modal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		padding: 16px 20px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.modal-header h3 {
		margin: 0;
		font-size: var(--fs-md);
		font-weight: var(--fw-semibold);
	}

	.modal-body {
		flex: 1;
		padding: 20px;
		overflow-y: auto;
	}

	.modal-footer {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
		padding: 12px 20px;
		border-top: 1px solid var(--border-subtle);
	}

	/* Form */
	.form-group {
		margin-bottom: 16px;
	}

	.form-group label {
		display: block;
		margin-bottom: 6px;
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		color: var(--text-secondary);
	}

	.form-group input,
	.form-group select,
	.form-group textarea {
		width: 100%;
		padding: 8px 12px;
		font-size: var(--fs-sm);
		font-family: inherit;
	}

	.form-group textarea {
		resize: vertical;
		line-height: var(--lh-base);
	}

	.form-row {
		display: flex;
		gap: 16px;
	}

	.form-row .form-group { flex: 1; min-width: 0; }


	.pagination-bar {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 12px;
		padding: 12px 0;
	}

	.page-info {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
	}

	/* Empty */
	.empty-state {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
		padding: 56px 16px;
		border: 1px dashed var(--border);
		border-radius: var(--radius-lg);
		text-align: center;
	}

	.empty-glyph {
		display: grid;
		place-items: center;
		width: 48px;
		height: 48px;
		margin-bottom: 4px;
		border-radius: 14px;
		background: var(--accent-soft);
		color: var(--accent);
	}

	.empty-title {
		font-size: var(--fs-md);
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
	}

	.empty-sub {
		max-width: 42ch;
		margin-bottom: 8px;
		font-size: var(--fs-sm);
		color: var(--text-secondary);
	}

	@media (max-width: 640px) {
		.form-row {
			flex-direction: column;
			gap: 0;
		}

		.issue-card-footer {
			padding-left: 0;
		}

		.issue-title {
			white-space: normal;
		}
	}
</style>
