<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { appendLiveOutput } from '$lib/liveOutput.js';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import ShepherdConsole from '$lib/components/ShepherdConsole.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';

	// Shepherd (목자) console (#598). Sections in order: workspace warning,
	// status card, console + live output, recent shepherd tasks. The issue
	// board / needs-attention list lands in stage #7 and patrol log in #8 —
	// their slots belong below this section order.

	let status = $state(null);      // GET /api/shepherd/status
	let tasks = $state([]);         // GET /api/shepherd/tasks
	let loaded = $state(false);
	let unsubs = [];

	let liveOutput = $state([]);
	let liveOutputOpen = $state(false); // tracks an open (non-newline-terminated) tail line

	const kindLabels = { command: '지시', review: '검토', patrol: '순찰' };

	async function loadStatus() {
		const res = await apiGet('/api/shepherd/status');
		if (res?.data) status = res.data;
	}

	async function loadTasks() {
		const res = await apiGet('/api/shepherd/tasks?limit=20');
		if (res?.data) tasks = res.data.tasks || [];
	}

	onMount(async () => {
		await Promise.all([loadStatus(), loadTasks()]);
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
		// gate (the manager only ever runs one task at a time).
		unsubs.push(onSSE('output', (data) => {
			if (data.sheep_name !== 'shepherd') return;
			const st = { open: liveOutputOpen };
			liveOutput = appendLiveOutput(liveOutput, data.text, st, 5000);
			liveOutputOpen = st.open;
		}));

		unsubs.push(onSSE('task_start', (data) => {
			if (data.sheep_name !== 'shepherd') return;
			liveOutput = [`▶ ${data.prompt}`, ''];
			liveOutputOpen = false;
			loadStatus();
			loadTasks();
		}));

		for (const ev of ['task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, (data) => {
				if (data.sheep_name !== 'shepherd') return;
				loadStatus();
				loadTasks();
			}));
		}

		// The manager sheep is excluded from the sheep store / systemStatus, so
		// its status_change needs the dedicated refresh like the sidebar does.
		unsubs.push(onSSE('status_change', (data) => {
			if (data.sheep_name === 'shepherd') loadStatus();
		}));

		// Events missed while the SSE connection was down: re-read everything.
		unsubs.push(onSSE('reconnect', () => { loadStatus(); loadTasks(); }));
	});

	onDestroy(() => unsubs.forEach(fn => fn?.()));

	function firstLine(text) {
		if (!text) return '';
		const line = String(text).trim().split('\n')[0];
		const runes = [...line];
		return runes.length > 120 ? runes.slice(0, 120).join('') + '…' : line;
	}

	function timeAgo(dateStr) {
		if (!dateStr) return '';
		const date = new Date(String(dateStr).replace(' ', 'T'));
		const diff = Math.floor((Date.now() - date.getTime()) / 1000);
		if (diff < 60) return '방금';
		if (diff < 3600) return `${Math.floor(diff / 60)}분 전`;
		if (diff < 86400) return `${Math.floor(diff / 3600)}시간 전`;
		return `${Math.floor(diff / 86400)}일 전`;
	}

	function fmtCost(n) {
		const v = Number(n) || 0;
		return v >= 100 ? `$${Math.round(v).toLocaleString()}` : `$${v.toFixed(2)}`;
	}

	let workspaceOk = $derived(status?.workspace_valid === true);
	let runningTaskId = $derived(status?.running_task_id ?? 0);
	let working = $derived(status?.status === 'working');

	async function retryWorkspaceCheck() {
		await loadStatus();
	}
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
						<dt>오늘 비용</dt>
						<dd class="mono num">{fmtCost(status.today_cost_usd)}</dd>
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
			onSent={() => { loadStatus(); loadTasks(); }}
			onStopped={() => { loadStatus(); loadTasks(); }}
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

	<!-- ============ Recent shepherd tasks ============ -->
	<section class="block" aria-label="최근 목자 작업">
		<SectionHeader index="02" eyebrow="Log" title="최근 목자 작업" subtitle="지시·검토·순찰로 만든 작업 (최신 20개)">
			<button class="btn btn-sm btn-ghost" onclick={async () => { await Promise.all([loadStatus(), loadTasks()]); }}>
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
		grid-template-columns: 2fr 1fr 1fr 1.4fr;
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
