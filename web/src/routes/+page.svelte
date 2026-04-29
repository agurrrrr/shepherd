<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { sheep, systemStatus } from '$lib/stores.js';
	import { onSSE } from '$lib/sse.js';
	import SheepCard from '$lib/components/SheepCard.svelte';
	import CommandInput from '$lib/components/CommandInput.svelte';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Tabs from '$lib/components/Tabs.svelte';

	let loaded = false;
	let dashboard = null;
	let unsubscribers = [];
	let activityWindow = '1d';

	const activityTabs = [
		{ value: '5h', label: '5h' },
		{ value: '1d', label: '1d' },
		{ value: '7d', label: '7d' }
	];

	function fmtCost(n) {
		const v = Number(n) || 0;
		if (v === 0) return '$0';
		if (v < 0.01) return '$' + v.toFixed(4);
		if (v < 1) return '$' + v.toFixed(3);
		return '$' + v.toFixed(2);
	}

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
			case 'completed': return { name: 'check-circle', tone: 'success' };
			case 'failed': return { name: 'x-circle', tone: 'danger' };
			case 'running': return { name: 'zap', tone: 'live' };
			case 'pending': return { name: 'hourglass', tone: 'warning' };
			default: return { name: 'circle', tone: 'idle' };
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
		<!-- Hero: Live working sheep — first thing the user sees -->
		{@const working = $sheep.filter(s => s.status === 'working' && s.project)}
		{#if working.length > 0}
			<section class="live-section">
				<SectionHeader
					eyebrow="Currently Running"
					title={working.length === 1 ? '1 sheep working' : `${working.length} sheep working`}
					tone="live"
				/>
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
			</section>
		{/if}

		<!-- Activity stripe: stats + today combined -->
		<section class="activity-stripe">
			<div class="stat-chip">
				<span class="stat-chip-value">{dashboard.sheep?.total ?? 0}</span>
				<span class="stat-chip-label">Sheep</span>
			</div>
			<div class="stat-chip live">
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
			{#if dashboard.today}
				<span class="stripe-divider"></span>
				<span class="today-stat">
					<span class="today-label">Today</span>
					<span class="today-num">{dashboard.today.total}</span>
				</span>
				{#if dashboard.today.completed > 0}
					<span class="today-stat success-tone">{dashboard.today.completed} done</span>
				{/if}
				{#if dashboard.today.failed > 0}
					<span class="today-stat danger-tone">{dashboard.today.failed} failed</span>
				{/if}
			{/if}
		</section>

		<!-- Activity: per-project task count + cost across rolling windows -->
		{@const activity = dashboard.activity?.[activityWindow] || { items: [], total: 0, total_cost: 0 }}
		{@const activityMaxCost = activity.items.reduce((m, it) => Math.max(m, it.cost_usd || 0), 0)}
		<section class="activity-section">
			<div class="activity-head">
				<SectionHeader
					title="Activity"
					subtitle={`${activity.total} tasks · ${fmtCost(activity.total_cost)}`}
				/>
				<Tabs
					tabs={activityTabs}
					value={activityWindow}
					onChange={(v) => (activityWindow = v)}
					ariaLabel="Activity window"
				/>
			</div>
			{#if activity.items.length > 0}
				<div class="activity-list card">
					{#each activity.items as it (it.project)}
						{@const ratio = activityMaxCost > 0 ? (it.cost_usd / activityMaxCost) : 0}
						<div class="activity-row">
							<span class="activity-bar" style="--ratio: {ratio}"></span>
							<span class="activity-name" class:unassigned={it.project === '(unassigned)'}>{it.project}</span>
							<span class="activity-tasks">{it.tasks}</span>
							<span class="activity-cost">{fmtCost(it.cost_usd)}</span>
						</div>
					{/each}
				</div>
			{:else}
				<div class="card">
					<EmptyState
						icon="sheep"
						title="No activity in this window"
						description="Try a wider window or kick off a task above."
					/>
				</div>
			{/if}
		</section>

		<!-- Recent Tasks -->
		{@const recentTasks = dashboard.recent_tasks || []}
		<section class="recent-section">
			<SectionHeader title="Recent Tasks" />
			{#if recentTasks.length > 0}
				<div class="recent-list">
					{#each recentTasks as task (task.id)}
						{@const icn = statusIcon(task.status)}
						<a href="/tasks" class="recent-item card" class:failed={task.status === 'failed'}>
							<span class="recent-icon" data-tone={icn.tone}>
								<Icon name={icn.name} size={16} label={task.status} />
							</span>
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
				<div class="card">
					<EmptyState
						icon="sheep"
						title="The flock is resting"
						description="No tasks yet. Use the command input above to start your first one."
					/>
				</div>
			{/if}
		</section>
	{/if}
</div>

<style>
	.dashboard {
		max-width: 1200px;
	}

	.command-section {
		margin-bottom: var(--space-5);
		padding: var(--space-3) var(--space-4);
	}

	.text-muted {
		color: var(--text-secondary);
	}

	/* === Live (Hero) section === */
	.live-section {
		margin-bottom: var(--space-6);
		padding: var(--space-4);
		background:
			linear-gradient(180deg, rgba(86, 212, 221, 0.06), transparent 60%),
			var(--bg-2);
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		box-shadow: inset 3px 0 0 var(--live);
	}

	/* === Activity stripe (compact stats + today) === */
	.activity-stripe {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		flex-wrap: wrap;
		margin-bottom: var(--space-6);
		padding: var(--space-2);
		background: var(--bg-2);
		border: 1px solid var(--border);
		border-radius: var(--radius);
	}

	.stat-chip {
		display: inline-flex;
		align-items: baseline;
		gap: 6px;
		padding: 4px 10px;
		background: var(--bg-3);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-full);
		font-size: var(--fs-sm);
		line-height: 1.3;
	}

	.stat-chip-value {
		font-weight: var(--fw-bold);
		font-family: var(--font-mono);
		color: var(--text-primary);
	}

	.stat-chip-label {
		color: var(--text-secondary);
		font-size: var(--fs-xs);
	}

	.stat-chip.live .stat-chip-value { color: var(--live); }
	.stat-chip.success .stat-chip-value { color: var(--success); }
	.stat-chip.warning .stat-chip-value { color: var(--warning); }
	.stat-chip.danger .stat-chip-value { color: var(--danger); }
	.stat-chip.accent .stat-chip-value { color: var(--accent); }

	.stripe-divider {
		width: 1px;
		height: 18px;
		background: var(--border);
		margin: 0 4px;
	}

	.today-stat {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.today-label {
		font-size: var(--fs-2xs);
		font-weight: var(--fw-semibold);
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.today-num {
		font-family: var(--font-mono);
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
	}

	.today-stat.success-tone { color: var(--success); }
	.today-stat.danger-tone { color: var(--danger); }

	/* Section spacing wrapper */
	.activity-section,
	.recent-section {
		margin-bottom: var(--space-6);
	}

	/* Activity */
	.activity-head {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-3);
		flex-wrap: wrap;
		margin-bottom: var(--space-3);
	}

	.activity-list {
		padding: var(--space-2);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.activity-row {
		position: relative;
		display: grid;
		grid-template-columns: 1fr auto auto;
		align-items: center;
		gap: var(--space-3);
		padding: 8px 12px;
		border-radius: var(--radius-sm);
		isolation: isolate;
	}

	.activity-bar {
		position: absolute;
		inset: 0;
		background: var(--accent-soft);
		border-radius: var(--radius-sm);
		transform-origin: left center;
		transform: scaleX(var(--ratio, 0));
		opacity: 0.5;
		z-index: -1;
		transition: transform 0.25s ease-out;
	}

	.activity-name {
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.activity-name.unassigned {
		font-style: italic;
		color: var(--text-secondary);
	}

	.activity-tasks {
		font-family: var(--font-mono);
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		min-width: 36px;
		text-align: right;
	}

	.activity-tasks::after {
		content: ' tasks';
		color: var(--text-tertiary);
	}

	.activity-cost {
		font-family: var(--font-mono);
		font-size: var(--fs-sm);
		font-weight: var(--fw-semibold);
		color: var(--accent);
		min-width: 64px;
		text-align: right;
	}

	/* Live working sheep grid (inside live-section hero) */
	.sheep-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
		gap: var(--space-3);
	}

	/* Recent Tasks */
	.recent-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.recent-item {
		display: flex;
		gap: var(--space-3);
		padding: var(--space-3) var(--space-4);
		text-decoration: none;
		color: inherit;
		transition: border-color 0.15s, background 0.15s, transform 0.05s;
	}

	.recent-item:hover {
		border-color: var(--accent);
		text-decoration: none;
		color: inherit;
	}

	.recent-item.failed {
		background:
			linear-gradient(90deg, var(--danger-soft), transparent 30%),
			var(--bg-2);
		border-left-color: var(--danger);
	}

	.recent-item.failed:hover {
		border-color: var(--danger);
	}

	.recent-icon {
		flex-shrink: 0;
		display: inline-flex;
		margin-top: 2px;
		color: var(--text-secondary);
	}

	.recent-icon[data-tone="success"] { color: var(--success); }
	.recent-icon[data-tone="live"] { color: var(--live); }
	.recent-icon[data-tone="danger"] { color: var(--danger); }
	.recent-icon[data-tone="warning"] { color: var(--warning); }

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
		font-size: var(--fs-sm);
		line-height: var(--lh-snug);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.recent-time {
		flex-shrink: 0;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
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

	/* Responsive */
	@media (max-width: 768px) {
		.dashboard {
			max-width: 100%;
			overflow-x: hidden;
		}

		.live-section {
			padding: var(--space-3);
			margin-bottom: var(--space-4);
		}

		.activity-stripe {
			gap: 6px;
			padding: 6px;
			margin-bottom: var(--space-4);
		}

		.stat-chip {
			padding: 4px 9px;
			font-size: var(--fs-xs);
		}

		.activity-head {
			gap: var(--space-2);
		}

		.activity-row {
			padding: 8px 10px;
			gap: var(--space-2);
		}

		.sheep-grid {
			grid-template-columns: 1fr;
		}

		.command-section {
			padding: var(--space-2) var(--space-3);
		}

		.recent-item {
			padding: var(--space-2) var(--space-3);
		}

		.recent-prompt {
			white-space: normal;
			display: -webkit-box;
			-webkit-line-clamp: 2;
			-webkit-box-orient: vertical;
		}

		.recent-error {
			white-space: normal;
		}
	}

	/* Titan 2 (≤480px square viewport) — vertical real-estate is precious */
	@media (max-width: 480px) {
		.command-section {
			margin-bottom: var(--space-3);
			padding: var(--space-2) var(--space-3);
		}

		.live-section {
			padding: var(--space-3);
			margin-bottom: var(--space-3);
		}

		.activity-stripe {
			/* Horizontal scroll keeps stripe one-row even when many chips */
			flex-wrap: nowrap;
			overflow-x: auto;
			padding: 6px;
			margin-bottom: var(--space-3);
			scrollbar-width: none;
		}

		.activity-stripe::-webkit-scrollbar {
			display: none;
		}

		.stat-chip {
			flex-shrink: 0;
		}

		.stripe-divider {
			flex-shrink: 0;
		}

		.today-stat {
			flex-shrink: 0;
			white-space: nowrap;
		}

		.activity-section,
		.recent-section {
			margin-bottom: var(--space-4);
		}

		.activity-row {
			padding: 6px 8px;
			gap: 8px;
		}

		.activity-name {
			font-size: var(--fs-xs);
		}

		.activity-tasks {
			min-width: 32px;
		}

		.activity-cost {
			font-size: var(--fs-xs);
			min-width: 56px;
		}

		.recent-item {
			padding: var(--space-2);
			gap: 8px;
		}
	}
</style>
