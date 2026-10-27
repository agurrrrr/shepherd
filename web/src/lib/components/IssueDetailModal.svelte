<script lang="ts">
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import Icon from '$lib/components/Icon.svelte';
	import MarkdownBody from '$lib/components/MarkdownBody.svelte';

	// Issue detail modal shared by the project Issues tab and the Shepherd
	// board (#599). The project screen (IssueTab) used to own this markup; it
	// now renders this component so both screens get the shepherd controls:
	//   - 감시 토글   → PATCH {shepherd_watch}
	//   - 회차 초기화 → PATCH {shepherd_rounds: 0}
	//   - 실행        → POST execute, optionally {watch: true} ("감시를 켜고 실행")
	//   - 프로젝트 링크
	// The modal fetches the detail itself from (projectName, issueId), so a
	// caller only passes ids — board cards carry just list fields. `issueId`
	// may change (parent/child links); the modal refetches on every change.

	let {
		projectName,
		issueId,
		onClose = () => {},
		onChanged = () => {}
	} = $props();

	let issue = $state(null);
	let loadError = $state('');
	let busy = $state(false);

	// Inline edit state ('title' | 'body' | 'goal' | null).
	let editingField = $state(null);
	let editValue = $state('');

	// Execute option: enqueue with shepherd_watch turned on for the cascade.
	let executeWithWatch = $state(false);

	const statusLabels = {
		todo: '작업전',
		in_progress: '작업중',
		testing: '테스트',
		failed: '실패',
		done: '성공'
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

	let seq = 0;

	// Parent/child links inside the modal switch the target issue without
	// unmounting. Props are read-only in runes mode, so the active id lives in
	// local state; a parent-side change (different card) wins via the effect.
	let currentId = $state(0);
	$effect(() => {
		if (issueId != null && issueId !== currentId) currentId = issueId;
	});

	async function load() {
		const mySeq = ++seq;
		loadError = '';
		try {
			const res = await apiGet(
				`/api/projects/${encodeURIComponent(projectName)}/issues/${currentId}`
			);
			if (mySeq !== seq) return; // stale response — a newer load won
			if (res?.success && res.data) {
				issue = res.data;
			} else {
				loadError = res?.message || '이슈 상세를 불러오지 못했습니다.';
			}
		} catch (e) {
			if (mySeq === seq) loadError = '이슈 상세를 불러오지 못했습니다.';
		}
	}

	$effect(() => {
		// Re-fetch whenever the target issue changes (parent/child links).
		if (projectName && currentId) load();
	});

	async function patchIssue(payload, reload = true) {
		busy = true;
		try {
			const res = await apiPatch(
				`/api/projects/${encodeURIComponent(projectName)}/issues/${issue.id}`,
				payload
			);
			if (!res?.success) {
				loadError = res?.message || '이슈를 수정하지 못했습니다.';
				return;
			}
			loadError = '';
			if (reload) await load();
			onChanged();
		} catch (e) {
			loadError = '이슈를 수정하지 못했습니다: ' + (e?.message || e);
		} finally {
			busy = false;
		}
	}

	async function toggleWatch() {
		if (!issue) return;
		await patchIssue({ shepherd_watch: !issue.shepherd_watch });
	}

	async function resetRounds() {
		if (!issue) return;
		if (!confirm('검토 회차를 0으로 초기화할까요? 목자가 이 이슈를 다시 검토할 수 있습니다.')) return;
		await patchIssue({ shepherd_rounds: 0 });
	}

	async function updateIssueStatus(newStatus) {
		if (!issue) return;
		await patchIssue({ status: newStatus });
	}

	async function executeIssue() {
		if (!issue) return;
		const hasChildren = issue.children?.length > 0;
		const incomplete = hasChildren ? issue.children.filter((c) => c.status !== 'done').length : 0;
		let msg = incomplete > 0
			? `미완료 하위 이슈 ${incomplete}개를 먼저 큐에 넣고, 마지막에 이 이슈를 수행합니다. 계속할까요?`
			: '이 이슈를 수행하시겠습니까?';
		if (executeWithWatch) msg += '\n감시(목자 검토)를 켠 상태로 실행합니다.';
		if (!confirm(msg)) return;
		busy = true;
		try {
			const res = await apiPost(
				`/api/projects/${encodeURIComponent(projectName)}/issues/${issue.id}/execute`,
				{ watch: executeWithWatch }
			);
			if (res?.data?.task_ids?.length > 1) {
				alert(
					`작업 ${res.data.task_ids.length}개가 큐에 추가되었습니다.\n이슈: ${JSON.stringify(res.data.issue_ids)}\nTask: ${JSON.stringify(res.data.task_ids)}`
				);
			} else if (!res?.success) {
				loadError = res?.message || '수행 요청 실패';
			}
			await load();
			onChanged();
		} catch (e) {
			loadError = '수행 요청 실패: ' + (e?.message || e);
		} finally {
			busy = false;
		}
	}

	async function deleteIssue() {
		if (!issue) return;
		if (!confirm('이 이슈를 삭제하시겠습니까? 연결된 작업(Task)은 보존됩니다.')) return;
		busy = true;
		try {
			await apiDelete(`/api/projects/${encodeURIComponent(projectName)}/issues/${issue.id}`);
			onChanged();
			onClose();
		} catch (e) {
			loadError = '삭제 실패: ' + (e?.message || e);
		} finally {
			busy = false;
		}
	}

	function startEdit(field) {
		if (!issue) return;
		editingField = field;
		editValue = issue[field] || '';
	}

	async function saveEdit() {
		if (!issue || !editingField) return;
		await patchIssue({ [editingField]: editValue });
		editingField = null;
		editValue = '';
	}

	function cancelEdit() {
		editingField = null;
		editValue = '';
	}

	function childDoneCount(target) {
		const kids = target?.children || [];
		if (!kids.length) return null;
		const done = kids.filter((c) => c.status === 'done').length;
		return { done, total: kids.length };
	}

	function openChild(childId) {
		// Parent/child links point at the same project — refetch this modal.
		currentId = childId;
		load();
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div class="modal-overlay" onclick={onClose}>
	<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
	<div class="modal modal-xl" onclick={(e) => e.stopPropagation()}>
		{#if loadError}
			<div class="modal-header">
				<div class="detail-header-left"><h3>이슈 상세</h3></div>
				<div class="detail-header-right">
					<button class="btn btn-sm btn-ghost btn-icon" onclick={onClose} aria-label="닫기"><Icon name="x" size={15} /></button>
				</div>
			</div>
			<div class="modal-body">
				<div class="error-banner" role="alert"><Icon name="alert-triangle" size={15} /> {loadError}</div>
				<button class="btn btn-sm" onclick={load}>다시 시도</button>
			</div>
		{:else if !issue}
			<div class="modal-body" aria-busy="true">
				<div class="skeleton" style="height: 16px; width: 40%"></div>
				<div class="skeleton" style="height: 80px; margin-top: 16px"></div>
			</div>
		{:else}
			<div class="modal-header">
				<div class="detail-header-left">
					<span class="type-badge {issue.type}"><Icon name={typeIcons[issue.type] || 'file-text'} size={12} /> {typeLabels[issue.type] || issue.type}</span>
					{#if editingField === 'title'}
						<input type="text" class="edit-input" bind:value={editValue} onkeydown={(e) => e.key === 'Enter' && saveEdit()} />
					{:else}
						<h3 onclick={() => startEdit('title')} title="클릭하여 수정">{issue.title}</h3>
					{/if}
				</div>
				<div class="detail-header-right">
					{#if issue.status === 'testing'}
						<button class="btn btn-sm btn-success" onclick={() => updateIssueStatus('done')}><Icon name="check" size={13} stroke={2.4} /> 성공</button>
						<button class="btn btn-sm btn-danger" onclick={() => updateIssueStatus('failed')}><Icon name="x" size={13} stroke={2.4} /> 실패</button>
					{/if}
					{#if issue.status === 'todo' || issue.status === 'testing' || issue.status === 'failed'}
						<button class="btn btn-sm btn-primary" onclick={executeIssue}><Icon name="play" size={11} stroke={2.4} /> 수행</button>
					{/if}
					<button class="btn btn-sm btn-ghost btn-icon btn-del" onclick={deleteIssue} title="삭제" aria-label="이슈 삭제"><Icon name="trash" size={14} /></button>
					<button class="btn btn-sm btn-ghost btn-icon" onclick={onClose} aria-label="닫기"><Icon name="x" size={15} /></button>
				</div>
			</div>
			<div class="modal-body detail-body">
				<div class="detail-status-bar">
					<span class="status-label">상태:</span>
					<span class="status-badge {issue.status}">{statusLabels[issue.status] || issue.status}</span>
					{#if issue.parent_id}
						<span class="detail-meta">
							상위:
							<button class="linkish" onclick={() => openChild(issue.parent_id)}>
								#{issue.parent_id}{issue.parent ? ` ${issue.parent.title}` : ''}
							</button>
						</span>
					{/if}
					{#if issue.started_at}
						<span class="detail-meta">시작: {issue.started_at}</span>
					{/if}
					{#if issue.completed_at}
						<span class="detail-meta">마감: {issue.completed_at}</span>
					{/if}
					<span class="detail-meta">생성: {issue.created_at}</span>
				</div>

				<!-- Shepherd (목자) controls (#599) -->
				<div class="shepherd-bar">
					<button
						class="watch-toggle"
						class:on={issue.shepherd_watch}
						onclick={toggleWatch}
						disabled={busy}
						title="목자가 이 이슈의 작업을 검토하도록 감시합니다"
					>
						<Icon name={issue.shepherd_watch ? 'eye' : 'eye-off'} size={14} />
						{issue.shepherd_watch ? '목자 감시 중' : '감시 꺼짐'}
					</button>
					{#if issue.shepherd_watch}
						<span class="rounds mono num" title="검토 회차">회차 {issue.shepherd_rounds ?? 0}</span>
						{#if (issue.shepherd_rounds ?? 0) > 0}
							<button class="btn btn-sm btn-ghost" onclick={resetRounds} disabled={busy}>
								<Icon name="rotate-ccw" size={12} /> 회차 초기화
							</button>
						{/if}
					{/if}
					{#if issue.shepherd_last_review}
						<span class="last-review" title="목자의 마지막 검토">{issue.shepherd_last_review}</span>
					{/if}
					<span class="shepherd-bar-spacer"></span>
					<label class="watch-exec">
						<input type="checkbox" bind:checked={executeWithWatch} /> 감시를 켜고 실행
					</label>
					<a class="btn btn-sm btn-ghost" href="/projects/{encodeURIComponent(projectName)}?tab=issues">
						<Icon name="folder" size={13} /> {projectName}
					</a>
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
						{#if issue.body}
							<div class="detail-markdown"><MarkdownBody text={issue.body} /></div>
						{:else}
							<div class="detail-content empty">내용이 없습니다.</div>
						{/if}
					{/if}
				</div>

				<!-- Children checklist (auto, live status — not stored in body) -->
				{#if issue.children && issue.children.length > 0}
				<div class="detail-section">
					<h4>
						하위 이슈
						{#if childDoneCount(issue)}
							<span class="child-progress">
								({childDoneCount(issue).done}/{childDoneCount(issue).total} 완료)
							</span>
						{/if}
					</h4>
					<div class="children-list">
						{#each issue.children as child}
						<div class="child-row" class:child-done={child.status === 'done'}>
							<span class="child-check" aria-hidden="true">
								<Icon name={child.status === 'done' ? 'check-circle' : 'circle'} size={14} />
							</span>
							<button class="child-link" onclick={() => openChild(child.id)}>
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
						<div class="detail-content goal-content">{issue.goal || '목표가 설정되지 않았습니다.'}</div>
					{/if}
				</div>

				<!-- Linked Tasks -->
				{#if issue.tasks && issue.tasks.length > 0}
				<div class="detail-section">
					<h4>연결된 작업 ({issue.tasks.length})</h4>
					<div class="linked-tasks">
						{#each issue.tasks as task}
						<div class="linked-task">
							<a href="/tasks/{task.id}" class="task-link">작업 #{task.id}</a>
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
				<button class="btn" onclick={onClose}>닫기</button>
			</div>
		{/if}
	</div>
</div>

<style>
	/* Modal — copied from IssueTab.svelte (styles are component-scoped in
	   Svelte, so the shared board needs its own copy of the recipe). */
	.modal-overlay {
		position: fixed;
		inset: 0;
		z-index: 1000;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 24px;
		background: var(--bg-overlay);
		animation: fade-in var(--dur-1) steps(2) backwards;
	}

	.modal {
		display: flex;
		flex-direction: column;
		width: 100%;
		max-width: 600px;
		max-height: min(86vh, 900px);
		border: 0;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel), var(--shadow-3);
		animation: modal-in var(--dur-2) steps(3) backwards;
	}

	.modal-xl { max-width: 900px; }

	.modal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		padding: 16px 20px;
		border-bottom: 3px solid var(--lo);
	}

	.modal-header h3 {
		margin: 0;
		color: var(--highlight);
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

	.detail-header-left h3:hover { color: var(--accent); }

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
		border-radius: 0;
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		line-height: 1.7;
		background: var(--well);
		color: var(--text-secondary);
	}

	.type-badge.feature { background: var(--accent-soft); color: var(--accent); }
	.type-badge.bug { background: var(--danger-soft); color: var(--danger-text); }
	.type-badge.design { background: var(--queued-soft); color: var(--queued-text); }

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

	.status-badge {
		flex-shrink: 0;
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 1px 9px;
		border: 1px solid transparent;
		border-radius: 0;
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		line-height: 1.7;
		white-space: nowrap;
	}

	.status-badge::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 0;
		background: currentColor;
	}

	.status-badge.todo { background: var(--well); color: var(--text-secondary); border-color: var(--border-subtle); }
	.status-badge.todo::before { background: transparent; box-shadow: inset 0 0 0 1.5px currentColor; }
	.status-badge.in_progress { background: var(--live-soft); color: var(--live); border-color: var(--live-line); }
	.status-badge.in_progress::before { animation: px-blink 1s steps(1) infinite; }
	.status-badge.testing { background: var(--queued-soft); color: var(--queued-text); border-color: var(--queued-line); }
	.status-badge.failed { background: var(--danger-soft); color: var(--danger-text); border-color: var(--danger-line); }
	.status-badge.done { background: var(--success-soft); color: var(--success); border-color: var(--success-line); }

	/* Shepherd (목자) control row (#599) */
	.shepherd-bar {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 8px 12px;
		margin-top: 14px;
		padding: 10px 12px;
		border: 1px solid var(--border-subtle);
		border-radius: 0;
		background: var(--bg-3);
		font-size: var(--fs-xs);
	}

	.watch-toggle {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px;
		border: 0;
		box-shadow: inset 0 0 0 2px #000;
		border-radius: 0;
		background: var(--well);
		color: var(--text-secondary);
		font-family: inherit;
		font-size: var(--fs-xs);
		cursor: pointer;
	}

	.watch-toggle:hover { background: var(--bg-3); color: var(--text-primary); }

	.watch-toggle.on {
		background: var(--accent);
		box-shadow: var(--px-ring-2);
		color: #000;
	}

	.rounds {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.last-review {
		min-width: 0;
		max-width: 42ch;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.shepherd-bar-spacer { flex: 1; }

	.watch-exec {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		color: var(--text-secondary);
		cursor: pointer;
		white-space: nowrap;
	}

	.detail-section {
		margin-top: 20px;
		padding-top: 16px;
		border-top: 1px solid var(--border-subtle);
	}

	.detail-section h4 {
		margin: 0 0 10px;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.detail-section h4:hover { color: var(--text-secondary); }

	.detail-markdown { font-size: var(--fs-sm); }

	.detail-content.empty { color: var(--text-tertiary); }

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
		border-radius: 0;
		background: var(--accent-soft);
		box-shadow: inset 2px 0 0 var(--accent);
	}

	.edit-input,
	.edit-textarea {
		width: 100%;
		padding: 6px 10px;
		border-color: var(--accent);
		font-size: inherit;
		font-family: inherit;
	}

	.edit-textarea { resize: vertical; }

	.edit-actions {
		margin-top: 8px;
		display: flex;
		gap: 6px;
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
		border-radius: 0;
		background: var(--bg-3);
		font-size: var(--fs-sm);
	}

	.child-row.child-done { opacity: 0.7; }

	.child-check {
		display: inline-flex;
		flex-shrink: 0;
		color: var(--text-tertiary);
	}

	.child-done .child-check { color: var(--success); }

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

	.child-link:hover { color: var(--accent); }

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
		border-radius: 0;
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
	.task-status-badge.failed { color: var(--danger-text); }
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

	.error-banner {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		padding: 8px 12px;
		margin-bottom: 12px;
		border: 1px solid var(--danger-line);
		border-radius: 0;
		background: var(--danger-soft);
		color: var(--danger-text);
		font-size: var(--fs-sm);
	}

	.btn-success {
		background: var(--success);
		color: #000;
	}

	.btn-success:hover {
		background: var(--success-hover);
		color: #000;
	}

	.btn-del:hover {
		color: var(--danger-text);
		background: var(--danger-soft);
	}
</style>
