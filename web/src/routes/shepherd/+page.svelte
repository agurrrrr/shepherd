<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { appendLiveOutput } from '$lib/liveOutput.js';
	import { fmtCost } from '$lib/shepherdFormat.js';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import ShepherdConsole from '$lib/components/ShepherdConsole.svelte';
	import IssueDetailModal from '$lib/components/IssueDetailModal.svelte';
	import ShepherdTaskList from '$lib/components/ShepherdTaskList.svelte';
	import ShepherdPatrolLog from '$lib/components/ShepherdPatrolLog.svelte';
	import ShepherdIssueBoard from '$lib/components/ShepherdIssueBoard.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';

	// Shepherd (목자) console (#598). The page keeps only what must survive a
	// section tab switch in stage 2/4: shepherd status, the console with its
	// live output, and the single shared IssueDetailModal. Recent tasks, the
	// patrol log and the issue board (#599/#600) are split components that own
	// their data and SSE subscriptions (#621).

	let status = $state(null);      // GET /api/shepherd/status
	let loaded = $state(false);
	let unsubs = [];

	let liveOutput = $state([]);
	let liveOutputOpen = $state(false); // tracks an open (non-newline-terminated) tail line

	// Reload counters handed to the split components (first run is skipped by
	// each component's effect — onMount already loads).
	let taskRefreshKey = $state(0);
	let boardRefreshKey = $state(0);

	// Detail modal target. One modal on the page; the board and the attention
	// panel ask for it through onOpenIssue callbacks.
	let openIssue = $state(null);      // { projectName, issueId }

	// Autonomy tiers (#601). The daemon normalizes an unset/unknown value to
	// "execute", so status.autonomy_tier is always one of these four.
	const tierLabels = {
		report: '보고만',
		plan: '계획',
		execute: '실행',
		full: '전권'
	};

	async function loadStatus() {
		const res = await apiGet('/api/shepherd/status');
		if (res?.data) status = res.data;
	}

	onMount(async () => {
		await loadStatus();
		loaded = true;

		// Seed the live pane from the running task's stored output: GET
		// /api/tasks/:id merges the in-memory lines for running tasks (#8165),
		// so opening the page mid-run still shows what happened so far.
		if (status?.running_task_id) {
			const res = await apiGet(`/api/tasks/${status.running_task_id}`);
			if (res?.data?.output?.length) liveOutput = res.data.output;
		}

		// SSE: streaming output of the manager sheep. Output events carry
		// sheep_name but no task id, so "shepherd" + a running status is the
		// gate (the manager only ever runs one task at a time). Task/patrol/
		// board sections subscribe to their own events inside their components.
		unsubs.push(onSSE('output', (data) => {
			if (data.sheep_name !== 'shepherd') return;
			const st = { open: liveOutputOpen };
			liveOutput = appendLiveOutput(liveOutput, data.text, st, 5000);
			liveOutputOpen = st.open;
		}));

		unsubs.push(onSSE('task_start', (data) => {
			if (data.sheep_name === 'shepherd') {
				liveOutput = [`▶ ${data.prompt}`, ''];
				liveOutputOpen = false;
				loadStatus();
			}
		}));

		for (const ev of ['task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, (data) => {
				if (data.sheep_name === 'shepherd') loadStatus();
			}));
		}

		// The manager sheep is excluded from the sheep store / systemStatus, so
		// its status_change needs the dedicated refresh like the sidebar does.
		unsubs.push(onSSE('status_change', (data) => {
			if (data.sheep_name === 'shepherd') loadStatus();
		}));

		// Events missed while the SSE connection was down: re-read the status
		// (each component reloads its own data on reconnect).
		unsubs.push(onSSE('reconnect', () => loadStatus()));
	});

	onDestroy(() => {
		unsubs.forEach((fn) => fn?.());
	});

	let workspaceOk = $derived(status?.workspace_valid === true);
	let runningTaskId = $derived(status?.running_task_id ?? 0);
	let working = $derived(status?.status === 'working');
</script>

<div class="page">
	<PageHeader
		eyebrow="Watch · Orchestrator"
		title="Shepherd"
		subtitle="워크스페이스를 살피고 이슈로 일하는 목자에게 직접 지시를 보냅니다."
	>
		{#if status}
			<span class="head-status" data-tone={working ? 'live' : 'rest'}>
				{#if working}<span class="live-dot"></span>{/if}
				{working ? '목자 작업 중' : '목자 대기'}
			</span>
		{/if}
	</PageHeader>

	{#if status && !workspaceOk}
		<!-- Workspace unset or invalid: block the console, point at Settings → 목자. -->
		<div class="banner card" role="alert">
			<span class="banner-icon"><Icon name="alert-triangle" size={18} /></span>
			<div class="banner-text">
				<strong>워크스페이스가 지정되지 않았거나 유효하지 않습니다</strong>
				<p>{status.workspace_error || '목자가 일할 폴더를 먼저 정해야 지시를 보낼 수 있습니다.'}</p>
			</div>
			<a class="btn btn-sm btn-primary" href="/settings">설정에서 지정</a>
		</div>
	{/if}

	<!-- ============ Status card ============ -->
	<section class="block" aria-label="목자 상태">
		{#if !loaded}
			<div class="card status-card" aria-busy="true">
				<div class="skeleton" style="height: 18px; width: 30%"></div>
				<div class="skeleton" style="height: 64px; margin-top: 16px"></div>
			</div>
		{:else if status}
			<div class="card status-card">
				<div class="status-head">
					<span class="chip chip-mono">{status.provider}</span>
					{#if status.model}
						<span class="chip chip-mono" title="shepherd_model">{status.model}</span>
					{:else}
						<span class="chip chip-mono" title="프로바이더 기본 모델 사용">기본 모델</span>
					{/if}
					<StatusBadge status={working ? 'working' : 'idle'} />
					{#if status.circuit_tripped}
						<span class="chip chip-danger" title="연속 실패 {status.consecutive_failures}회 — 서킷 브레이커가 목자 작업을 멈췄습니다">
							서킷 브레이커 켜짐
						</span>
					{/if}
				</div>
				<dl class="status-grid">
					<div class="status-cell">
						<dt>워크스페이스</dt>
						<dd class="mono" title={status.workspace_path || ''}>
							{status.workspace_path || '미지정'}
							{#if !workspaceOk && status.workspace_error}
								<span class="status-warn">({status.workspace_error})</span>
							{/if}
						</dd>
					</div>
					<div class="status-cell">
						<dt>오늘 목자 작업</dt>
						<dd class="mono num">{status.today_task_count ?? 0}</dd>
					</div>
					<div class="status-cell">
						<dt>오늘 목자 비용</dt>
						<dd class="mono num" title="목자 작업(지시·검토·순찰)에만 든 비용">
							{fmtCost(status.today_cost_usd)}
						</dd>
					</div>
					<div class="status-cell">
						<dt>권한 단계</dt>
						<dd title="목자가 스스로 할 수 있는 범위 — settings에서 바꿉니다">
							<span class="chip chip-mono">{tierLabels[status.autonomy_tier] || status.autonomy_tier || '—'}</span>
						</dd>
					</div>
					<div class="status-cell">
						<dt>위임 상한</dt>
						<dd class="mono num" title="감시 이슈 작업의 동시 pending+running 수 / 상한">
							{#if status.max_active_delegations > 0 && status.active_delegations >= status.max_active_delegations}
								<span class="status-warn">{status.active_delegations ?? 0}/{status.max_active_delegations}</span>
							{:else}
								{status.active_delegations ?? 0}/{status.max_active_delegations ?? 0}
							{/if}
						</dd>
					</div>
					<div class="status-cell">
						<dt>하루 비용 상한</dt>
						<dd class="mono num" title="목자 작업 + 감시 이슈 작업의 합산 창(자정 기준)">
							{#if status.cost_limit_exceeded}
								<span class="status-warn">{fmtCost(status.today_cost)} / {fmtCost(status.daily_cost_limit_usd)} — 초과</span>
							{:else if status.daily_cost_limit_usd > 0}
								{fmtCost(status.today_cost)} / {fmtCost(status.daily_cost_limit_usd)}
							{:else}
								{fmtCost(status.today_cost)} · 제한 없음
							{/if}
						</dd>
					</div>
					<div class="status-cell">
						<dt>서킷 브레이커</dt>
						<dd>
							{#if status.circuit_tripped}
								<span class="status-warn mono num">연속 실패 {status.consecutive_failures}회</span>
							{:else}
								<span class="mono num">정상권</span>
							{/if}
						</dd>
					</div>
					<div class="status-cell">
						<dt>다음 순찰</dt>
						<dd class="mono num" title={status.patrol_interval ? '순찰 주기 ' + status.patrol_interval : ''}>
							{#if status.patrol_enabled}
								{(status.next_patrol_at || '').slice(5, 16) || '—'}
							{:else}
								<span class="status-off">꺼짐</span>
							{/if}
						</dd>
					</div>
				</dl>
			</div>
		{:else}
			<div class="card">
				<EmptyState icon="alert-triangle" title="목자 상태를 불러오지 못했습니다" description="데몬을 확인한 뒤 다시 시도하세요." />
			</div>
		{/if}
	</section>

	<!-- ============ Console + live output ============ -->
	<section class="block" aria-label="목자 콘솔">
		<SectionHeader index="01" eyebrow="Console" tone={working ? 'live' : 'default'}
			title={working ? `작업 #${runningTaskId} 실행 중` : '지시 보내기'}
			subtitle="Enter로 전송, Shift+Enter로 줄바꿈. 지시는 목자 작업으로 등록되어 바로 큐에 들어갑니다." />

		<ShepherdConsole
			disabled={!workspaceOk}
			disabledReason={status ? (status.workspace_error || '워크스페이스를 먼저 지정하세요') : '목자 상태를 불러오는 중'}
			working={working}
			runningTaskId={runningTaskId}
			injectSupported={status?.inject_supported ?? false}
			injectAvailable={status?.inject_available ?? false}
			onSent={() => { loadStatus(); taskRefreshKey += 1; }}
			onStopped={() => { loadStatus(); taskRefreshKey += 1; }}
			onInjected={() => loadStatus()}
		/>

		{#if liveOutput.length > 0}
			<div class="card output-card">
				<div class="output-head">
					<span class="eyebrow">Live output</span>
					<span class="label-count mono num">{liveOutput.length.toLocaleString()} lines</span>
				</div>
				<OutputViewer lines={liveOutput} maxHeight="420px" />
			</div>
		{/if}
	</section>

	<!-- ============ Split sections (#621) ============ -->
	<ShepherdTaskList refreshKey={taskRefreshKey} onRefresh={loadStatus} />

	<ShepherdPatrolLog onRan={() => { loadStatus(); taskRefreshKey += 1; }} />

	<ShepherdIssueBoard refreshKey={boardRefreshKey} onOpenIssue={(r) => (openIssue = r)} />

	{#if openIssue}
		<IssueDetailModal
			projectName={openIssue.projectName}
			issueId={openIssue.issueId}
			onClose={() => openIssue = null}
			onChanged={() => boardRefreshKey += 1}
		/>
	{/if}
</div>

<style>
	.page { max-width: var(--content-max); }

	.block { margin-bottom: var(--space-8); }

	/* Head chip */
	.head-status {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		padding: 6px 12px;
		border-radius: var(--radius-full);
		border: 1px solid var(--border-subtle);
		background: var(--bg-2);
	}

	/* Workspace warning banner */
	.banner {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: 14px 18px;
		margin-bottom: var(--space-5);
		border-color: var(--warning-line) !important;
		background: linear-gradient(90deg, var(--warning-soft), transparent 70%), var(--bg-2);
	}

	.banner-icon { color: var(--warning); flex-shrink: 0; }

	.banner-text { flex: 1; min-width: 0; }

	.banner-text strong {
		display: block;
		font-size: var(--fs-base);
		color: var(--text-primary);
	}

	.banner-text p { margin: 4px 0 0; font-size: var(--fs-sm); color: var(--text-secondary); }

	/* Status card */
	.status-card { padding: 18px 20px; }

	.status-head {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		margin-bottom: 16px;
	}

	.status-grid {
		display: grid;
		grid-template-columns: 2fr 1fr 1fr 1.4fr 1.2fr;
		gap: var(--space-4);
		margin: 0;
	}

	.status-cell dt {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		text-transform: uppercase;
		letter-spacing: var(--tracking-caps);
		color: var(--text-tertiary);
		margin-bottom: 6px;
	}

	.status-cell dd {
		margin: 0;
		font-size: var(--fs-base);
		color: var(--text-primary);
		overflow-wrap: anywhere;
	}

	.status-warn { color: var(--warning); font-size: var(--fs-sm); }

	.status-off { color: var(--text-tertiary); }

	@media (max-width: 720px) {
		.status-grid { grid-template-columns: 1fr 1fr; }
	}

	/* Output card */
	.output-card {
		margin-top: var(--space-4);
		padding: 14px 16px;
	}

	.output-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 10px;
	}

	.label-count { font-size: var(--fs-2xs); color: var(--text-tertiary); }
</style>
