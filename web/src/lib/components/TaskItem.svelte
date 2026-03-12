<script>
	let { task } = $props();

	const statusIcons = {
		completed: '\u2705',
		running: '\uD83D\uDD04',
		failed: '\u274C',
		pending: '\u23F8',
		stopped: '\u23F9'
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
</script>

<a href="/tasks/{task.id}" class="task-item" class:failed={task.status === 'failed'} class:stopped={task.status === 'stopped'}>
	<span class="task-id mono">#{task.id}</span>
	<span class="task-status">{statusIcons[task.status] || '\u23F8'}</span>
	<span class="task-project">{task.project || '-'}</span>
	<span class="task-prompt">{truncate(task.prompt, 60)}</span>
	<span class="task-time mono">{formatDate(task.created_at)}</span>
</a>

<style>
	.task-item {
		display: grid;
		grid-template-columns: 60px 30px 120px 1fr 100px;
		gap: 8px;
		align-items: center;
		padding: 10px 12px;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		color: var(--text-primary);
		text-decoration: none;
		font-size: 13px;
		transition: background 0.15s;
	}

	.task-item:hover {
		background: var(--bg-tertiary);
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
		font-size: 12px;
	}

	.task-status {
		text-align: center;
	}

	.task-project {
		color: var(--accent);
		font-size: 12px;
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
		font-size: 12px;
		text-align: right;
	}
</style>
