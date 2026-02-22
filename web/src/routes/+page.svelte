<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { sheep, systemStatus } from '$lib/stores.js';
	import { onSSE } from '$lib/sse.js';
	import SheepCard from '$lib/components/SheepCard.svelte';
	import CommandInput from '$lib/components/CommandInput.svelte';

	let loaded = false;
	let unsubscribers = [];

	onMount(async () => {
		await refreshData();
		loaded = true;

		unsubscribers.push(
			onSSE('status_change', () => refreshSheep()),
			onSSE('task_complete', () => refreshData()),
			onSSE('task_start', () => refreshData()),
			onSSE('task_fail', () => refreshData())
		);
	});

	onDestroy(() => {
		unsubscribers.forEach(fn => fn?.());
	});

	async function refreshData() {
		const [statusRes, sheepRes] = await Promise.all([
			apiGet('/api/system/status'),
			apiGet('/api/sheep')
		]);
		if (statusRes?.data) systemStatus.set(statusRes.data);
		if (sheepRes?.data) sheep.set(sheepRes.data);
	}

	async function refreshSheep() {
		const res = await apiGet('/api/sheep');
		if (res?.data) sheep.set(res.data);
	}
</script>

<div class="dashboard">
	<!-- Command Input -->
	<div class="command-section card">
		<CommandInput />
	</div>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else}
		<!-- Stats Overview -->
		{#if $systemStatus}
			<div class="stats-grid">
				<div class="card stat-card">
					<div class="stat-label">Sheep</div>
					<div class="stat-number">{$systemStatus.sheep?.total ?? 0}</div>
					<div class="stat-detail">
						<span class="badge badge-working">{$systemStatus.sheep?.working ?? 0} working</span>
						<span class="badge badge-idle">{$systemStatus.sheep?.idle ?? 0} idle</span>
					</div>
				</div>

				<div class="card stat-card">
					<div class="stat-label">Projects</div>
					<div class="stat-number">{$systemStatus.projects ?? 0}</div>
				</div>

				<div class="card stat-card">
					<div class="stat-label">Running</div>
					<div class="stat-number accent">{$systemStatus.tasks?.running ?? 0}</div>
					<div class="stat-detail">
						<span class="badge badge-pending">{$systemStatus.tasks?.pending ?? 0} pending</span>
					</div>
				</div>

				<div class="card stat-card">
					<div class="stat-label">Completed</div>
					<div class="stat-number success">{$systemStatus.tasks?.completed ?? 0}</div>
					<div class="stat-detail">
						<span class="badge badge-failed">{$systemStatus.tasks?.failed ?? 0} failed</span>
					</div>
				</div>
			</div>
		{/if}

		<!-- Working projects only -->
		{@const working = $sheep.filter(s => s.status === 'working' && s.project)}
		{#if working.length > 0}
			<h2 class="section-title">Working</h2>
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
		{:else}
			<div class="card empty-state">
				<p>No active tasks.</p>
				<p class="text-muted">All sheep are idle.</p>
			</div>
		{/if}
	{/if}
</div>

<style>
	.dashboard {
		max-width: 1200px;
	}

	.command-section {
		margin-bottom: 24px;
		padding: 12px 16px;
	}

	.text-muted {
		color: var(--text-secondary);
	}

	.stats-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
		gap: 12px;
		margin-bottom: 32px;
	}

	.stat-card {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.stat-label {
		font-size: 12px;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.stat-number {
		font-size: 28px;
		font-weight: 700;
		font-family: var(--font-mono);
		line-height: 1.2;
	}

	.stat-number.accent { color: var(--accent); }
	.stat-number.success { color: var(--success); }

	.stat-detail {
		display: flex;
		gap: 6px;
		flex-wrap: wrap;
		margin-top: 4px;
	}

	.section-title {
		font-size: 16px;
		font-weight: 600;
		margin-bottom: 12px;
	}

	.sheep-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
		gap: 12px;
	}

	.empty-state {
		text-align: center;
		padding: 40px;
		color: var(--text-secondary);
	}

	.empty-state code {
		background: var(--bg-tertiary);
		padding: 2px 6px;
		border-radius: 4px;
		font-family: var(--font-mono);
		font-size: 13px;
		color: var(--accent);
	}

	@media (max-width: 768px) {
		.stats-grid {
			grid-template-columns: repeat(2, 1fr);
			gap: 8px;
		}

		.stat-number {
			font-size: 22px;
		}

		.sheep-grid {
			grid-template-columns: 1fr;
		}

		.command-section {
			padding: 10px 12px;
		}
	}
</style>
