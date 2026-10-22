<script lang="ts">
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import MarkdownBody from '$lib/components/MarkdownBody.svelte';

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

	<!-- Issue Detail Modal -->
	{#if showingDetail && selectedIssue}
	<div class="modal-overlay" onclick={closeDetail}>
		<div class="modal modal-xl" onclick={(e) => e.stopPropagation()}>
			<div class="modal-header">
				<div class="detail-header-left">
					<span class="type-badge {selectedIssue.type}"><Icon name={typeIcons[selectedIssue.type] || 'file-text'} size={12} /> {typeLabels[selectedIssue.type] || selectedIssue.type}</span>
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
						<button class="btn btn-sm btn-success" onclick={() => updateIssueStatus(selectedIssue.id, 'done')}><Icon name="check" size={13} stroke={2.4} /> 성공</button>
						<button class="btn btn-sm btn-danger" onclick={() => updateIssueStatus(selectedIssue.id, 'failed')}><Icon name="x" size={13} stroke={2.4} /> 실패</button>
					{/if}
					{#if (selectedIssue.status === 'todo' || selectedIssue.status === 'testing' || selectedIssue.status === 'failed')}
						<button class="btn btn-sm btn-primary" onclick={() => executeIssue(selectedIssue.id)}><Icon name="play" size={11} stroke={2.4} /> 수행</button>
					{/if}
					<button class="btn btn-sm btn-ghost btn-icon btn-del" onclick={() => deleteIssue(selectedIssue.id)} title="삭제" aria-label="이슈 삭제"><Icon name="trash" size={14} /></button>
					<button class="btn btn-sm btn-ghost btn-icon" onclick={closeDetail} aria-label="닫기"><Icon name="x" size={15} /></button>
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
						{#if selectedIssue.body}
							<div class="detail-markdown"><MarkdownBody text={selectedIssue.body} /></div>
						{:else}
							<div class="detail-content empty">내용이 없습니다.</div>
						{/if}
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
								<Icon name={child.status === 'done' ? 'check-circle' : 'circle'} size={14} />
							</span>
							<button class="child-link" onclick={() => loadIssueDetail(child.id)}>
								#{child.id} {child.title}
							</button>
							<span class="status-badge {child.status}">{statusLabels[child.status] || child.status}</span>
						</div>
						{/each}
					</div>
					<p class="children-hint">수행하면 미완료 하위 이슈가 먼저 큐에 들어가고, 이 이슈가 마지막에 들어갑니다.</p>
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
				onclick={() => loadIssueDetail(issue.id)}
				onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); loadIssueDetail(issue.id); } }}
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
	.status-badge.in_progress::before { animation: pulse-ring 1.8s var(--ease-out) infinite; }
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

	/* Children checklist in detail */
	.children-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.child-row {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 8px 12px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius);
		background: var(--bg-3);
		font-size: var(--fs-sm);
	}

	.child-row.child-done {
		opacity: 0.7;
	}

	.child-check {
		display: inline-flex;
		flex-shrink: 0;
		color: var(--text-tertiary);
	}

	.child-done .child-check {
		color: var(--success);
	}

	.child-link {
		flex: 1;
		min-width: 0;
		padding: 0;
		border: none;
		background: none;
		color: var(--text-primary);
		font-family: inherit;
		font-size: inherit;
		text-align: left;
		cursor: pointer;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.child-link:hover {
		color: var(--accent);
	}

	.child-progress {
		font-family: var(--font-mono);
		font-weight: 400;
		color: var(--text-tertiary);
		font-size: var(--fs-2xs);
		letter-spacing: 0;
		text-transform: none;
	}

	.children-hint {
		margin: 10px 0 0;
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
	}

	.linkish {
		padding: 0;
		border: none;
		background: none;
		color: var(--accent);
		font-family: inherit;
		font-size: inherit;
		cursor: pointer;
		text-decoration: underline;
		text-decoration-thickness: 1px;
		text-underline-offset: 3px;
	}

	/* Modal — sits on the shared overlay recipe (app.css .modal-overlay) */
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
	.modal-xl { max-width: 900px; }

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

	/* Detail */
	.detail-header-left {
		display: flex;
		align-items: center;
		gap: 12px;
		flex: 1;
		min-width: 0;
	}

	.detail-header-left h3 {
		margin: 0;
		font-size: var(--fs-md);
		cursor: text;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.detail-header-left h3:hover {
		color: var(--accent);
	}

	.detail-header-right {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
	}

	.type-badge {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		flex-shrink: 0;
		padding: 1px 9px;
		border-radius: var(--radius-full);
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		line-height: 1.7;
		background: rgba(237, 230, 218, 0.05);
		color: var(--text-secondary);
	}

	.type-badge.feature { background: var(--accent-soft); color: var(--accent); }
	.type-badge.bug { background: var(--danger-soft); color: var(--danger); }
	.type-badge.design { background: var(--queued-soft); color: var(--queued); }

	.detail-status-bar {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 8px 14px;
		padding-bottom: 4px;
		font-size: var(--fs-xs);
	}

	.status-label { color: var(--text-tertiary); }

	.detail-meta {
		font-family: var(--font-mono);
		color: var(--text-tertiary);
		font-size: var(--fs-2xs);
	}

	.detail-section {
		margin-top: 20px;
		padding-top: 16px;
		border-top: 1px solid var(--border-subtle);
	}

	.detail-section h4 {
		margin: 0 0 10px;
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: var(--fw-medium);
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.detail-section h4:hover {
		color: var(--text-secondary);
	}

	.detail-markdown {
		font-size: var(--fs-sm);
	}

	.detail-content.empty {
		color: var(--text-tertiary);
	}

	.detail-meta .linkish {
		font-family: var(--font-sans);
		font-size: var(--fs-xs);
	}

	.detail-content {
		font-size: var(--fs-sm);
		line-height: 1.65;
		color: var(--text-primary);
		white-space: pre-wrap;
	}

	.goal-content {
		padding: 12px 14px;
		border-radius: var(--radius);
		background: var(--accent-soft);
		box-shadow: inset 2px 0 0 var(--accent);
	}

	.edit-input,
	.edit-textarea {
		width: 100%;
		padding: 6px 10px;
		border-color: rgba(244, 184, 96, 0.6);
		font-size: inherit;
		font-family: inherit;
	}

	.edit-textarea { resize: vertical; }

	.edit-actions {
		margin-top: 8px;
		display: flex;
		gap: 6px;
	}

	/* Linked Tasks */
	.linked-tasks {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.linked-task {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 8px 12px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius);
		background: var(--bg-3);
		font-size: var(--fs-sm);
	}

	.task-link {
		flex-shrink: 0;
		font-weight: var(--fw-medium);
	}

	.task-status-badge {
		flex-shrink: 0;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-secondary);
	}

	.task-status-badge.completed { color: var(--success); }
	.task-status-badge.failed { color: var(--danger); }
	.task-status-badge.stopped { color: var(--wool); }
	.task-status-badge.running { color: var(--live); }

	.task-summary {
		flex: 1;
		min-width: 0;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-date {
		flex-shrink: 0;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	/* Pagination */
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

	/* Local button variants (global .btn covers the rest) */
	.btn-success {
		background: var(--success-soft);
		border-color: var(--success-line);
		color: var(--success);
		box-shadow: none;
	}

	.btn-success:hover {
		background: rgba(159, 214, 133, 0.22);
		border-color: var(--success);
		color: var(--success);
	}

	.btn-del:hover {
		color: var(--danger);
		background: var(--danger-soft);
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
