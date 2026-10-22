<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiDownload } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { appendLiveOutput } from '$lib/liveOutput.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import MagiStreamPanel from '$lib/components/MagiStreamPanel.svelte';
	import SubagentStreamPanel from '$lib/components/SubagentStreamPanel.svelte';
	import MarkdownBody from '$lib/components/MarkdownBody.svelte';
	import DiffView from '$lib/components/DiffView.svelte';

	let task = $state(null);
	let loading = $state(true);
	let liveOutput = $state([]);
	let liveOutputOpen = $state(false);
	let unsubs = [];

	let cancelling = $state(false);
	let cancelError = $state('');

	// Task changes (checkpoint diff)
	const MAX_VISIBLE_FILES = 200;
	let changes = $state(null);
	let changesLoading = $state(false);
	let changesError = $state('');
	let changesLive = $state(false);
	let showAllFiles = $state(false);
	let expandedFile = $state('');
	let fileDiff = $state(null);
	let fileDiffLoading = $state(false);
	let fileDiffError = $state('');

	// Revert / undo
	let revertOpen = $state(false);
	let revertLoading = $state(false);
	let revertPreview = $state(null);
	let revertConflicts = $state([]);
	let revertError = $state('');
	let reverting = $state(false);
	let undoLoading = $state(false);
	let actionError = $state('');

	let taskId = $derived.by(() => $page.params.id);
	let canRevert = $derived(
		task &&
			['completed', 'failed', 'stopped'].includes(task.status) &&
			changes?.available &&
			!task.reverted_at
	);
	let isReverted = $derived(!!task?.reverted_at);
	let from = $derived.by(() => $page.url.searchParams.get('from'));
	let backFromProject = $derived(from === 'project' && task?.project);
	let backLink = $derived(backFromProject
		? `/projects/${encodeURIComponent(task.project)}?tab=history`
		: '/tasks');
	let backLabel = $derived(backFromProject ? `← Back to ${task.project}` : '← Back to Tasks');

	onMount(async () => {
		const res = await apiGet(`/api/tasks/${taskId}`);
		if (res?.data) {
			task = res.data;
			liveOutput = task.output || [];
			liveOutputOpen = false;
			await loadChanges(false);
		}
		loading = false;

		// SSE for live output when task is running. The server-side
		// LineCoalescer already emits complete lines (task #7209), so
		// appendLiveOutput is now a thin wrapper that pushes lines.
		unsubs.push(onSSE('output', (data) => {
			if (task && task.status === 'running' && task.sheep === data.sheep_name) {
				const state = { open: liveOutputOpen };
				liveOutput = appendLiveOutput(liveOutput, data.text, state, 5000);
				liveOutputOpen = state.open;
			}
		}));

		unsubs.push(onSSE('task_complete', (data) => {
			if (task && data.task_id === task.id) {
				task = { ...task, status: 'completed', summary: data.summary };
				loadChanges(false);
			}
		}));

		unsubs.push(onSSE('task_fail', (data) => {
			if (task && data.task_id === task.id) {
				task = { ...task, status: 'failed', error: data.error };
			}
		}));

		unsubs.push(onSSE('task_stop', (data) => {
			if (task && data.task_id === task.id) {
				task = { ...task, status: 'stopped', error: data.reason };
			}
		}));

		unsubs.push(onSSE('task_update', (data) => {
			if (task && data.task_id === task.id) {
				task = { ...task, reverted_at: data.reverted_at || '' };
				loadChanges(false);
			}
		}));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	const reasonLabels = {
		'checkpoint disabled': '체크포인트가 비활성화되어 변경 사항을 볼 수 없습니다.',
		'not a git repository': 'git 저장소가 아니라서 변경 사항을 볼 수 없습니다.',
		'too large': '변경 사항이 너무 커서 스냅샷을 만들지 못했습니다.',
		'no end snapshot': '작업 종료 스냅샷이 없어 변경 사항을 볼 수 없습니다.',
		'snapshot pruned': '스냅샷이 정리되어 변경 사항을 볼 수 없습니다.',
		running: '작업이 실행 중입니다. "현재까지 변경 보기"를 눌러 지금까지의 변경을 확인하세요.',
	};

	function reasonText(reason) {
		return reasonLabels[reason] || reason || '변경 사항을 볼 수 없습니다.';
	}

	async function loadChanges(live = false) {
		if (!taskId) return;
		changesLoading = true;
		changesError = '';
		try {
			const res = await apiGet(`/api/tasks/${taskId}/changes${live ? '?live=1' : ''}`);
			if (res?.data) {
				changes = res.data;
				changesLive = !!res.data.live;
				if (!live) showAllFiles = false;
			} else {
				changesError = res?.message || 'Failed to load changes';
			}
		} catch (err) {
			changesError = err?.message || 'Failed to load changes';
		} finally {
			changesLoading = false;
		}
	}

	async function toggleFileDiff(file) {
		if (expandedFile === file.path) {
			expandedFile = '';
			fileDiff = null;
			fileDiffError = '';
			return;
		}
		expandedFile = file.path;
		fileDiff = null;
		fileDiffError = '';
		fileDiffLoading = true;
		try {
			const live = changesLive ? '&live=1' : '';
			const res = await apiGet(
				`/api/tasks/${taskId}/changes/diff?file=${encodeURIComponent(file.path)}${live}`
			);
			if (res?.data) {
				fileDiff = res.data;
			} else {
				fileDiffError = res?.message || 'Failed to load diff';
			}
		} catch (err) {
			fileDiffError = err?.message || 'Failed to load diff';
		} finally {
			fileDiffLoading = false;
		}
	}

	async function downloadPatch() {
		if (!changes?.available) return;
		const blob = await apiDownload(
			`/api/tasks/${taskId}/changes/patch${changesLive ? '?live=1' : ''}`
		);
		if (!blob) return;
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = `task-${taskId}.patch`;
		document.body.appendChild(a);
		a.click();
		a.remove();
		URL.revokeObjectURL(url);
	}

	async function refreshTask() {
		const res = await apiGet(`/api/tasks/${taskId}`);
		if (res?.data) task = res.data;
	}

	async function openRevertModal() {
		if (revertLoading || reverting) return;
		revertOpen = true;
		revertLoading = true;
		revertPreview = null;
		revertConflicts = [];
		revertError = '';
		try {
			const res = await apiPost(`/api/tasks/${taskId}/revert`, { dry_run: true });
			if (res?.success) {
				revertPreview = res.data;
			} else {
				revertError = res?.message || '되돌리기 미리보기를 불러오지 못했습니다.';
				revertConflicts = res?.conflicts || [];
			}
		} catch (err) {
			revertError = err?.message || '되돌리기 미리보기를 불러오지 못했습니다.';
		} finally {
			revertLoading = false;
		}
	}

	function closeRevertModal() {
		if (reverting) return;
		revertOpen = false;
		revertPreview = null;
		revertConflicts = [];
		revertError = '';
	}

	async function confirmRevert() {
		if (reverting || !revertPreview?.changed) return;
		reverting = true;
		revertError = '';
		try {
			const res = await apiPost(`/api/tasks/${taskId}/revert`, { dry_run: false });
			if (res?.success) {
				revertOpen = false;
				await refreshTask();
				await loadChanges(false);
			} else {
				revertError = res?.message || '되돌리지 못했습니다.';
				revertConflicts = res?.conflicts || [];
			}
		} catch (err) {
			revertError = err?.message || '되돌리지 못했습니다.';
		} finally {
			reverting = false;
		}
	}

	async function undoRevert() {
		if (undoLoading) return;
		if (!confirm('되돌리기 취소를 실행할까요? 되돌린 뒤 생긴 다른 변경은 보존됩니다.')) return;
		undoLoading = true;
		actionError = '';
		try {
			const res = await apiPost(`/api/tasks/${taskId}/revert/undo`, {});
			if (res?.success) {
				await refreshTask();
				await loadChanges(false);
			} else {
				actionError = res?.message || '되돌리기 취소에 실패했습니다.';
			}
		} catch (err) {
			actionError = err?.message || '되돌리기 취소에 실패했습니다.';
		} finally {
			undoLoading = false;
		}
	}

	function displayPath(file) {
		if (file.status === 'renamed' && file.old_path) {
			return `${file.old_path} → ${file.path}`;
		}
		return file.path;
	}

	let visibleFiles = $derived.by(() => {
		const files = changes?.files || [];
		return showAllFiles ? files : files.slice(0, MAX_VISIBLE_FILES);
	});

	function formatDuration(start, end) {
		if (!start || !end) return '-';
		const ms = new Date(end) - new Date(start);
		const sec = Math.floor(ms / 1000);
		const min = Math.floor(sec / 60);
		if (min > 0) return `${min}m ${sec % 60}s`;
		return `${sec}s`;
	}

	async function cancelPending() {
		if (cancelling || !task || task.status !== 'pending') return;
		if (!confirm(`Cancel pending task #${task.id}?`)) return;
		cancelling = true;
		cancelError = '';
		try {
			const res = await apiPost(`/api/tasks/${task.id}/cancel`, {});
			if (res?.success) {
				task = { ...task, status: 'stopped', error: 'cancelled by user' };
			} else {
				cancelError = res?.message || 'Failed to cancel task';
			}
		} catch (err) {
			cancelError = err?.message || 'Failed to cancel task';
		} finally {
			cancelling = false;
		}
	}

	let providerIsMagi = $derived(task?.provider === 'magi');

	// Detect subagent output: any line starting with [SUB: triggers the panel.
	// Same routing pattern as MAGI ([MAGI: prefix → MagiStreamPanel).
	let hasSubagentOutput = $derived(
		liveOutput.some((line) => /^\[SUB:/.test(line ?? ''))
	);
</script>

<div class="page">
	<div class="page-nav">
		<a href={backLink}>{backLabel}</a>
	</div>

	{#if loading}
		<p class="text-muted">Loading...</p>
	{:else if !task}
		<p class="text-muted">Task not found</p>
	{:else}
		<div class="task-detail">
			<div class="detail-header">
				<h1>Task #{task.id}</h1>
				<StatusBadge status={task.status} />
				{#if task.reverted_at}
					<span class="reverted-badge">되돌림 ({task.reverted_at})</span>
				{/if}
				{#if task.status === 'pending'}
					<button class="btn btn-danger cancel-pending" onclick={cancelPending} disabled={cancelling}>
						{cancelling ? '...' : 'Cancel'}
					</button>
				{/if}
			</div>
			{#if cancelError}
				<p class="cancel-error">{cancelError}</p>
			{/if}

			<div class="meta-grid card">
				<div class="meta-item">
					<span class="meta-label">Project</span>
					<span>{task.project || '-'}</span>
				</div>
				<div class="meta-item">
					<span class="meta-label">Sheep</span>
					<span>{task.sheep || '-'}</span>
				</div>
				<div class="meta-item">
					<span class="meta-label">Provider</span>
					<span>{task.provider || '-'}</span>
				</div>
				<div class="meta-item">
					<span class="meta-label">Created</span>
					<span>{task.created_at || '-'}</span>
				</div>
				{#if task.started_at}
					<div class="meta-item">
						<span class="meta-label">Started</span>
						<span>{task.started_at}</span>
					</div>
				{/if}
				{#if task.completed_at}
					<div class="meta-item">
						<span class="meta-label">Completed</span>
						<span>{task.completed_at}</span>
					</div>
					<div class="meta-item">
						<span class="meta-label">Duration</span>
						<span>{formatDuration(task.started_at, task.completed_at)}</span>
					</div>
				{/if}
				{#if task.total_tokens > 0}
					<div class="meta-item">
						<span class="meta-label">Tokens</span>
						<span>{task.total_tokens.toLocaleString()} <span class="meta-sub">({(task.prompt_tokens ?? 0).toLocaleString()} in / {(task.completion_tokens ?? 0).toLocaleString()} out)</span></span>
					</div>
				{/if}
				{#if task.cost_usd > 0}
					<div class="meta-item">
						<span class="meta-label">Cost</span>
						<span>${task.cost_usd.toFixed(4)}</span>
					</div>
				{/if}
			</div>

			<div class="section card">
				<h3>Prompt</h3>
				<pre class="mono prompt-text">{task.prompt}</pre>
			</div>

			{#if task.error}
				<div class="section card error-section">
					<h3>Error</h3>
					<pre class="mono">{task.error}</pre>
				</div>
			{/if}

			{#if task.summary}
				<div class="section card">
					<h3>Summary</h3>
					<MarkdownBody text={task.summary} />
				</div>
			{/if}

			<div class="section card changes-card">
				<div class="changes-header">
					<h3>
						변경 사항
						{#if changes?.available}
							<span class="changes-summary">{changes.files.length} files · +{changes.additions} −{changes.deletions}</span>
						{/if}
					</h3>
					<div class="changes-actions">
						{#if changes?.commits_during_task?.length}
							<span class="commit-badge">커밋 {changes.commits_during_task.length}개 포함</span>
						{/if}
						{#if changes?.available}
							<button type="button" class="link-btn" onclick={downloadPatch}>패치 내려받기</button>
						{/if}
						{#if task.status === 'running'}
							<button
								type="button"
								class="btn btn-sm live-btn"
								onclick={() => loadChanges(true)}
								disabled={changesLoading}
							>
								{changesLive ? '현재까지 변경 다시 보기' : '현재까지 변경 보기'}
							</button>
						{/if}
						{#if canRevert}
							<button type="button" class="btn btn-sm revert-btn" onclick={openRevertModal}>
								이 작업 되돌리기
							</button>
						{/if}
						{#if isReverted}
							<button
								type="button"
								class="btn btn-sm"
								onclick={undoRevert}
								disabled={undoLoading}
							>
								{undoLoading ? '...' : '되돌리기 취소'}
							</button>
						{/if}
					</div>
				</div>
				{#if actionError}
					<p class="changes-error">{actionError}</p>
				{/if}

				{#if changesLoading && !changes}
					<p class="text-muted">Loading changes...</p>
				{:else if changesError}
					<p class="changes-error">{changesError}</p>
				{:else if changes}
					{#if changes.warning}
						<p class="changes-warning">{changes.warning}</p>
					{/if}

					{#if changes.available}
						<p class="changes-notice">작업이 실행되는 동안 이 디렉터리에서 생긴 모든 변경이 포함됩니다.</p>
						{#if changes.files.length === 0}
							<p class="text-muted">변경된 파일이 없습니다.</p>
						{:else}
							<div class="change-list">
								{#each visibleFiles as f (f.path)}
									<button
										type="button"
										class="change-row"
										class:active={expandedFile === f.path}
										onclick={() => toggleFileDiff(f)}
									>
										<span class="status-badge status-{f.status}">{f.status}</span>
										<span class="change-path mono">{displayPath(f)}</span>
										<span class="change-stat">
											{#if f.binary}
												<span class="stat-binary">binary</span>
											{:else}
												{#if f.additions > 0}<span class="stat-add">+{f.additions}</span>{/if}
												{#if f.deletions > 0}<span class="stat-del">−{f.deletions}</span>{/if}
											{/if}
										</span>
									</button>
									{#if expandedFile === f.path}
										<div class="change-diff">
											{#if fileDiffError}
												<p class="changes-error">{fileDiffError}</p>
											{/if}
											<DiffView diff={fileDiff} loading={fileDiffLoading} />
										</div>
									{/if}
								{/each}
							</div>
							{#if !showAllFiles && changes.files.length > MAX_VISIBLE_FILES}
								<button type="button" class="btn btn-sm more-files" onclick={() => showAllFiles = true}>
									더 보기 ({changes.files.length - MAX_VISIBLE_FILES}개 더)
								</button>
							{/if}
						{/if}

						{#if changes.files_modified_mismatch?.length}
							<details class="mismatch">
								<summary>보고했지만 실제 변경이 없는 파일 ({changes.files_modified_mismatch.length})</summary>
								<ul class="files-list mono">
									{#each changes.files_modified_mismatch as file}
										<li>{file}</li>
									{/each}
								</ul>
							</details>
						{/if}
					{:else}
						<p class="changes-reason">{reasonText(changes.reason)}</p>
						{#if task.files_modified?.length}
							<div class="reported-files">
								<h4>에이전트가 보고한 파일 ({task.files_modified.length})</h4>
								<ul class="files-list mono">
									{#each task.files_modified as file}
										<li>{file}</li>
									{/each}
								</ul>
							</div>
						{/if}
					{/if}
				{/if}
			</div>

			{#if providerIsMagi}
				<div class="section">
					<h3>MAGI 심의 Output</h3>
					<MagiStreamPanel lines={liveOutput} maxHeight="600px" />
				</div>
			{:else if hasSubagentOutput}
				<div class="section">
					<h3>서브에이전트 Output</h3>
					<SubagentStreamPanel lines={liveOutput} maxHeight="600px" />
				</div>
			{:else}
				<div class="section">
					<h3>Output ({liveOutput.length} lines)</h3>
					<OutputViewer lines={liveOutput} />
				</div>
			{/if}
		</div>

	{#if revertOpen}
		<div class="modal-overlay">
			<div class="revert-modal card">
				<div class="modal-header">
					<h3>Task #{task.id} 되돌리기</h3>
					<button class="btn btn-sm" onclick={closeRevertModal} disabled={reverting}>Close</button>
				</div>

				{#if revertLoading}
					<p class="text-muted">바뀔 파일을 확인하는 중...</p>
				{:else if revertConflicts.length}
					<p class="revert-error">충돌이 있어 되돌릴 수 없습니다. 작업 트리는 바뀌지 않았습니다.</p>
					<ul class="revert-file-list mono">
						{#each revertConflicts as path}
							<li class="conflict-path">{path}</li>
						{/each}
					</ul>
				{:else if revertError}
					<p class="revert-error">{revertError}</p>
				{:else if revertPreview}
					<p class="revert-notice">다음 파일이 이 작업 이전 상태로 돌아갑니다.</p>
					{#if revertPreview.files?.length}
						<ul class="revert-file-list mono">
							{#each revertPreview.files as f (f.path)}
								<li>
									<span class="status-badge status-{f.status}">{f.status}</span>
									<span>{f.old_path ? `${f.old_path} → ${f.path}` : f.path}</span>
								</li>
							{/each}
						</ul>
					{:else}
						<p class="text-muted">바뀔 파일이 없습니다.</p>
					{/if}
					<p class="revert-warning">되돌린 변경은 커밋되지 않은 변경으로 작업 트리에 남습니다. 직접 확인한 뒤 커밋하세요.</p>
					<div class="modal-actions">
						<button class="btn" onclick={closeRevertModal} disabled={reverting}>취소</button>
						<button
							class="btn btn-danger"
							onclick={confirmRevert}
							disabled={reverting || !revertPreview.changed}
						>
							{reverting ? '되돌리는 중...' : '되돌리기'}
						</button>
					</div>
				{/if}
			</div>
		</div>
	{/if}
	{/if}
</div>

<style>
	.page-nav {
		margin-bottom: 16px;
	}

	.page-nav a {
		color: var(--text-secondary);
		font-size: 13px;
	}

	.page-nav a:hover {
		color: var(--accent);
	}

	.text-muted { color: var(--text-secondary); }

	.detail-header {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-bottom: 20px;
	}

	.detail-header h1 {
		font-size: 22px;
		font-weight: 600;
	}

	.cancel-pending {
		margin-left: auto;
	}

	.cancel-error {
		color: var(--danger);
		font-size: 13px;
		margin: 0 0 16px;
	}

	.meta-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
		gap: 12px;
		margin-bottom: 16px;
	}

	.meta-item {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.meta-label {
		font-size: 11px;
		color: var(--text-secondary);
		text-transform: uppercase;
		font-weight: 500;
	}

	.meta-sub {
		color: var(--text-tertiary);
		font-size: 12px;
	}

	.section {
		margin-bottom: 16px;
	}

	.section h3 {
		font-size: 14px;
		font-weight: 600;
		margin-bottom: 8px;
		color: var(--text-secondary);
	}

	.prompt-text {
		white-space: pre-wrap;
		word-break: break-word;
		font-size: 13px;
		line-height: 1.5;
		margin: 0;
	}

	.error-section {
		border-color: var(--danger);
	}

	.error-section pre {
		color: var(--danger);
		white-space: pre-wrap;
		word-break: break-word;
		margin: 0;
		font-size: 13px;
	}

	.changes-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 12px;
		flex-wrap: wrap;
		margin-bottom: 8px;
	}

	.changes-header h3 {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		margin: 0;
	}

	.changes-summary {
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 400;
		color: var(--text-secondary);
	}

	.changes-actions {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}

	.commit-badge {
		font-size: 11px;
		padding: 2px 8px;
		border-radius: var(--radius-full);
		background: var(--accent-soft);
		color: var(--accent);
	}

	.link-btn {
		background: none;
		border: none;
		padding: 0;
		color: var(--accent);
		font-size: 12px;
		cursor: pointer;
	}

	.link-btn:hover {
		color: var(--accent-hover);
		text-decoration: underline;
	}

	.live-btn {
		font-size: 12px;
	}

	.btn-sm {
		padding: 4px 10px;
		font-size: 12px;
	}

	.changes-notice {
		font-size: 12px;
		color: var(--text-tertiary);
		margin: 0 0 10px;
	}

	.changes-warning {
		font-size: 12px;
		color: var(--warning);
		background: var(--warning-soft);
		padding: 6px 10px;
		border-radius: var(--radius);
		margin: 0 0 10px;
	}

	.changes-error {
		font-size: 12px;
		color: var(--danger);
		margin: 6px 0;
	}

	.changes-reason {
		font-size: 13px;
		color: var(--text-secondary);
		margin: 0 0 10px;
	}

	.change-list {
		border-top: 1px solid var(--border);
	}

	.change-row {
		display: flex;
		align-items: center;
		gap: 8px;
		width: 100%;
		padding: 6px 4px;
		background: transparent;
		border: none;
		border-bottom: 1px solid var(--border-subtle);
		color: var(--text-primary);
		font-size: 12px;
		text-align: left;
		cursor: pointer;
	}

	.change-row:hover,
	.change-row.active {
		background: var(--bg-tertiary);
	}

	.status-badge {
		flex-shrink: 0;
		font-family: var(--font-mono);
		font-size: 10px;
		text-transform: uppercase;
		padding: 1px 6px;
		border-radius: 3px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
	}

	.status-badge.status-added {
		background: var(--success-soft);
		color: var(--success);
	}

	.status-badge.status-deleted {
		background: var(--danger-soft);
		color: var(--danger);
	}

	.status-badge.status-modified {
		background: var(--warning-soft);
		color: var(--warning);
	}

	.status-badge.status-renamed {
		background: var(--accent-soft);
		color: var(--accent);
	}

	.change-path {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.change-stat {
		flex-shrink: 0;
		display: flex;
		gap: 6px;
		font-family: var(--font-mono);
		font-size: 11px;
	}

	.stat-add { color: var(--success); }
	.stat-del { color: var(--danger); }
	.stat-binary { color: var(--text-tertiary); }

	.change-diff {
		padding-left: 12px;
	}

	.more-files {
		margin-top: 10px;
	}

	.mismatch {
		margin-top: 12px;
		font-size: 12px;
		color: var(--text-secondary);
	}

	.mismatch summary {
		cursor: pointer;
	}

	.reported-files {
		margin-top: 8px;
	}

	.reported-files h4 {
		font-size: 12px;
		font-weight: 600;
		color: var(--text-secondary);
		margin: 0 0 6px;
	}

	.files-list {
		list-style: none;
		font-size: 12px;
		color: var(--accent);
	}

	.files-list li {
		padding: 2px 0;
	}

	.reverted-badge {
		font-size: 11px;
		padding: 2px 8px;
		border-radius: var(--radius-full);
		background: var(--warning-soft);
		color: var(--warning);
	}

	.revert-btn {
		font-size: 12px;
	}

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

	.revert-modal {
		width: 100%;
		max-width: 560px;
		max-height: 80vh;
		overflow-y: auto;
		padding: 20px;
	}

	.modal-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		margin-bottom: 14px;
	}

	.modal-header h3 {
		margin: 0;
		font-size: 16px;
	}

	.revert-notice {
		font-size: 13px;
		color: var(--text-secondary);
		margin: 0 0 10px;
	}

	.revert-file-list {
		list-style: none;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		max-height: 240px;
		overflow-y: auto;
		margin: 0 0 12px;
		padding: 4px 0;
		font-size: 12px;
	}

	.revert-file-list li {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 10px;
		overflow-wrap: anywhere;
	}

	.conflict-path {
		color: var(--danger);
	}

	.revert-warning {
		font-size: 12px;
		color: var(--warning);
		background: var(--warning-soft);
		padding: 8px 10px;
		border-radius: var(--radius);
		margin: 0 0 12px;
	}

	.revert-error {
		font-size: 13px;
		color: var(--danger);
		margin: 0 0 12px;
	}

	.modal-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}

</style>
