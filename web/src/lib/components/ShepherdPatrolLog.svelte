<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { timeAgo } from '$lib/shepherdFormat.js';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Icon from '$lib/components/Icon.svelte';

	// Patrol log (#621 split; #623 polish). Records come from
	// GET /api/shepherd/patrols — every pass (auto or manual) stores one: when
	// the code looked, how many findings were new, which shepherd task it woke
	// (+ that task's first summary line), and why a wake was skipped.
	// POST /api/shepherd/patrols/run is the "지금 순찰" button — same rules as
	// the ticker pass, so it can legitimately report a skip.
	//
	// #623: the tab starts at 30 records and grows by 30 per "더 보기" click;
	// the endpoint caps at 100 (handlers_patrol.go), and rows older than 30
	// days are pruned by the pass itself, so the cap gets an honest caption.
	// A row expands in place to list its findings (kind/severity/title/message,
	// plus a board link when the finding carries a project) — the old one-line
	// join of titles was always cut off.
	let { refreshKey = 0, onRan = () => {}, status = null } = $props();

	const PAGE_STEP = 30;
	const API_CAP = 100; // handlers_patrol.go clamps limit to 100

	let patrols = $state([]);
	let patrolLimit = $state(PAGE_STEP);
	let patrolLoaded = $state(false);
	let patrolBusy = $state(false);
	let patrolMsg = $state('');
	let expandedId = $state(null);

	const severityLabels = { 0: '낮음', 1: '낮음', 2: '보통', 3: '보통', 4: '높음', 5: '높음' };

	function severityChipClass(sev) {
		if (sev >= 5) return 'chip-danger';
		if (sev >= 3) return 'chip-accent';
		return 'chip-info';
	}

	async function loadPatrols() {
		const res = await apiGet(`/api/shepherd/patrols?limit=${patrolLimit}`);
		if (res?.data) patrols = res.data.patrols || [];
		patrolLoaded = true;
	}

	function loadMore() {
		patrolLimit = Math.min(patrolLimit + PAGE_STEP, API_CAP);
		loadPatrols();
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

	function toggleRow(id) {
		expandedId = expandedId === id ? null : id;
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

<section class="patrol-tab" aria-label="순찰 기록">
	<!-- One line: patrol cadence / next pass on the left, actions on the right. -->
	<div class="patrol-bar">
		{#if status?.patrol_enabled}
			<span class="cadence-chip chip mono" title="다음 자동 순찰 시각">
				순찰 주기 {status.patrol_interval || '—'} · 다음 순찰 {(status.next_patrol_at || '').slice(5, 16) || '—'}
			</span>
		{:else if status}
			<span class="cadence-chip chip cadence-off">
				순찰이 꺼져 있습니다. <a class="cadence-link" href="/settings">설정 → 목자</a>에서 켤 수 있습니다.
			</span>
		{:else}
			<span class="skeleton cadence-skeleton" aria-hidden="true"></span>
		{/if}

		{#if patrolMsg}
			<span class="patrol-msg" role="status">{patrolMsg}</span>
		{/if}

		<span class="patrol-bar-spacer"></span>

		<button class="btn btn-sm btn-ghost" onclick={runPatrolNow} disabled={patrolBusy} aria-label="지금 순찰">
			<Icon name={patrolBusy ? 'refresh-cw' : 'search'} size={13} /> {patrolBusy ? '순찰 중…' : '지금 순찰'}
		</button>
		<button class="btn btn-sm btn-ghost" onclick={loadPatrols} aria-label="새로고침">
			<Icon name="refresh-cw" size={13} />
		</button>
	</div>

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
				{@const findings = p.findings || []}
				<div class="patrol-entry reveal" style="--i: {i}">
					<!-- svelte-ignore a11y_click_events_have_key_events -->
					<div class="patrol-row" role="button" tabindex="0"
						aria-expanded={expandedId === p.id}
						onclick={() => toggleRow(p.id)}
						onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleRow(p.id); } }}>
						<span class="patrol-caret" aria-hidden="true">
							<Icon name={expandedId === p.id ? 'chevron-down' : 'chevron-right'} size={13} />
						</span>
						<span class="patrol-time mono num" title={p.created_at}>{timeAgo(p.created_at)}</span>
						<span class="patrol-new chip {p.new_count > 0 ? 'chip-danger' : 'chip-info'} mono num"
							title="{findings.length}개 항목 점검">새 이상 {p.new_count}</span>
						<div class="patrol-body">
							{#if p.task_id}
								<a class="patrol-task-link" href="/tasks/{p.task_id}?from=shepherd&tab=patrols"
									onclick={(e) => e.stopPropagation()}>
									목자 순찰 작업 #{p.task_id}
									{#if p.task_summary}<span class="patrol-summary">{p.task_summary}</span>{/if}
								</a>
							{:else if p.skipped_reason}
								<span class="patrol-skip" title={p.skipped_reason}>건너뜀 — {p.skipped_reason}</span>
							{:else if findings.length > 0}
								<span class="patrol-items">{findings.length}개 점검 항목 — 행을 눌러 보세요</span>
							{:else}
								<span class="patrol-none">확인할 항목 없음</span>
							{/if}
						</div>
					</div>

					{#if expandedId === p.id}
						{#if findings.length > 0}
							<ul class="finding-list">
								{#each findings as f, fi (f.fingerprint || fi)}
									<li class="finding sev-{Math.min(f.severity ?? 0, 7)}">
										<span class="finding-sev chip {severityChipClass(f.severity ?? 0)} mono num"
											title="심각도 {f.severity}">{severityLabels[f.severity] ?? ('sev ' + f.severity)}</span>
										<div class="finding-body">
											<strong class="finding-title">{f.title}</strong>
											{#if f.message}<p class="finding-message">{f.message}</p>{/if}
										</div>
										{#if f.project}
											<!-- The finding carries only a project name (PatrolFinding), so the
											     link opens the board tab with that project filter — no issue id. -->
											<a class="finding-project chip" href="/shepherd?tab=board&project={encodeURIComponent(f.project)}"
												title="이슈 보드에서 {f.project} 보기">{f.project}<Icon name="arrow-up-right" size={11} /></a>
										{/if}
									</li>
								{/each}
							</ul>
						{:else}
							<p class="finding-empty">이 순찰은 점검 항목을 기록하지 않았습니다.</p>
						{/if}
					{/if}
				</div>
			{/each}
		</div>

		{#if patrols.length >= patrolLimit && patrolLimit < API_CAP}
			<button class="btn btn-sm btn-ghost more" onclick={loadMore}>
				더 보기 (지금 {patrols.length}개까지)
			</button>
		{:else if patrols.length >= API_CAP}
			<p class="patrol-cap">최근 100개까지만 보여 줍니다. 순찰 기록은 30일이 지나면 지워집니다.</p>
		{/if}
	{/if}
</section>

<style>
	.patrol-tab {
		display: flex;
		flex-direction: column;
		min-height: 0;
	}

	/* ---------- Top bar: cadence + actions ---------- */
	.patrol-bar {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		margin-bottom: 12px;
	}

	.cadence-chip {
		font-size: var(--fs-2xs);
		background: var(--well);
		color: var(--text-secondary);
	}

	.cadence-link { color: var(--accent); text-decoration: none; }
	.cadence-link:hover { text-decoration: underline; }

	.cadence-off {
		color: var(--warning);
	}

	.cadence-skeleton {
		display: inline-block;
		width: 220px;
		height: 22px;
		border-radius: 0;
	}

	.patrol-msg {
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		margin-right: 4px;
	}

	.patrol-bar-spacer { margin-left: auto; }

	/* ---------- Rows ---------- */
	.patrol-table {
		display: flex;
		flex-direction: column;
		padding: 4px;
		border: 0;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel);
	}

	.patrol-entry + .patrol-entry { box-shadow: 0 -2px 0 0 var(--lo); }

	.patrol-row {
		display: grid;
		grid-template-columns: 18px 72px 88px minmax(0, 1fr);
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		border-radius: 0;
		cursor: pointer;
	}

	.patrol-row:hover,
	.patrol-row:focus-visible { background: var(--bg-3); }

	.patrol-caret { color: var(--text-tertiary); display: inline-flex; }

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

	/* ---------- Expanded findings ---------- */
	.finding-list {
		list-style: none;
		margin: 0;
		padding: 4px 12px 12px 42px;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.finding {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		align-items: start;
		gap: 10px;
		padding: 8px 10px;
		border: 0;
		border-radius: 0;
		background: var(--bg-3);
		box-shadow: inset 0 0 0 2px #000;
	}

	.finding.sev-5, .finding.sev-6, .finding.sev-7 { box-shadow: inset 0 0 0 2px var(--danger); }

	.finding-sev { font-size: var(--fs-2xs); white-space: nowrap; }

	.finding-body { min-width: 0; }

	.finding-title { font-size: var(--fs-sm); color: var(--text-primary); font-weight: var(--fw-medium); }

	.finding-message {
		margin: 4px 0 0;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		overflow-wrap: anywhere;
	}

	.finding-project {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		max-width: 150px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--fs-2xs);
		text-decoration: none;
		color: var(--text-secondary);
		border: 1px solid var(--border-subtle);
		background: var(--well);
	}

	.finding-project:hover { border-color: var(--border-strong); color: var(--text-primary); text-decoration: none; }

	.finding-empty {
		margin: 0;
		padding: 0 12px 12px 42px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.more { align-self: flex-start; margin-top: 10px; }

	.patrol-cap {
		margin: 10px 0 0;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	@media (max-width: 640px) {
		.patrol-row { grid-template-columns: 18px minmax(0, 1fr); }
		.patrol-time { grid-column: 2; }
		.patrol-new { grid-row: 2; grid-column: 2; justify-self: start; }
		.patrol-body { grid-column: 2; }
		.finding-list { padding-left: 12px; }
		.finding { grid-template-columns: auto minmax(0, 1fr); }
		.finding-project { grid-column: 2; justify-self: start; }
	}
</style>
