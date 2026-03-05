<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { sheep, systemStatus } from '$lib/stores.js';
	import { onSSE } from '$lib/sse.js';
	import SheepCard from '$lib/components/SheepCard.svelte';
	import CommandInput from '$lib/components/CommandInput.svelte';

	let loaded = false;
	let dashboard = null;
	let unsubscribers = [];

	onMount(async () => {
		await refreshData();
		loaded = true;

		unsubscribers.push(
			onSSE('status_change', () => refreshData()),
			onSSE('task_complete', () => refreshData()),
			onSSE('task_start', () => refreshData()),
			onSSE('task_fail', () => refreshData())
		);
	});

	onDestroy(() => {
		unsubscribers.forEach(fn => fn?.());
	});

	async function refreshData() {
		const [dashRes, sheepRes] = await Promise.all([
			apiGet('/api/dashboard'),
			apiGet('/api/sheep')
		]);
		if (dashRes?.data) {
			dashboard = dashRes.data;
			// Keep systemStatus in sync for sidebar
			systemStatus.set({
				sheep: { total: dashRes.data.sheep.total, working: dashRes.data.sheep.working, idle: dashRes.data.sheep.idle, error: dashRes.data.sheep.error },
				projects: dashRes.data.projects,
				tasks: dashRes.data.tasks
			});
		}
		if (sheepRes?.data) sheep.set(sheepRes.data);
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

	function statusIcon(status) {
		switch (status) {
			case 'completed': return '\u2705';
			case 'failed': return '\u274C';
			case 'running': return '\u26A1';
			case 'pending': return '\u23F3';
			default: return '\u2022';
		}
	}
</script>

<div class="dashboard">
	<!-- Command Input -->
	<div class="command-section card">
		<CommandInput />
	</div>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if dashboard}
		<!-- Stats Row: compact overview -->
		<div class="stats-row">
			<div class="stat-chip">
				<span class="stat-chip-value">{dashboard.sheep?.total ?? 0}</span>
				<span class="stat-chip-label">Sheep</span>
			</div>
			<div class="stat-chip accent">
				<span class="stat-chip-value">{dashboard.sheep?.working ?? 0}</span>
				<span class="stat-chip-label">Working</span>
			</div>
			<div class="stat-chip">
				<span class="stat-chip-value">{dashboard.projects ?? 0}</span>
				<span class="stat-chip-label">Projects</span>
			</div>
			<div class="stat-chip warning">
				<span class="stat-chip-value">{dashboard.tasks?.pending ?? 0}</span>
				<span class="stat-chip-label">Pending</span>
			</div>
			<div class="stat-chip success">
				<span class="stat-chip-value">{dashboard.tasks?.completed ?? 0}</span>
				<span class="stat-chip-label">Completed</span>
			</div>
			<div class="stat-chip danger">
				<span class="stat-chip-value">{dashboard.tasks?.failed ?? 0}</span>
				<span class="stat-chip-label">Failed</span>
			</div>
		</div>

		<!-- Today's Activity -->
		{#if dashboard.today}
			<div class="today-bar card">
				<span class="today-label">Today</span>
				<div class="today-stats">
					<span class="today-stat">{dashboard.today.total} tasks</span>
					<span class="today-stat success">{dashboard.today.completed} completed</span>
					{#if dashboard.today.failed > 0}
						<span class="today-stat danger">{dashboard.today.failed} failed</span>
					{/if}
				</div>
			</div>
		{/if}

		<!-- Sheep Grid: all sheep at a glance -->
		{@const sheepList = dashboard.sheep?.list || []}
		{#if sheepList.length > 0}
			<h2 class="section-title">Flock</h2>
			<div class="flock-grid">
				{#each sheepList as s (s.name)}
					<div class="flock-item card" class:working={s.status === 'working'} class:error={s.status === 'error'}>
						<div class="flock-header">
							<span class="flock-dot {s.status}"></span>
							<span class="flock-name">{s.name}</span>
						</div>
						{#if s.project}
							<span class="flock-project">{s.project}</span>
						{:else}
							<span class="flock-unassigned">unassigned</span>
						{/if}
						<span class="flock-provider">{s.provider}</span>
					</div>
				{/each}
			</div>
		{/if}

		<!-- Working sheep with live output -->
		{@const working = $sheep.filter(s => s.status === 'working' && s.project)}
		{#if working.length > 0}
			<h2 class="section-title">Live</h2>
			<div class="sheep-grid">
				{#each working as s (s.name)}
					<SheepCard
						name={s.name}
						project={s.project}
						status={s.status}
						provider={s.provider}
					/>
				{/each}
			</div>
		{/if}

		<!-- Recent Tasks -->
		{@const recentTasks = dashboard.recent_tasks || []}
		{#if recentTasks.length > 0}
			<h2 class="section-title">Recent Tasks</h2>
			<div class="recent-list">
				{#each recentTasks as task (task.id)}
					<a href="/tasks" class="recent-item card">
						<span class="recent-icon">{statusIcon(task.status)}</span>
						<div class="recent-body">
							<div class="recent-top">
								<span class="recent-prompt">{task.summary || task.prompt}</span>
								<span class="recent-time">{timeAgo(task.created_at)}</span>
							</div>
							<div class="recent-meta">
								{#if task.project}
									<span class="badge badge-idle">{task.project}</span>
								{/if}
								{#if task.sheep}
									<span class="recent-sheep">{task.sheep}</span>
								{/if}
								<span class="badge badge-{task.status}">{task.status}</span>
							</div>
							{#if task.status === 'failed' && task.error}
								<span class="recent-error">{task.error}</span>
							{/if}
						</div>
					</a>
				{/each}
			</div>
		{:else}
			<div class="card empty-state">
				<p>No tasks yet.</p>
				<p class="text-muted">Use the command input above to get started.</p>
			</div>
		{/if}
	{/if}
</div>

<style>
	.dashboard {
		max-width: 1200px;
	}

	.command-section {
		margin-bottom: 20px;
		padding: 12px 16px;
	}

	.text-muted {
		color: var(--text-secondary);
	}

	/* Stats Row */
	.stats-row {
		display: flex;
		gap: 8px;
		margin-bottom: 16px;
		flex-wrap: wrap;
	}

	.stat-chip {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 12px;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 20px;
		font-size: 13px;
	}

	.stat-chip-value {
		font-weight: 700;
		font-family: var(--font-mono);
	}

	.stat-chip-label {
		color: var(--text-secondary);
	}

	.stat-chip.accent .stat-chip-value { color: var(--accent); }
	.stat-chip.success .stat-chip-value { color: var(--success); }
	.stat-chip.warning .stat-chip-value { color: var(--warning); }
	.stat-chip.danger .stat-chip-value { color: var(--danger); }

	/* Today bar */
	.today-bar {
		display: flex;
		align-items: center;
		gap: 16px;
		padding: 10px 16px;
		margin-bottom: 20px;
	}

	.today-label {
		font-weight: 600;
		font-size: 13px;
		color: var(--accent);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.today-stats {
		display: flex;
		gap: 12px;
		font-size: 13px;
	}

	.today-stat {
		color: var(--text-secondary);
	}

	.today-stat.success { color: var(--success); }
	.today-stat.danger { color: var(--danger); }

	/* Section title */
	.section-title {
		font-size: 14px;
		font-weight: 600;
		margin-bottom: 10px;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	/* Flock Grid */
	.flock-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
		gap: 8px;
		margin-bottom: 24px;
	}

	.flock-item {
		padding: 10px 12px;
		display: flex;
		flex-direction: column;
		gap: 3px;
		transition: border-color 0.2s;
	}

	.flock-item.working {
		border-color: var(--accent);
	}

	.flock-item.error {
		border-color: var(--danger);
	}

	.flock-header {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.flock-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.flock-dot.idle { background: var(--text-secondary); }
	.flock-dot.working {
		background: var(--accent);
		animation: pulse 1.5s ease-in-out infinite;
	}
	.flock-dot.error { background: var(--danger); }

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	.flock-name {
		font-weight: 600;
		font-size: 13px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.flock-project {
		font-size: 12px;
		color: var(--accent);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.flock-unassigned {
		font-size: 12px;
		color: var(--text-secondary);
		font-style: italic;
	}

	.flock-provider {
		font-size: 11px;
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}

	/* Live working sheep */
	.sheep-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
		gap: 12px;
		margin-bottom: 24px;
	}

	/* Recent Tasks */
	.recent-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
		margin-bottom: 24px;
	}

	.recent-item {
		display: flex;
		gap: 10px;
		padding: 10px 14px;
		text-decoration: none;
		color: inherit;
		transition: border-color 0.15s;
	}

	.recent-item:hover {
		border-color: var(--accent);
		text-decoration: none;
		color: inherit;
	}

	.recent-icon {
		flex-shrink: 0;
		font-size: 14px;
		margin-top: 2px;
	}

	.recent-body {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.recent-top {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		gap: 8px;
	}

	.recent-prompt {
		font-size: 13px;
		line-height: 1.4;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.recent-time {
		flex-shrink: 0;
		font-size: 11px;
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}

	.recent-meta {
		display: flex;
		gap: 6px;
		align-items: center;
		flex-wrap: wrap;
	}

	.recent-sheep {
		font-size: 11px;
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}

	.recent-error {
		font-size: 12px;
		color: var(--danger);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	/* Empty state */
	.empty-state {
		text-align: center;
		padding: 40px;
		color: var(--text-secondary);
	}

	/* Responsive */
	@media (max-width: 768px) {
		.stats-row {
			gap: 6px;
		}

		.stat-chip {
			padding: 5px 10px;
			font-size: 12px;
		}

		.flock-grid {
			grid-template-columns: repeat(2, 1fr);
			gap: 6px;
		}

		.sheep-grid {
			grid-template-columns: 1fr;
		}

		.command-section {
			padding: 10px 12px;
		}

		.today-bar {
			flex-direction: column;
			align-items: flex-start;
			gap: 6px;
		}
	}
</style>
