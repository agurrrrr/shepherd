<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { timeAgo } from '$lib/shepherdFormat.js';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Icon from '$lib/components/Icon.svelte';

	// Patrol log (#621, split out of routes/shepherd/+page.svelte; records
	// from #600). GET /api/shepherd/patrols — every pass (auto or manual)
	// stores a record: when the code looked, how many findings were new, which
	// shepherd task it woke (+ that task's first summary line), and why a wake
	// was skipped. POST /api/shepherd/patrols/run is the "지금 순찰" button —
	// same rules as the ticker pass, so it can legitimately report a skip.
	//
	// Owns its data and SSE subscription: an auto pass may store a record, so
	// the debounced board-refresh events (task_start / task_complete / ... /
	// reconnect) reload the list here. `refreshKey` lets the page force a
	// reload; `onRan` tells the page to re-read the shepherd status after a
	// manual run (the task list refreshes itself via the task_start SSE).
	let { refreshKey = 0, onRan = () => {} } = $props();

	let patrols = $state([]);
	let patrolLoaded = $state(false);
	let patrolBusy = $state(false);
	let patrolMsg = $state('');

	async function loadPatrols() {
		const res = await apiGet('/api/shepherd/patrols?limit=10');
		if (res?.data) patrols = res.data.patrols || [];
		patrolLoaded = true;
	}

	async function runPatrolNow() {
		if (patrolBusy) return;
		patrolBusy = true;
		patrolMsg = '';
		try {
			const res = await apiPost('/api/shepherd/patrols/run', {});
			if (!res?.success) {
				patrolMsg = '순찰 실행에 실패했습니다: ' + (res?.message || '');
			} else {
				const rec = res.data?.patrol || {};
				if (rec.task_id) patrolMsg = `새 이상 ${rec.new_count}개 — 목자 작업 #${rec.task_id} 적재`;
				else if (rec.skipped_reason) patrolMsg = `기록됨 — 깨우기 건너뜀: ${rec.skipped_reason}`;
				else if (rec.new_count > 0) patrolMsg = `새 이상 ${rec.new_count}개 기록 (목자 미호출)`;
				else patrolMsg = '이상 없음 — 기록만 남겼습니다';
				await loadPatrols();
				onRan(); // the page re-reads /api/shepherd/status
			}
		} catch (e) {
			patrolMsg = '순찰 실행에 실패했습니다: ' + (e?.message || e);
		} finally {
			patrolBusy = false;
			setTimeout(() => (patrolMsg = ''), 6000);
		}
	}

	let unsubs = [];
	let sseTimer = null;

	onMount(() => {
		loadPatrols();

		// Debounce like the board: SSE events arrive in bursts, and an auto
		// pass may have stored a record worth re-reading once.
		const schedule = () => {
			clearTimeout(sseTimer);
			sseTimer = setTimeout(loadPatrols, 600);
		};
		for (const ev of ['task_start', 'task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, schedule));
		}
		unsubs.push(onSSE('reconnect', schedule));
	});

	onDestroy(() => {
		unsubs.forEach((fn) => fn?.());
		clearTimeout(sseTimer);
	});

	let first = true;
	$effect(() => {
		refreshKey;
		if (first) {
			first = false;
			return;
		}
		loadPatrols();
	});
</script>

<section class="block" aria-label="순찰 기록">
	<SectionHeader index="03" eyebrow="Patrol" title="순찰 기록"
		subtitle="코드가 관리 범위를 점검한 결과입니다. 새 이상이 있을 때만 목자 순찰 작업을 만듭니다.">
		{#if patrolMsg}
			<span class="patrol-msg" role="status">{patrolMsg}</span>
		{/if}
		<button class="btn btn-sm btn-ghost" onclick={runPatrolNow} disabled={patrolBusy} aria-label="지금 순찰">
			<Icon name={patrolBusy ? 'refresh-cw' : 'search'} size={13} /> {patrolBusy ? '순찰 중…' : '지금 순찰'}
		</button>
		<button class="btn btn-sm btn-ghost" onclick={loadPatrols} aria-label="새로고침">
			<Icon name="refresh-cw" size={13} />
		</button>
	</SectionHeader>

	{#if !patrolLoaded}
		<div class="card" aria-busy="true">
			<div class="skeleton" style="height: 48px"></div>
			<div class="skeleton" style="height: 48px; margin-top: 8px"></div>
		</div>
	{:else if patrols.length === 0}
		<div class="card">
			<EmptyState icon="search" title="아직 순찰 기록이 없습니다"
				description="순찰 주기가 켜지면 정해진 주기마다, 아니면 오른쪽 버튼으로 지금 점검할 수 있습니다." />
		</div>
	{:else}
		<div class="patrol-table">
			{#each patrols as p, i (p.id)}
				<div class="patrol-row reveal" style="--i: {i}">
					<span class="patrol-time mono num" title={p.created_at}>{timeAgo(p.created_at)}</span>
					<span class="patrol-new chip {p.new_count > 0 ? 'chip-danger' : 'chip-info'} mono num"
						title="{(p.findings || []).length}개 항목 점검">새 이상 {p.new_count}</span>
					<div class="patrol-body">
						{#if p.task_id}
							<a class="patrol-task-link" href="/tasks/{p.task_id}">
								목자 순찰 작업 #{p.task_id}
								{#if p.task_summary}<span class="patrol-summary">{p.task_summary}</span>{/if}
							</a>
						{:else if p.skipped_reason}
							<span class="patrol-skip" title={p.skipped_reason}>건너뜀 — {p.skipped_reason}</span>
						{:else if (p.findings || []).length > 0}
							<span class="patrol-items">{(p.findings || []).map((f) => f.title).join(' · ')}</span>
						{:else}
							<span class="patrol-none">확인할 항목 없음</span>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}
</section>

<style>
	/* ===== Patrol log (#600) ===== */
	.patrol-msg {
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		margin-right: 4px;
	}

	.patrol-table {
		display: flex;
		flex-direction: column;
		padding: 4px;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-2);
		box-shadow: var(--sheen);
	}

	.patrol-row {
		display: grid;
		grid-template-columns: 72px 88px minmax(0, 1fr);
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		border-radius: var(--radius);
	}

	.patrol-row + .patrol-row { box-shadow: 0 -1px 0 var(--border-subtle); }

	.patrol-time { font-size: var(--fs-xs); color: var(--text-tertiary); }

	.patrol-new { white-space: nowrap; font-size: var(--fs-2xs); justify-self: start; }

	.patrol-body { min-width: 0; font-size: var(--fs-sm); }

	.patrol-task-link {
		color: var(--text-primary);
		text-decoration: none;
		display: inline-flex;
		gap: 8px;
		align-items: baseline;
		min-width: 0;
	}

	.patrol-task-link:hover { text-decoration: underline; }

	.patrol-summary {
		color: var(--text-secondary);
		font-size: var(--fs-xs);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		min-width: 0;
	}

	.patrol-skip { color: var(--warning); font-size: var(--fs-xs); }

	.patrol-items {
		color: var(--text-secondary);
		font-size: var(--fs-xs);
		display: block;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.patrol-none { color: var(--text-tertiary); font-size: var(--fs-xs); }

	@media (max-width: 640px) {
		.patrol-row { grid-template-columns: 64px minmax(0, 1fr); }
		.patrol-new { grid-row: 2; grid-column: 2; justify-self: start; }
		.patrol-body { grid-column: 2; }
	}
</style>
