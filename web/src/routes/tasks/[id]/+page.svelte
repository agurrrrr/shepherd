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
	import Icon from '$lib/components/Icon.svelte';
	import { translate, t } from '$lib/i18n/index.js';

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
	// Shepherd screens pass ?from=shepherd&tab=<tab> (#623) — the tab they
	// sent us from (history / patrols / output / attention). No tab means the
	// 목자 작업 list, the shepherd screen's default "back" home.
	let backFromShepherd = $derived(from === 'shepherd');
	let shepherdBackTab = $derived.by(() => {
		const t = $page.url.searchParams.get('tab');
		return ['output', 'history', 'patrols', 'board', 'attention'].includes(t) ? t : 'history';
	});
	let backLink = $derived(backFromProject
		? `/projects/${encodeURIComponent(task.project)}?tab=history`
		: backFromShepherd
			? `/shepherd?tab=${shepherdBackTab}`
			: '/tasks');
	let backLabel = $derived(backFromProject ? task.project : backFromShepherd ? 'Shepherd' : 'Tasks');

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

	const reasonKeys = {
		'checkpoint disabled': 'changes.reason.checkpointDisabled',
		'not a git repository': 'changes.reason.notGitRepo',
		'too large': 'changes.reason.tooLarge',
		'no end snapshot': 'changes.reason.noEndSnapshot',
		'snapshot pruned': 'changes.reason.snapshotPruned',
		'shepherd task': 'changes.reason.shepherdTask',
		running: 'changes.reason.running',
	};

	function reasonText(reason) {
		const key = reasonKeys[reason];
		return key ? translate(key) : (reason || translate('changes.reason.unknown'));
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
				revertError = res?.message || translate('revert.previewFailed');
				revertConflicts = res?.conflicts || [];
			}
		} catch (err) {
			revertError = err?.message || translate('revert.previewFailed');
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
				revertError = res?.message || translate('revert.failed');
				revertConflicts = res?.conflicts || [];
			}
		} catch (err) {
			revertError = err?.message || translate('revert.failed');
		} finally {
			reverting = false;
		}
	}

	async function undoRevert() {
		if (undoLoading) return;
		if (!confirm(translate('revert.undoConfirm'))) return;
		undoLoading = true;
		actionError = '';
		try {
			const res = await apiPost(`/api/tasks/${taskId}/revert/undo`, {});
			if (res?.success) {
				await refreshTask();
				await loadChanges(false);
			} else {
				actionError = res?.message || translate('revert.undoFailed');
			}
		} catch (err) {
			actionError = err?.message || translate('revert.undoFailed');
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

	// Balance the fact strip: up to 6 facts sit on one row, more split into two
	// even rows instead of leaving a mostly empty second row.
	let metaCols = $derived.by(() => {
		if (!task) return 4;
		const n = 4 + (task.started_at ? 1 : 0) + (task.completed_at ? 2 : 0) + (task.total_tokens > 0 ? 1 : 0) + (task.cost_usd > 0 ? 1 : 0);
		return n <= 6 ? n : Math.ceil(n / 2);
	});

	// Detect subagent output: any line starting with [SUB: triggers the panel.
	// Same routing pattern as MAGI ([MAGI: prefix → MagiStreamPanel).
	let hasSubagentOutput = $derived(
		liveOutput.some((line) => /^\[SUB:/.test(line ?? ''))
	);
</script>

<div class="page">
	<div class="page-nav">
		<a href={backLink} class="crumb"><Icon name="arrow-left" size={13} /><span>{backLabel}</span></a>
	</div>

	{#if loading}
		<div class="skeleton" style="height: 52px; width: 280px; margin-bottom: 24px"></div>
		<div class="skeleton" style="height: 74px; margin-bottom: 18px"></div>
		<div class="skeleton" style="height: 180px"></div>
	{:else if !task}
		<div class="card"><p class="text-muted">Task not found</p></div>
	{:else}
		<div class="task-detail">
			<header class="detail-header">
				<div class="dh-text">
					<span class="eyebrow">{task.project || 'Task'}{task.sheep ? ` · ${task.sheep}` : ''}</span>
					<div class="dh-title-row">
						<h1><span class="hash">#</span>{task.id}</h1>
						<StatusBadge status={task.status} />
						{#if task.reverted_at}
							<span class="chip chip-queued" title={task.reverted_at}>{$t('taskDetail.revertChip', { time: task.reverted_at })}</span>
						{/if}
					</div>
				</div>
				{#if task.status === 'pending'}
					<button class="btn btn-danger cancel-pending" onclick={cancelPending} disabled={cancelling}>
						<Icon name="x" size={14} />
						{cancelling ? 'Cancelling…' : 'Cancel'}
					</button>
				{/if}
			</header>
			{#if cancelError}
				<p class="cancel-error">{cancelError}</p>
			{/if}

			<dl class="meta-grid" style="--meta-cols: {metaCols}">
				<div class="meta-item">
					<dt class="meta-label">Project</dt>
					<dd>
						{#if task.project}
							<a href="/projects/{encodeURIComponent(task.project)}">{task.project}</a>
						{:else if task.shepherd_kind}
							<span class="chip chip-accent">{$t('taskDetail.shepherdChip')}</span>
						{:else}
							–
						{/if}
					</dd>
				</div>
				<div class="meta-item">
					<dt class="meta-label">Sheep</dt>
					<dd>{task.sheep || '–'}</dd>
				</div>
				<div class="meta-item">
					<dt class="meta-label">Provider</dt>
					<dd class="mono">{task.provider || '–'}</dd>
				</div>
				<div class="meta-item">
					<dt class="meta-label">Created</dt>
					<dd class="mono num">{task.created_at || '–'}</dd>
				</div>
				{#if task.started_at}
					<div class="meta-item">
						<dt class="meta-label">Started</dt>
						<dd class="mono num">{task.started_at}</dd>
					</div>
				{/if}
				{#if task.completed_at}
					<div class="meta-item">
						<dt class="meta-label">Completed</dt>
						<dd class="mono num">{task.completed_at}</dd>
					</div>
					<div class="meta-item">
						<dt class="meta-label">Duration</dt>
						<dd class="mono num">{formatDuration(task.started_at, task.completed_at)}</dd>
					</div>
				{/if}
				{#if task.total_tokens > 0}
					<div class="meta-item">
						<dt class="meta-label">Tokens</dt>
						<dd class="mono num">{task.total_tokens.toLocaleString()}</dd>
						<dd class="mono num meta-sub">{(task.prompt_tokens ?? 0).toLocaleString()} in · {(task.completion_tokens ?? 0).toLocaleString()} out</dd>
					</div>
				{/if}
				{#if task.cost_usd > 0}
					<div class="meta-item">
						<dt class="meta-label">Cost</dt>
						<dd class="mono num">${task.cost_usd.toFixed(4)}</dd>
					</div>
				{/if}
			</dl>

			<div class="section card">
				<h3 class="section-label">Prompt</h3>
				<pre class="mono prompt-text">{task.prompt}</pre>
			</div>

			{#if task.error}
				<div class="section card error-section">
					<h3 class="section-label"><Icon name="alert-triangle" size={12} /> Error</h3>
					<pre class="mono">{task.error}</pre>
				</div>
			{/if}

			{#if task.summary}
				<div class="section card">
					<h3 class="section-label">Summary</h3>
					<MarkdownBody text={task.summary} />
				</div>
			{/if}

			<div class="section card changes-card">
				<div class="changes-header">
					<h3 class="section-label ko">
						{$t('taskDetail.changesTitle')}
						{#if changes?.available}
							<span class="changes-summary">{changes.files.length} files · +{changes.additions} −{changes.deletions}</span>
						{/if}
					</h3>
					<div class="changes-actions">
						{#if changes?.commits_during_task?.length}
							<span class="commit-badge">{$t('taskDetail.commitsBadge', { count: changes.commits_during_task.length })}</span>
						{/if}
						{#if changes?.available}
							<button type="button" class="link-btn" onclick={downloadPatch}><Icon name="arrow-up-right" size={12} /> {$t('taskDetail.downloadPatch')}</button>
						{/if}
						{#if task.status === 'running'}
							<button
								type="button"
								class="btn btn-sm live-btn"
								onclick={() => loadChanges(true)}
								disabled={changesLoading}
							>
								{changesLive ? $t('taskDetail.refreshLiveChanges') : $t('taskDetail.viewLiveChanges')}
							</button>
						{/if}
						{#if canRevert}
							<button type="button" class="btn btn-sm revert-btn" onclick={openRevertModal}>
								<Icon name="rotate-ccw" size={12} />
								{$t('taskDetail.revertButton')}
							</button>
						{/if}
						{#if isReverted}
							<button
								type="button"
								class="btn btn-sm"
								onclick={undoRevert}
								disabled={undoLoading}
							>
								{undoLoading ? '...' : $t('taskDetail.undoRevertButton')}
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
						<p class="changes-notice">{$t('taskDetail.changesNotice')}</p>
						{#if changes.files.length === 0}
							<p class="text-muted">{$t('taskDetail.noChangedFiles')}</p>
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
									{$t('taskDetail.showMoreFiles', { count: changes.files.length - MAX_VISIBLE_FILES })}
								</button>
							{/if}
						{/if}

						{#if changes.files_modified_mismatch?.length}
							<details class="mismatch">
								<summary>{$t('taskDetail.mismatchSummary', { count: changes.files_modified_mismatch.length })}</summary>
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
								<h4>{$t('taskDetail.reportedFiles', { count: task.files_modified.length })}</h4>
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
					<h3 class="section-label ko">{$t('taskDetail.magiOutputTitle')}</h3>
					<MagiStreamPanel lines={liveOutput} maxHeight="600px" />
				</div>
			{:else if hasSubagentOutput}
				<div class="section">
					<h3 class="section-label ko">{$t('taskDetail.subagentOutputTitle')}</h3>
					<SubagentStreamPanel lines={liveOutput} maxHeight="600px" />
				</div>
			{:else}
				<div class="section">
					<h3 class="section-label">Output <span class="label-count">{liveOutput.length.toLocaleString()} lines</span></h3>
					<OutputViewer lines={liveOutput} />
				</div>
			{/if}
		</div>

	{#if revertOpen}
		<div class="modal-overlay">
			<div class="modal-content revert-modal card" role="dialog" aria-modal="true" aria-label={$t('revert.modalAria')}>
				<div class="modal-header">
					<h3>{$t('revert.modalTitle', { id: task.id })}</h3>
					<button class="btn btn-sm btn-ghost btn-icon" onclick={closeRevertModal} disabled={reverting} aria-label="Close"><Icon name="x" size={15} /></button>
				</div>

				{#if revertLoading}
					<p class="text-muted">{$t('revert.checking')}</p>
				{:else if revertConflicts.length}
					<p class="revert-error">{$t('revert.conflictError')}</p>
					<ul class="revert-file-list mono">
						{#each revertConflicts as path}
							<li class="conflict-path">{path}</li>
						{/each}
					</ul>
				{:else if revertError}
					<p class="revert-error">{revertError}</p>
				{:else if revertPreview}
					<p class="revert-notice">{$t('revert.notice')}</p>
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
						<p class="text-muted">{$t('revert.noFiles')}</p>
					{/if}
					<p class="revert-warning">{$t('revert.warning')}</p>
					<div class="modal-actions">
						<button class="btn" onclick={closeRevertModal} disabled={reverting}>{$t('common.cancel')}</button>
						<button
							class="btn btn-danger-solid"
							onclick={confirmRevert}
							disabled={reverting || !revertPreview.changed}
						>
							{reverting ? $t('revert.reverting') : $t('revert.confirm')}
						</button>
					</div>
				{/if}
			</div>
		</div>
	{/if}
	{/if}
</div>

<style>
	.page {
		max-width: var(--content-max);
	}

	.page-nav {
		margin-bottom: 22px;
	}

	.crumb {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
		transition: color var(--dur-1) steps(2), gap var(--dur-2) var(--ease-out);
	}

	.crumb:hover {
		color: var(--accent);
		text-decoration: none;
		gap: 9px;
	}

	.detail-header {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: 16px;
		margin-bottom: 22px;
		animation: rise-in var(--dur-3) var(--ease-out) backwards;
	}

	.dh-text {
		display: flex;
		flex-direction: column;
		gap: 10px;
		min-width: 0;
	}

	.dh-title-row {
		display: flex;
		align-items: center;
		gap: 14px;
		flex-wrap: wrap;
	}

	/* 48px keeps the task id on Press Start 2P's 8px crisp grid (issue #636 §2-1). */
	.detail-header h1 {
		font-family: var(--font-display);
		font-size: var(--fs-3xl);
		font-weight: 400;
		line-height: 0.95;
		letter-spacing: 0;
	}

	.hash {
		color: var(--text-tertiary);
		margin-right: 0.04em;
	}

	.cancel-error {
		color: var(--danger-text);
		font-size: var(--fs-sm);
		margin: 0 0 16px;
	}

	/* Fact strip */
	.meta-grid {
		display: grid;
		grid-template-columns: repeat(var(--meta-cols, 4), minmax(0, 1fr));
		margin: 0 0 20px;
		border: 1px solid var(--border);
		border-radius: 0;
		background: var(--bg-2);
		overflow: hidden;
		animation: rise-in var(--dur-3) var(--ease-out) 60ms backwards;
	}

	.meta-item {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 14px 16px;
		box-shadow: -1px 0 0 var(--border-subtle), 0 -1px 0 var(--border-subtle);
		min-width: 0;
	}

	.meta-item dd {
		margin: 0;
		font-size: var(--fs-sm);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.meta-item dd.mono {
		font-size: var(--fs-xs);
	}

	.meta-item a {
		color: var(--text-primary);
	}

	.meta-item a:hover {
		color: var(--accent);
	}

	.meta-label {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.meta-item dd.meta-sub {
		color: var(--text-tertiary);
		font-size: var(--fs-2xs);
	}

	.section {
		margin-bottom: 18px;
		animation: rise-in var(--dur-3) var(--ease-out) 120ms backwards;
	}

	.section.card {
		padding: 18px 20px;
	}

	.section-label {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 0 0 12px;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	/* Hangul does not take wide caps tracking */
	.section-label.ko {
		font-family: var(--font-sans);
		font-size: var(--fs-xs);
		letter-spacing: 0;
	}

	.label-count {
		letter-spacing: 0;
		text-transform: none;
		color: var(--text-tertiary);
		opacity: 0.8;
	}

	.prompt-text {
		white-space: pre-wrap;
		word-break: break-word;
		font-size: var(--fs-xs);
		line-height: 1.65;
		margin: 0;
		max-height: 420px;
		overflow: auto;
		color: var(--text-primary);
	}

	.error-section {
		background: linear-gradient(var(--danger-soft), var(--danger-soft)), var(--bg-2);
		box-shadow: var(--px-ring), inset 3px 3px 0 0 var(--danger), inset -3px -3px 0 0 var(--lo);
	}

	.error-section .section-label {
		color: var(--danger-text);
	}

	.error-section pre {
		color: var(--danger-text);
		white-space: pre-wrap;
		word-break: break-word;
		margin: 0;
		font-size: var(--fs-xs);
		line-height: 1.6;
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
		flex-wrap: wrap;
		margin: 0;
	}

	.changes-summary {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		font-weight: 400;
		letter-spacing: 0;
		text-transform: none;
		color: var(--text-secondary);
	}

	.changes-actions {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}

	.commit-badge {
		font-size: var(--fs-2xs);
		padding: 2px 8px;
		border-radius: 0;
		background: var(--accent-soft);
		color: var(--accent);
	}

	.link-btn {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		background: none;
		border: none;
		padding: 0;
		color: var(--accent);
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		cursor: pointer;
	}

	.link-btn:hover {
		color: var(--accent-hover);
		text-decoration: underline;
	}

	.live-btn {
		font-size: var(--fs-xs);
	}

	.changes-notice {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		margin: 0 0 10px;
	}

	.changes-warning {
		font-size: var(--fs-xs);
		color: var(--warning);
		background: var(--warning-soft);
		padding: 6px 10px;
		border-radius: 0;
		margin: 0 0 10px;
	}

	.changes-error {
		font-size: var(--fs-xs);
		color: var(--danger-text);
		margin: 6px 0;
	}

	.changes-reason {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		margin: 0 0 10px;
	}

	.change-list {
		border-top: 1px solid var(--border-subtle);
	}

	.change-row {
		display: flex;
		align-items: center;
		gap: 10px;
		width: 100%;
		padding: 8px 8px;
		background: transparent;
		border: none;
		border-bottom: 1px solid var(--border-subtle);
		color: var(--text-primary);
		font-size: var(--fs-xs);
		text-align: left;
		cursor: pointer;
		transition: background var(--dur-1) steps(2);
	}

	.change-row:hover,
	.change-row.active {
		background: var(--bg-3);
	}

	.status-badge {
		flex-shrink: 0;
		min-width: 64px;
		text-align: center;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		padding: 1px 6px;
		border-radius: 0;
		background: var(--well);
		color: var(--text-secondary);
	}

	.status-badge.status-added {
		background: var(--success-soft);
		color: var(--success);
	}

	.status-badge.status-deleted {
		background: var(--danger-soft);
		color: var(--danger-text);
	}

	.status-badge.status-modified {
		background: var(--warning-soft);
		color: var(--warning);
	}

	.status-badge.status-renamed {
		background: var(--info-soft);
		color: var(--info);
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
		font-size: var(--fs-2xs);
	}

	.stat-add { color: var(--success); }
	.stat-del { color: var(--danger-text); }
	.stat-binary { color: var(--text-tertiary); }

	.change-diff {
		padding-left: 12px;
	}

	.more-files {
		margin-top: 10px;
	}

	.mismatch {
		margin-top: 12px;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.mismatch summary {
		cursor: pointer;
	}

	.reported-files {
		margin-top: 8px;
	}

	.reported-files h4 {
		font-size: var(--fs-xs);
		font-weight: 600;
		color: var(--text-secondary);
		margin: 0 0 6px;
	}

	.files-list {
		list-style: none;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.files-list li {
		padding: 2px 0;
	}

	.revert-modal {
		max-width: 580px;
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
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		font-weight: 400;
	}

	.revert-notice {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		margin: 0 0 10px;
	}

	.revert-file-list {
		list-style: none;
		border: 1px solid var(--border);
		border-radius: 0;
		max-height: 240px;
		overflow-y: auto;
		margin: 0 0 12px;
		padding: 4px 0;
		font-size: var(--fs-xs);
	}

	.revert-file-list li {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 4px 10px;
		overflow-wrap: anywhere;
	}

	.conflict-path {
		color: var(--danger-text);
	}

	.revert-warning {
		font-size: var(--fs-xs);
		color: var(--warning);
		background: var(--warning-soft);
		padding: 8px 10px;
		border-radius: 0;
		margin: 0 0 12px;
	}

	.revert-error {
		font-size: var(--fs-sm);
		color: var(--danger-text);
		margin: 0 0 12px;
	}

	.modal-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}

	@media (max-width: 1100px) {
		.meta-grid {
			grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
		}
	}

	@media (max-width: 768px) {
		.detail-header {
			flex-direction: column;
			align-items: flex-start;
		}

		.meta-grid {
			grid-template-columns: 1fr 1fr;
		}

		.section.card {
			padding: 14px;
		}
	}
</style>
