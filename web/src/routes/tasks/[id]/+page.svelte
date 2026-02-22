<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';

	let task = null;
	let loading = true;
	let liveOutput = [];
	let unsubs = [];

	$: taskId = $page.params.id;

	onMount(async () => {
		const res = await apiGet(`/api/tasks/${taskId}`);
		if (res?.data) {
			task = res.data;
			liveOutput = task.output || [];
		}
		loading = false;

		// SSE for live output when task is running
		unsubs.push(onSSE('output', (data) => {
			if (task && task.status === 'running' && task.sheep === data.sheep_name) {
				liveOutput = [...liveOutput, data.text];
			}
		}));

		unsubs.push(onSSE('task_complete', (data) => {
			if (task && data.task_id === task.id) {
				task = { ...task, status: 'completed', summary: data.summary };
			}
		}));

		unsubs.push(onSSE('task_fail', (data) => {
			if (task && data.task_id === task.id) {
				task = { ...task, status: 'failed', error: data.error };
			}
		}));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	function formatDuration(start, end) {
		if (!start || !end) return '-';
		const ms = new Date(end) - new Date(start);
		const sec = Math.floor(ms / 1000);
		const min = Math.floor(sec / 60);
		if (min > 0) return `${min}m ${sec % 60}s`;
		return `${sec}s`;
	}
</script>

<div class="page">
	<div class="page-nav">
		<a href="/tasks">&larr; Back to Tasks</a>
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
			</div>

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
					<pre class="mono">{task.summary}</pre>
				</div>
			{/if}

			{#if task.files_modified?.length}
				<div class="section card">
					<h3>Files Modified ({task.files_modified.length})</h3>
					<ul class="files-list mono">
						{#each task.files_modified as file}
							<li>{file}</li>
						{/each}
					</ul>
				</div>
			{/if}

			<div class="section">
				<h3>Output ({liveOutput.length} lines)</h3>
				<OutputViewer lines={liveOutput} />
			</div>
		</div>
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

	.files-list {
		list-style: none;
		font-size: 12px;
		color: var(--accent);
	}

	.files-list li {
		padding: 2px 0;
	}
</style>
