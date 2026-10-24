<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { timeAgo, firstLine } from '$lib/shepherdFormat.js';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Icon from '$lib/components/Icon.svelte';

	// Recent shepherd tasks (#621, split out of routes/shepherd/+page.svelte).
	// Owns its data (GET /api/shepherd/tasks) and its SSE refreshes: shepherd
	// task_start / task_complete / task_fail / task_stop and a reconnect all
	// re-read the list. `refreshKey` lets the page force a reload (console
	// send/stop, patrol run); `onRefresh` lets the "새로고침" button also ask
	// the page to re-read the shepherd status.
	let { refreshKey = 0, onRefresh = () => {} } = $props();

	let tasks = $state([]);
	const kindLabels = { command: '지시', review: '검토', patrol: '순찰' };

	async function loadTasks() {
		const res = await apiGet('/api/shepherd/tasks?limit=20');
		if (res?.data) tasks = res.data.tasks || [];
	}

	let unsubs = [];

	onMount(() => {
		loadTasks();

		// task_start reloads the list regardless of which sheep started — the
		// page did the same before the split, and a patrol wake reaches here
		// through this event rather than an explicit call.
		unsubs.push(onSSE('task_start', () => loadTasks()));
		for (const ev of ['task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, (data) => {
				if (data.sheep_name === 'shepherd') loadTasks();
			}));
		}
		unsubs.push(onSSE('reconnect', () => loadTasks()));
	});

	onDestroy(() => {
		unsubs.forEach((fn) => fn?.());
	});

	// Page-requested reloads (console send/stop, patrol wake). The mount load
	// above already covers the first render, so skip the effect's first run.
	let first = true;
	$effect(() => {
		refreshKey;
		if (first) {
			first = false;
			return;
		}
		loadTasks();
	});
</script>

<section class="block" aria-label="최근 목자 작업">
	<SectionHeader index="02" eyebrow="Log" title="최근 목자 작업" subtitle="지시·검토·순찰로 만든 작업 (최신 20개)">
		<button class="btn btn-sm btn-ghost" onclick={async () => { await loadTasks(); onRefresh(); }}>
			<Icon name="refresh-cw" size={13} /> 새로고침
		</button>
	</SectionHeader>

	{#if tasks.length === 0}
		<div class="card">
			<EmptyState icon="inbox" title="아직 목자 작업이 없습니다" description="위 콘솔에서 지시를 보내면 여기에 기록됩니다." />
		</div>
	{:else}
		<div class="task-table">
			{#each tasks as t, i (t.id)}
				<a class="task-row reveal" style="--i: {i}" href="/tasks/{t.id}" aria-label={`목자 작업 #${t.id}`}>
					<span class="row-kind chip chip-{t.shepherd_kind === 'command' ? 'accent' : t.shepherd_kind === 'review' ? 'queued' : 'info'}">
						{kindLabels[t.shepherd_kind] || t.shepherd_kind}
					</span>
					<span class="row-id mono num">#{t.id}</span>
					<div class="row-body">
						<span class="row-text">{firstLine(t.summary || t.prompt || t.error)}</span>
					</div>
					<StatusBadge status={t.status} />
					<span class="row-time mono num" title={t.created_at}>{timeAgo(t.created_at)}</span>
				</a>
			{/each}
		</div>
	{/if}
</section>

<style>
	/* Section spacing — the page's .block rule is scoped, so each split
	   section carries its own copy (#621). */
	.block { margin-bottom: var(--space-8); }

	/* Recent tasks */
	.task-table {
		display: flex;
		flex-direction: column;
		padding: 4px;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-2);
		box-shadow: var(--sheen);
	}

	.task-row {
		display: grid;
		grid-template-columns: 52px 54px minmax(0, 1fr) auto 72px;
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		border-radius: var(--radius);
		color: var(--text-secondary);
		text-decoration: none;
		transition: background var(--dur-1) ease;
	}

	.task-row + .task-row { box-shadow: 0 -1px 0 var(--border-subtle); }

	.task-row:hover,
	.task-row:focus-visible {
		background: rgba(237, 230, 218, 0.035);
		text-decoration: none;
		color: var(--text-secondary);
	}

	.row-kind { white-space: nowrap; }

	.row-id { color: var(--text-tertiary); font-size: var(--fs-sm); }

	.row-body { min-width: 0; }

	.row-text {
		display: block;
		font-size: var(--fs-base);
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.row-time { font-size: var(--fs-xs); color: var(--text-tertiary); text-align: right; }

	@media (max-width: 640px) {
		.task-row { grid-template-columns: 48px minmax(0, 1fr) auto; }
		.row-id, .row-time { display: none; }
	}
</style>
