<script>
	import Icon from './Icon.svelte';

	let { task } = $props();

	const statusIcons = {
		completed: { name: 'check-circle', tone: 'success' },
		running: { name: 'refresh-cw', tone: 'live' },
		failed: { name: 'x-circle', tone: 'danger' },
		pending: { name: 'hourglass', tone: 'warning' },
		stopped: { name: 'stop-circle', tone: 'warning' }
	};

	function formatDate(dateStr) {
		if (!dateStr) return '-';
		const d = new Date(dateStr);
		const month = String(d.getMonth() + 1).padStart(2, '0');
		const day = String(d.getDate()).padStart(2, '0');
		const hour = String(d.getHours()).padStart(2, '0');
		const min = String(d.getMinutes()).padStart(2, '0');
		return `${month}-${day} ${hour}:${min}`;
	}

	function truncate(str, len) {
		if (!str) return '';
		return str.length > len ? str.slice(0, len) + '...' : str;
	}

	let icon = $derived(statusIcons[task.status] ?? { name: 'circle', tone: 'idle' });
</script>

<a href="/tasks/{task.id}" class="task-item" class:failed={task.status === 'failed'} class:stopped={task.status === 'stopped'}>
	<span class="task-id mono">#{task.id}</span>
	<span class="task-status" data-tone={icon.tone}>
		<Icon name={icon.name} size={16} label={task.status} />
	</span>
	<span class="task-project">{task.project || '-'}</span>
	<span class="task-prompt">{truncate(task.prompt, 60)}</span>
	<span class="task-time mono">{formatDate(task.created_at)}</span>
</a>

<style>
	.task-item {
		display: grid;
		grid-template-columns: 60px 30px 120px 1fr 100px;
		gap: var(--space-2);
		align-items: center;
		padding: 10px 12px;
		background: var(--bg-2);
		border: 0;
		border-radius: 0;
		box-shadow: 0 -2px 0 0 var(--lo);
		color: var(--text-primary);
		text-decoration: none;
		font-size: var(--fs-sm);
	}

	.task-item:hover {
		background: var(--bg-3);
		text-decoration: none;
	}

	.task-item.failed {
		border-left: 3px solid var(--danger);
	}

	.task-item.stopped {
		border-left: 3px solid var(--warning);
	}

	.task-id {
		color: var(--text-secondary);
		font-size: var(--fs-xs);
	}

	/* Status square: 2px black ring, status fill, black glyph */
	.task-status {
		display: inline-flex;
		justify-content: center;
		align-items: center;
		width: 22px;
		height: 22px;
		margin: 2px;
		background: var(--p-dgray);
		box-shadow: var(--px-ring-2);
		color: #000;
	}

	.task-status[data-tone="success"] { background: var(--success); }
	.task-status[data-tone="live"] { background: var(--live); }
	.task-status[data-tone="danger"] { background: var(--danger); }
	.task-status[data-tone="warning"] { background: var(--warning); }

	.task-project {
		color: var(--highlight);
		font-size: var(--fs-xs);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-prompt {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-time {
		color: var(--text-secondary);
		font-size: var(--fs-xs);
		text-align: right;
	}
</style>
