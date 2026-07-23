<script lang="ts">
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';

	let { projectName }: { projectName: string } = $props();

	let issues = $state([]);
	let loading = $state(false);
	let total = 0;
	let page = 1;
	let totalPages = 1;
	const limit = 20;

	// Filters
	let filterStatus = $state('');
	let filterType = $state('');
	let searchQ = $state('');

	// Create form
	let showCreate = $state(false);
	let createForm = $state({ title: '', type: 'feature', body: '', goal: '', parent_id: '' });

	// Detail modal
	let selectedIssue = $state(null);
	let showingDetail = $state(false);

	// Edit inline
	let editingField = $state(null); // 'title' | 'body' | 'goal' | null
	let editValue = $state('');

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
		design: '🎨',
		feature: '✨',
		bug: '🐛'
	};

	async function loadIssues() {
		loading = true;
		try {
			let url = `/api/projects/${encodeURIComponent(projectName)}/issues?page=${page}&limit=${limit}`;
			if (filterStatus) url += `&status=${filterStatus}`;
			if (filterType) url += `&type=${filterType}`;
			if (searchQ) url += `&q=${encodeURIComponent(searchQ)}`;

			const res = await apiGet(url);
			if (res.success) {
				issues = res.data || [];
				total = res.total || 0;
				totalPages = res.total_pages || 1;
			}
		} catch (e) {
			console.error('Failed to load issues:', e);
		} finally {
			loading = false;
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

	async function executeIssue(issueId) {
		const hasChildren = selectedIssue?.id === issueId && selectedIssue?.children?.length > 0;
		const incomplete = hasChildren
			? selectedIssue.children.filter((c) => c.status !== 'done').length
			: 0;
		const msg =
			incomplete > 0
				? `미완료 하위 이슈 ${incomplete}개를 먼저 큐에 넣고, 마지막에 이 이슈를 수행합니다. 계속할까요?`
				: '이 이슈를 수행하시겠습니까?';
		if (!confirm(msg)) return;
		try {
			const res = await apiPost(`/api/projects/${encodeURIComponent(projectName)}/issues/${issueId}/execute`, {});
			if (res?.data?.task_ids?.length > 1) {
				alert(
					`작업 ${res.data.task_ids.length}개가 큐에 추가되었습니다.\n이슈: ${JSON.stringify(res.data.issue_ids)}\nTask: ${JSON.stringify(res.data.task_ids)}`
				);
			}
			await loadIssues();
			if (showingDetail && selectedIssue?.id === issueId) {
				await loadIssueDetail(issueId);
			}
		} catch (e) {
			console.error('Failed to execute issue:', e);
			alert('수행 요청 실패: ' + (e?.message || e));
		}
	}

	async function updateIssueStatus(issueId, newStatus) {
		try {
			await apiPatch(`/api/projects/${encodeURIComponent(projectName)}/issues/${issueId}`, { status: newStatus });
			await loadIssues();
			if (showingDetail && selectedIssue?.id === issueId) {
				await loadIssueDetail(issueId);
			}
		} catch (e) {
			console.error('Failed to update issue status:', e);
		}
	}

	async function deleteIssue(issueId) {
		if (!confirm('이 이슈를 삭제하시겠습니까? 연결된 작업(Task)은 보존됩니다.')) return;
		try {
			await apiDelete(`/api/projects/${encodeURIComponent(projectName)}/issues/${issueId}`);
			await loadIssues();
			showingDetail = false;
			selectedIssue = null;
		} catch (e) {
			console.error('Failed to delete issue:', e);
		}
	}

	async function loadIssueDetail(issueId) {
		try {
			const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/issues/${issueId}`);
			if (res.success) {
				selectedIssue = res.data;
				showingDetail = true;
			}
		} catch (e) {
			console.error('Failed to load issue detail:', e);
		}
	}

	function openCreate() {
		showCreate = true;
		createForm = { title: '', type: 'feature', body: '', goal: '', parent_id: '' };
	}

	/** Root-ish options for parent picker (current list page; best-effort). */
	function parentOptions() {
		return (issues || []).filter((i) => !i.parent_id);
	}

	function childDoneCount(issue) {
		const kids = issue?.children || [];
		if (!kids.length) return null;
		const done = kids.filter((c) => c.status === 'done').length;
		return { done, total: kids.length };
	}

	function closeCreate() {
		showCreate = false;
	}

	function closeDetail() {
		showingDetail = false;
		selectedIssue = null;
	}

	function startEdit(field) {
		if (!selectedIssue) return;
		editingField = field;
		editValue = selectedIssue[field] || '';
	}

	async function saveEdit() {
		if (!selectedIssue || !editingField) return;
		try {
			await apiPatch(`/api/projects/${encodeURIComponent(projectName)}/issues/${selectedIssue.id}`, {
				[editingField]: editValue
			});
			editingField = null;
			await loadIssueDetail(selectedIssue.id);
		} catch (e) {
			console.error('Failed to save edit:', e);
		}
	}

	function cancelEdit() {
		editingField = null;
		editValue = '';
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
		<button class="btn btn-primary" onclick={openCreate}>+ 이슈 생성</button>
	</div>

	<!-- Filters -->
	<div class="issue-filters">
		<input
			type="text"
			placeholder="제목 검색..."
			class="search-input"
			bind:value={searchQ}
			oninput={applyFilters}
		/>
		<select bind:value={filterStatus} oninput={applyFilters}>
			<option value="">모든 상태</option>
			<option value="todo">작업전</option>
			<option value="in_progress">작업중</option>
			<option value="testing">테스트</option>
			<option value="failed">실패</option>
			<option value="done">성공</option>
		</select>
		<select bind:value={filterType} oninput={applyFilters}>
			<option value="">모든 타입</option>
			<option value="design">설계</option>
			<option value="feature">기능</option>
			<option value="bug">버그</option>
		</select>
		<button class="btn btn-sm" onclick={resetFilters}>초기화</button>
	</div>

	<!-- Create Form Modal — only Cancel/× close (no outside-click dismiss; avoid data loss while typing) -->
	{#if showCreate}
	<div class="modal-overlay">
		<div class="modal modal-lg">
			<div class="modal-header">
				<h3>새 이슈 생성</h3>
				<button class="btn-close" onclick={closeCreate}>×</button>
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

	<!-- Issue Detail Modal -->
	{#if showingDetail && selectedIssue}
	<div class="modal-overlay" onclick={closeDetail}>
		<div class="modal modal-xl" onclick={(e) => e.stopPropagation()}>
			<div class="modal-header">
				<div class="detail-header-left">
					<span class="type-badge">{typeIcons[selectedIssue.type] || '📋'} {typeLabels[selectedIssue.type] || selectedIssue.type}</span>
					{#if editingField === 'title'}
						<input type="text" class="edit-input" bind:value={editValue} onkeydown={(e) => e.key === 'Enter' && saveEdit()} />
					{:else}
						<h3 onclick={() => startEdit('title')} title="클릭하여 수정">
							{selectedIssue.title}
						</h3>
					{/if}
				</div>
				<div class="detail-header-right">
					{#if selectedIssue.status === 'testing'}
						<button class="btn btn-sm btn-success" onclick={() => updateIssueStatus(selectedIssue.id, 'done')}>✅ 성공</button>
						<button class="btn btn-sm btn-danger" onclick={() => updateIssueStatus(selectedIssue.id, 'failed')}>❌ 실패</button>
					{/if}
					{#if (selectedIssue.status === 'todo' || selectedIssue.status === 'testing' || selectedIssue.status === 'failed')}
						<button class="btn btn-sm btn-primary" onclick={() => executeIssue(selectedIssue.id)}>▶ 수행</button>
					{/if}
					<button class="btn btn-sm" onclick={() => deleteIssue(selectedIssue.id)}>🗑</button>
					<button class="btn-close" onclick={closeDetail}>×</button>
				</div>
			</div>
			<div class="modal-body detail-body">
				<div class="detail-status-bar">
					<span class="status-label">상태:</span>
					<span class="status-badge {selectedIssue.status}">{statusLabels[selectedIssue.status] || selectedIssue.status}</span>
					{#if selectedIssue.parent_id}
						<span class="detail-meta">
							상위:
							<button class="linkish" onclick={() => loadIssueDetail(selectedIssue.parent_id)}>
								#{selectedIssue.parent_id}{selectedIssue.parent ? ` ${selectedIssue.parent.title}` : ''}
							</button>
						</span>
					{/if}
					{#if selectedIssue.started_at}
						<span class="detail-meta">시작: {selectedIssue.started_at}</span>
					{/if}
					{#if selectedIssue.completed_at}
						<span class="detail-meta">마감: {selectedIssue.completed_at}</span>
					{/if}
					<span class="detail-meta">생성: {selectedIssue.created_at}</span>
				</div>

				<!-- Body -->
				<div class="detail-section">
					<h4 onclick={() => startEdit('body')} title="클릭하여 수정">이슈 내용</h4>
					{#if editingField === 'body'}
						<textarea class="edit-textarea" bind:value={editValue} rows="8" onkeydown={(e) => e.key === 'Enter' && e.ctrlKey && saveEdit()}></textarea>
						<div class="edit-actions">
							<button class="btn btn-sm btn-primary" onclick={saveEdit}>저장</button>
							<button class="btn btn-sm" onclick={cancelEdit}>취소</button>
						</div>
					{:else}
						<div class="detail-content">{selectedIssue.body || '내용이 없습니다.'}</div>
					{/if}
				</div>

				<!-- Children checklist (auto, live status — not stored in body) -->
				{#if selectedIssue.children && selectedIssue.children.length > 0}
				<div class="detail-section">
					<h4>
						하위 이슈
						{#if childDoneCount(selectedIssue)}
							<span class="child-progress">
								({childDoneCount(selectedIssue).done}/{childDoneCount(selectedIssue).total} 완료)
							</span>
						{/if}
					</h4>
					<div class="children-list">
						{#each selectedIssue.children as child}
						<div class="child-row" class:child-done={child.status === 'done'}>
							<span class="child-check" aria-hidden="true">
								{child.status === 'done' ? '✅' : '⬜'}
							</span>
							<button class="child-link" onclick={() => loadIssueDetail(child.id)}>
								#{child.id} {child.title}
							</button>
							<span class="status-badge {child.status}">{statusLabels[child.status] || child.status}</span>
						</div>
						{/each}
					</div>
					<p class="children-hint">▶ 수행 시 미완료 하위가 먼저 큐에 들어가고, 이 이슈가 마지막에 들어갑니다.</p>
				</div>
				{/if}

				<!-- Goal -->
				<div class="detail-section">
					<h4 onclick={() => startEdit('goal')} title="클릭하여 수정">목표 (완료 기준)</h4>
					{#if editingField === 'goal'}
						<textarea class="edit-textarea" bind:value={editValue} rows="4" onkeydown={(e) => e.key === 'Enter' && e.ctrlKey && saveEdit()}></textarea>
						<div class="edit-actions">
							<button class="btn btn-sm btn-primary" onclick={saveEdit}>저장</button>
							<button class="btn btn-sm" onclick={cancelEdit}>취소</button>
						</div>
					{:else}
						<div class="detail-content goal-content">{selectedIssue.goal || '목표가 설정되지 않았습니다.'}</div>
					{/if}
				</div>

				<!-- Linked Tasks -->
				{#if selectedIssue.tasks && selectedIssue.tasks.length > 0}
				<div class="detail-section">
					<h4>연결된 작업 ({selectedIssue.tasks.length})</h4>
					<div class="linked-tasks">
						{#each selectedIssue.tasks as task}
						<div class="linked-task">
							<a href="#/history" class="task-link">작업 #{task.id}</a>
							<span class="task-status-badge {task.status}">{task.status}</span>
							{#if task.summary}
								<span class="task-summary">{task.summary}</span>
							{/if}
							<span class="task-date">{task.created_at}</span>
						</div>
						{/each}
					</div>
				</div>
				{/if}
			</div>
			<div class="modal-footer">
				<button class="btn" onclick={closeDetail}>닫기</button>
			</div>
		</div>
	</div>
	{/if}

	<!-- Issue List -->
	{#if loading}
		<div class="loading">로딩 중...</div>
	{:else if issues.length === 0}
		<div class="empty-state">
			<p>아직 이슈가 없습니다.</p>
			<button class="btn btn-primary" onclick={openCreate}>+ 첫 번째 이슈 생성</button>
		</div>
	{:else}
		<div class="issue-list">
			{#each issues as issue}
			<div class="issue-card" class:is-child={!!issue.parent_id} onclick={() => loadIssueDetail(issue.id)}>
				<div class="issue-card-header">
					<span class="type-icon">{typeIcons[issue.type] || '📋'}</span>
					<span class="issue-id">#{issue.id}</span>
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
					<span class="issue-date">{issue.created_at}</span>
				</div>
			</div>
			{/each}
		</div>

		<!-- Pagination -->
		{#if totalPages > 1}
		<div class="pagination-bar">
			<button class="btn btn-sm" disabled={page <= 1} onclick={() => handlePageChange(page - 1)}>이전</button>
			<span class="page-info">{page} / {totalPages}</span>
			<button class="btn btn-sm" disabled={page >= totalPages} onclick={() => handlePageChange(page + 1)}>다음</button>
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
		align-items: center;
		padding: 0.5rem 0 1rem;
	}

	.issue-header h2 {
		margin: 0;
		font-size: 1.25rem;
	}

	.issue-filters {
		display: flex;
		gap: 0.5rem;
		margin-bottom: 1rem;
		flex-wrap: wrap;
	}

	.search-input {
		flex: 1;
		min-width: 150px;
		padding: 0.4rem 0.75rem;
		border: 1px solid var(--border-color, #444);
		border-radius: 4px;
		background: var(--input-bg, #1e1e2e);
		color: var(--text-color, #cdd6f4);
		font-size: 0.85rem;
	}

	.issue-filters select {
		padding: 0.4rem 0.5rem;
		border: 1px solid var(--border-color, #444);
		border-radius: 4px;
		background: var(--input-bg, #1e1e2e);
		color: var(--text-color, #cdd6f4);
		font-size: 0.85rem;
	}

	.issue-list {
		flex: 1;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		padding-bottom: 1rem;
	}

	.issue-card {
		border: 1px solid var(--border-color, #444);
		border-radius: 8px;
		padding: 0.75rem 1rem;
		cursor: pointer;
		transition: border-color 0.2s;
	}

	.issue-card:hover {
		border-color: var(--accent-color, #89b4fa);
	}

	.issue-card-header {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.type-icon {
		font-size: 1.1rem;
	}

	.issue-id {
		font-size: 0.8rem;
		color: var(--text-muted, #a6adc8);
		font-variant-numeric: tabular-nums;
	}

	.issue-title {
		flex: 1;
		font-weight: 500;
	}

	.issue-card.is-child {
		margin-left: 0.75rem;
		border-left: 3px solid var(--accent-color, #89b4fa);
	}

	.parent-tag {
		font-size: 0.75rem;
		color: var(--accent-color, #89b4fa);
	}

	/* Children checklist in detail */
	.children-list {
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
	}

	.child-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.45rem 0.7rem;
		background: var(--surface, #313244);
		border-radius: 6px;
		font-size: 0.85rem;
	}

	.child-row.child-done {
		opacity: 0.75;
	}

	.child-check {
		flex-shrink: 0;
	}

	.child-link {
		flex: 1;
		text-align: left;
		background: none;
		border: none;
		color: var(--accent-color, #89b4fa);
		cursor: pointer;
		font-size: inherit;
		padding: 0;
		font-family: inherit;
	}

	.child-link:hover {
		text-decoration: underline;
	}

	.child-progress {
		font-weight: 400;
		color: var(--text-muted, #a6adc8);
		font-size: 0.8rem;
	}

	.children-hint {
		margin: 0.6rem 0 0;
		font-size: 0.78rem;
		color: var(--text-muted, #a6adc8);
	}

	.linkish {
		background: none;
		border: none;
		color: var(--accent-color, #89b4fa);
		cursor: pointer;
		font-size: inherit;
		padding: 0;
		font-family: inherit;
		text-decoration: underline;
	}

	.issue-card-footer {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		margin-top: 0.4rem;
		font-size: 0.8rem;
		color: var(--text-muted, #a6adc8);
	}

	.status-badge {
		padding: 2px 8px;
		border-radius: 12px;
		font-size: 0.75rem;
		font-weight: 500;
	}

	.status-badge.todo { background: #45475a; color: #cdd6f4; }
	.status-badge.in_progress { background: #1e3a5f; color: #89b4fa; }
	.status-badge.testing { background: #3f3f1e; color: #f9e2af; }
	.status-badge.failed { background: #5f1e1e; color: #f38ba8; }
	.status-badge.done { background: #1e5f2a; color: #a6e3a1; }

	.issue-type-label {
		font-size: 0.75rem;
		padding: 1px 6px;
		border-radius: 4px;
		background: var(--surface, #313244);
	}

	.task-count {
		color: var(--accent-color, #89b4fa);
	}

	/* Modal */
	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 1000;
		padding: 1rem;
	}

	.modal {
		background: var(--bg, #1e1e2e);
		border-radius: 12px;
		width: 100%;
		max-width: 600px;
		max-height: 85vh;
		display: flex;
		flex-direction: column;
		box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
	}

	.modal-lg { max-width: 700px; }
	.modal-xl { max-width: 900px; }

	.modal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 1rem 1.25rem;
		border-bottom: 1px solid var(--border-color, #444);
	}

	.modal-header h3 {
		margin: 0;
		font-size: 1.1rem;
	}

	.modal-body {
		padding: 1.25rem;
		overflow-y: auto;
		flex: 1;
	}

	.modal-footer {
		display: flex;
		justify-content: flex-end;
		gap: 0.5rem;
		padding: 0.75rem 1.25rem;
		border-top: 1px solid var(--border-color, #444);
	}

	.btn-close {
		background: none;
		border: none;
		font-size: 1.5rem;
		cursor: pointer;
		color: var(--text-muted, #a6adc8);
		line-height: 1;
		padding: 0 0.25rem;
	}

	.btn-close:hover {
		color: var(--text-color, #cdd6f4);
	}

	/* Form */
	.form-group {
		margin-bottom: 1rem;
	}

	.form-group label {
		display: block;
		margin-bottom: 0.3rem;
		font-size: 0.85rem;
		font-weight: 500;
		color: var(--text-muted, #a6adc8);
	}

	.form-group input,
	.form-group select,
	.form-group textarea {
		width: 100%;
		padding: 0.5rem 0.75rem;
		border: 1px solid var(--border-color, #444);
		border-radius: 6px;
		background: var(--input-bg, #181825);
		color: var(--text-color, #cdd6f4);
		font-size: 0.9rem;
		font-family: inherit;
		box-sizing: border-box;
	}

	.form-group textarea {
		resize: vertical;
	}

	.form-row {
		display: flex;
		gap: 1rem;
	}

	.form-row .form-group { flex: 1; }

	/* Detail */
	.detail-header-left {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		flex: 1;
		min-width: 0;
	}

	.detail-header-left h3 {
		margin: 0;
		font-size: 1rem;
		cursor: pointer;
	}

	.detail-header-right {
		display: flex;
		align-items: center;
		gap: 0.4rem;
		flex-shrink: 0;
	}

	.type-badge {
		font-size: 0.8rem;
		padding: 2px 8px;
		border-radius: 4px;
		background: var(--surface, #313244);
	}

	.detail-status-bar {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.5rem 0;
		margin-bottom: 1rem;
		font-size: 0.85rem;
		flex-wrap: wrap;
	}

	.status-label { color: var(--text-muted, #a6adc8); }

	.detail-meta {
		color: var(--text-muted, #a6adc8);
		font-size: 0.8rem;
	}

	.detail-section {
		margin-top: 1.25rem;
		padding-top: 1rem;
		border-top: 1px solid var(--border-color, #313244);
	}

	.detail-section h4 {
		margin: 0 0 0.5rem;
		font-size: 0.9rem;
		cursor: pointer;
		color: var(--text-muted, #a6adc8);
	}

	.detail-content {
		font-size: 0.9rem;
		line-height: 1.6;
		white-space: pre-wrap;
	}

	.goal-content {
		padding: 0.75rem;
		background: var(--surface, #313244);
		border-radius: 6px;
		border-left: 3px solid var(--accent-color, #89b4fa);
	}

	.edit-input,
	.edit-textarea {
		width: 100%;
		padding: 0.4rem 0.6rem;
		border: 1px solid var(--accent-color, #89b4fa);
		border-radius: 4px;
		background: var(--input-bg, #181825);
		color: var(--text-color, #cdd6f4);
		font-size: inherit;
		font-family: inherit;
		box-sizing: border-box;
	}

	.edit-textarea { resize: vertical; }

	.edit-actions {
		margin-top: 0.5rem;
		display: flex;
		gap: 0.4rem;
	}

	/* Linked Tasks */
	.linked-tasks {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}

	.linked-task {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.5rem 0.75rem;
		background: var(--surface, #313244);
		border-radius: 6px;
		font-size: 0.85rem;
	}

	.task-link {
		color: var(--accent-color, #89b4fa);
		text-decoration: none;
		font-weight: 500;
	}

	.task-link:hover { text-decoration: underline; }

	.task-status-badge {
		font-size: 0.7rem;
		padding: 1px 6px;
		border-radius: 4px;
		background: var(--input-bg, #181825);
	}

	.task-status-badge.completed { color: #a6e3a1; }
	.task-status-badge.failed { color: #f38ba8; }
	.task-status-badge.stopped { color: #fab387; }
	.task-status-badge.running { color: #89b4fa; }

	.task-summary {
		flex: 1;
		color: var(--text-muted, #a6adc8);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-date {
		color: var(--text-muted, #a6adc8);
		font-size: 0.75rem;
		flex-shrink: 0;
	}

	/* Pagination */
	.pagination-bar {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 1rem;
		padding: 0.75rem 0;
	}

	.page-info {
		font-size: 0.85rem;
		color: var(--text-muted, #a6adc8);
	}

	/* Empty & Loading */
	.empty-state {
		text-align: center;
		padding: 3rem 1rem;
		color: var(--text-muted, #a6adc8);
	}

	.loading {
		text-align: center;
		padding: 2rem;
		color: var(--text-muted, #a6adc8);
	}

	/* Buttons */
	.btn {
		padding: 0.4rem 0.8rem;
		border: 1px solid var(--border-color, #444);
		border-radius: 6px;
		background: var(--surface, #313244);
		color: var(--text-color, #cdd6f4);
		cursor: pointer;
		font-size: 0.85rem;
	}

	.btn:hover { border-color: var(--accent-color, #89b4fa); }

	.btn-primary {
		background: var(--accent-color, #89b4fa);
		color: #1e1e2e;
		border-color: var(--accent-color, #89b4fa);
		font-weight: 500;
	}

	.btn-primary:hover { opacity: 0.9; }

	.btn-success {
		background: #a6e3a1;
		color: #1e1e2e;
		border-color: #a6e3a1;
	}

	.btn-danger {
		background: #f38ba8;
		color: #1e1e2e;
		border-color: #f38ba8;
	}

	.btn-sm {
		padding: 0.25rem 0.6rem;
		font-size: 0.8rem;
	}

	.btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
