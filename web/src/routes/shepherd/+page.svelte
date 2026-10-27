<script>
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { appendLiveOutput } from '$lib/liveOutput.js';
	import { fmtCost } from '$lib/shepherdFormat.js';
	import { shepherdAttention } from '$lib/stores.js';
	import PageTabs from '$lib/components/PageTabs.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import MagiStreamPanel from '$lib/components/MagiStreamPanel.svelte';
	import SubagentStreamPanel from '$lib/components/SubagentStreamPanel.svelte';
	import ShepherdConsole from '$lib/components/ShepherdConsole.svelte';
	import AttentionPanel from '$lib/components/AttentionPanel.svelte';
	import IssueDetailModal from '$lib/components/IssueDetailModal.svelte';
	import ShepherdTaskList from '$lib/components/ShepherdTaskList.svelte';
	import ShepherdPatrolLog from '$lib/components/ShepherdPatrolLog.svelte';
	import ShepherdIssueBoard from '$lib/components/ShepherdIssueBoard.svelte';
	import Icon from '$lib/components/Icon.svelte';

	// Shepherd (목자) console (#598, restructured #622). Full-height app page
	// like the project workspace: header + one-line status chips + section tabs
	// + tab content that fills the remaining height + the instruction input
	// pinned at the bottom (Live Output tab only). Vertical scrolling happens
	// inside each tab pane only.
	//
	// The page keeps what must survive a tab switch: shepherd status, the live
	// output (SSE `output` events carry no task id, so the subscription lives
	// here), the console, and the single shared IssueDetailModal. The tab
	// sections (#621 components) own their data and SSE subscriptions and are
	// mounted with {#if} — an unmounted tab sends no requests.

	const SHEPHERD_TABS = [
		{ id: 'output', label: 'Live Output', icon: 'terminal' },
		{ id: 'history', label: '목자 작업', icon: 'clock' },
		{ id: 'patrols', label: '순찰 기록', icon: 'search' },
		{ id: 'board', label: '이슈 보드', icon: 'list-checks' },
		{ id: 'attention', label: '확인 필요', icon: 'alert-triangle' }
	];
	const VALID_TABS = SHEPHERD_TABS.map((t) => t.id);

	let activeTab = $state('output');

	let status = $state(null);      // GET /api/shepherd/status
	let loaded = $state(false);
	let unsubs = [];

	let liveOutput = $state([]);
	let liveOutputOpen = $state(false); // tracks an open (non-newline-terminated) tail line
	let recentTask = $state(null);      // most recent shepherd task (empty-output link)

	// Reload counters handed to the split components (first run is skipped by
	// each component's effect — onMount already loads).
	let taskRefreshKey = $state(0);
	let boardRefreshKey = $state(0);

	// Attention panel refresh counter. The panel lives in the `attention` tab;
	// bumps while it is unmounted are inert (no request leaves the page).
	let attentionTick = $state(0);
	let attentionTimer = null;
	function scheduleAttentionRefresh() {
		clearTimeout(attentionTimer);
		attentionTimer = setTimeout(() => { attentionTick += 1; }, 600);
	}

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
		try {
			const res = await apiGet('/api/shepherd/status');
			if (res?.data) status = res.data;
		} catch {
			/* keep the last-known status; the status row shows the retry line */
		}
	}

	// --- Tab <-> URL (#622) --------------------------------------------------
	// The tab lives in ?tab=. Unknown values fall back to output. Switching
	// tabs rewrites the query with replaceState (no back-history per tab, same
	// as the project screen) while KEEPING the board filter keys (project,
	// type, watched, q) — dropping them would reset the board on reload.
	function tabFromUrl() {
		const t = $page.url.searchParams.get('tab');
		return VALID_TABS.includes(t) ? t : 'output';
	}

	function switchTab(tab) {
		if (!VALID_TABS.includes(tab)) tab = 'output';
		activeTab = tab;
		const params = new URLSearchParams($page.url.searchParams);
		if (tab === 'output') params.delete('tab'); // default tab needs no param
		else params.set('tab', tab);
		const qs = params.toString();
		goto(qs ? `?${qs}` : $page.url.pathname, { replaceState: true, noScroll: true, keepFocus: true });
	}

	// Query-only navigations (e.g. a sidebar badge link to ?tab=attention while
	// already on /shepherd) must move the visible tab too. Track the last URL
	// tab WE have seen: comparing against activeTab instead made this effect
	// depend on activeTab, and a switch back to the default (output) tab —
	// which deletes ?tab= — was reverted by the still-stale URL (#620).
	let lastUrlTab = null;
	$effect(() => {
		const t = $page.url.searchParams.get('tab');
		if (t === lastUrlTab) return;
		lastUrlTab = t;
		activeTab = VALID_TABS.includes(t) ? t : 'output';
	});

	let workspaceOk = $derived(status?.workspace_valid === true);
	let runningTaskId = $derived(status?.running_task_id ?? 0);
	let working = $derived(status?.status === 'working');
	let delegationAtCap = $derived(
		(status?.max_active_delegations ?? 0) > 0 &&
		(status?.active_delegations ?? 0) >= (status?.max_active_delegations ?? 0)
	);
	// Tab list for the shared PageTabs bar: live dot on Live Output while the
	// manager is working, and the 확인 필요 count badge (#623) reads the same
	// shepherdAttention store as the sidebar badge, so both numbers always
	// match (attention.js assembles items + pending requests in one place).
	let pageTabs = $derived(
		SHEPHERD_TABS.map((t) => ({
			...t,
			live: t.id === 'output' && working,
			badge: t.id === 'attention' ? $shepherdAttention : 0
		}))
	);

	// MAGI provider -> slot panel; [SUB:...] demux lines -> subagent panel.
	// Same routing as the project screen (#7575): without it OutputViewer
	// treats every prefixed 🔧/💭 line as plain text (markdown wall). The
	// shepherd can run any provider including magi (Settings → 목자), and its
	// runs can spawn subagents, so both branches apply here too.
	let hasSubagentOutput = $derived(
		liveOutput.some((line) => /^\[SUB:/.test(line ?? ''))
	);

	function openAttentionIssue(item) {
		if (!item.target_id || !item.project) return;
		openIssue = { projectName: item.project, issueId: item.target_id };
	}

	onMount(async () => {
		activeTab = tabFromUrl();

		await loadStatus();
		loaded = true;

		// Seed the live pane from the running task's stored output: GET
		// /api/tasks/:id merges the in-memory lines for running tasks (#8165),
		// so opening the page mid-run still shows what happened so far.
		if (status?.running_task_id) {
			const res = await apiGet(`/api/tasks/${status.running_task_id}`);
			if (res?.data?.output?.length) liveOutput = res.data.output;
		}

		// One-shot read for the empty-output "최근 작업 #n 결과 보기" link.
		apiGet('/api/shepherd/tasks?limit=1')
			.then((res) => { recentTask = res?.data?.tasks?.[0] ?? null; })
			.catch(() => {});

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
			// Board counts every project's task (#599) — attention follows.
			scheduleAttentionRefresh();
		}));

		for (const ev of ['task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, (data) => {
				if (data.sheep_name === 'shepherd') loadStatus();
				scheduleAttentionRefresh();
			}));
		}

		// The manager sheep is excluded from the sheep store / systemStatus, so
		// its status_change needs the dedicated refresh like the sidebar does.
		unsubs.push(onSSE('status_change', (data) => {
			if (data.sheep_name === 'shepherd') loadStatus();
		}));

		// Events missed while the SSE connection was down: re-read the status
		// (each mounted section reloads its own data on reconnect).
		unsubs.push(onSSE('reconnect', () => { loadStatus(); scheduleAttentionRefresh(); }));
	});

	onDestroy(() => {
		unsubs.forEach((fn) => fn?.());
		clearTimeout(attentionTimer);
	});
</script>

<div class="shepherd-page">
	<!-- ============ Header (project-screen .project-head structure) ============ -->
	<header class="project-head">
		<div class="ph-main">
			<span class="eyebrow">Watch · Orchestrator</span>
			<div class="ph-title-row">
				<h1 class="project-title">Shepherd</h1>
			</div>
			<p class="ph-meta">
				{#if status?.workspace_path}
					<span class="header-path mono" title={status.workspace_path}>{status.workspace_path}</span>
				{:else if status}
					<span class="header-path mono ws-missing">워크스페이스 미지정</span>
				{:else}
					<span class="header-path mono">—</span>
				{/if}
			</p>
		</div>
		<div class="header-status">
			{#if status}
				<div class="sheep-pill" class:working>
					<span class="sheep-ava" aria-hidden="true">s</span>
					<span class="sheep-label">shepherd</span>
					<StatusBadge status={working ? 'working' : 'idle'} taskId={runningTaskId || null} />
				</div>
				<div class="controls">
					<!-- Provider/model are changed in Settings → 목자 only (wiki:
					     shepherd-orchestration). The chips link there — the settings
					     screen opens on the 목자 tab, so no query is needed. -->
					<a class="head-chip chip chip-mono" href="/settings"
						title="설정 → 목자에서 바꿉니다">{status.provider || '—'}</a>
					<a class="head-chip chip chip-mono" href="/settings"
						title="설정 → 목자에서 바꿉니다">{status.model || '기본 모델'}</a>
				</div>
			{/if}
		</div>
	</header>

	<!-- ============ Status row (replaces the eight-cell status card) ============ -->
	{#if !loaded}
		<div class="status-row" aria-busy="true">
			<div class="skeleton" style="height: 24px; width: 96px; flex-shrink: 0"></div>
			<div class="skeleton" style="height: 24px; width: 72px; flex-shrink: 0"></div>
			<div class="skeleton" style="height: 24px; width: 120px; flex-shrink: 0"></div>
			<div class="skeleton" style="height: 24px; width: 110px; flex-shrink: 0"></div>
		</div>
	{:else if !status}
		<div class="status-row status-error">
			<span>목자 상태를 불러오지 못했습니다.</span>
			<button class="btn btn-sm btn-ghost" onclick={loadStatus}>
				<Icon name="refresh-cw" size={13} /> 다시 시도
			</button>
		</div>
	{:else if !workspaceOk}
		<!-- Workspace unset or invalid: block the console, point at Settings → 목자. -->
		<div class="banner card" role="alert">
			<span class="banner-icon"><Icon name="alert-triangle" size={18} /></span>
			<div class="banner-text">
				<strong>워크스페이스가 지정되지 않았거나 유효하지 않습니다</strong>
				<p>{status.workspace_error || '목자가 일할 폴더를 먼저 정해야 지시를 보낼 수 있습니다.'}</p>
			</div>
			<a class="btn btn-sm btn-primary" href="/settings">설정에서 지정</a>
		</div>
	{:else}
		<div class="status-row">
			<span class="status-chip" title="목자가 스스로 할 수 있는 범위, 설정에서 바꿉니다">
				권한 {tierLabels[status.autonomy_tier] || status.autonomy_tier || '—'}
			</span>
			<span class="status-chip mono" title="감시 이슈 작업의 동시 pending+running 수 / 상한"
				class:warn={delegationAtCap}>
				위임 {status.active_delegations ?? 0}/{status.max_active_delegations ?? 0}{#if delegationAtCap}{' '}— 상한 도달{/if}
			</span>
			<span class="status-chip mono" title="목자 작업과 감시 이슈 작업의 합산(자정 기준)"
				class:warn={status.cost_limit_exceeded}>
				{#if status.cost_limit_exceeded}
					{fmtCost(status.today_cost)} / {fmtCost(status.daily_cost_limit_usd)} — 초과
				{:else if status.daily_cost_limit_usd > 0}
					{fmtCost(status.today_cost)} / {fmtCost(status.daily_cost_limit_usd)}
				{:else}
					{fmtCost(status.today_cost)} · 제한 없음
				{/if}
			</span>
			<span class="status-chip" title="목자 작업(지시·검토·순찰)에만 든 비용">
				오늘 {status.today_task_count ?? 0}건 · {fmtCost(status.today_cost_usd)}
			</span>
			<span class="status-chip" class:off={!status.patrol_enabled}
				title={status.patrol_interval ? '순찰 주기 ' + status.patrol_interval : ''}>
				{#if status.patrol_enabled}
					다음 순찰 {(status.next_patrol_at || '').slice(5, 16) || '—'}
				{:else}
					순찰 꺼짐
				{/if}
			</span>
			{#if status.circuit_tripped}
				<span class="status-chip warn" title="연속 실패 {status.consecutive_failures}회 — 서킷 브레이커가 목자 작업을 멈췄습니다">
					<Icon name="zap" size={12} /> 서킷 브레이커 켜짐
				</span>
			{/if}
		</div>
	{/if}

	<!-- ============ Tabs ============ -->
	<PageTabs tabs={pageTabs} value={activeTab} onChange={switchTab} ariaLabel="목자 화면 섹션" />

	<!-- ============ Tab content: fills the remaining height ============ -->
	<div class="content-area">
		{#if activeTab === 'output'}
			<div class="output-fill" role="tabpanel" id="panel-output" aria-labelledby="tab-output">
				{#if status?.provider === 'magi'}
					<MagiStreamPanel lines={liveOutput} maxHeight="none" />
				{:else if hasSubagentOutput}
					<SubagentStreamPanel lines={liveOutput} maxHeight="none" />
				{:else if liveOutput.length > 0}
					<OutputViewer lines={liveOutput} maxHeight="none" />
				{:else}
					<div class="output-empty">
						<Icon name="terminal" size={28} />
						<p>아래 입력창으로 목자에게 지시를 보내면 실시간 출력이 여기에 나옵니다.</p>
						{#if recentTask}
							<!-- from=shepherd&tab=output: the task page's back link returns to this tab (#623). -->
							<a class="recent-link" href="/tasks/{recentTask.id}?from=shepherd&tab=output">최근 작업 #{recentTask.id} 결과 보기</a>
						{/if}
					</div>
				{/if}
			</div>
		{/if}

		{#if activeTab === 'history'}
			<div class="tab-fill" role="tabpanel" id="panel-history" aria-labelledby="tab-history">
				<ShepherdTaskList refreshKey={taskRefreshKey} onRefresh={loadStatus} />
			</div>
		{/if}

		{#if activeTab === 'patrols'}
			<div class="tab-fill" role="tabpanel" id="panel-patrols" aria-labelledby="tab-patrols">
				<ShepherdPatrolLog onRan={() => { loadStatus(); taskRefreshKey += 1; }} {status} />
			</div>
		{/if}

		{#if activeTab === 'board'}
			<!-- Desktop: the board manages its own height — filters stay pinned and
			     each column scrolls inside itself, so this pane must NOT scroll (#623).
			     Mobile/short screens flip that: the pane becomes the single vertical
			     scroller and the columns grow to natural height (#634). -->
			<div class="tab-fill board-pane" role="tabpanel" id="panel-board" aria-labelledby="tab-board">
				<ShepherdIssueBoard refreshKey={boardRefreshKey} onOpenIssue={(r) => (openIssue = r)} />
			</div>
		{/if}

		{#if activeTab === 'attention'}
			<div class="tab-fill" role="tabpanel" id="panel-attention" aria-labelledby="tab-attention">
				<AttentionPanel tick={attentionTick} onOpenIssue={openAttentionIssue} />
			</div>
		{/if}
	</div>

	<!-- ============ Instruction input pinned at the bottom (output tab only) ============ -->
	{#if activeTab === 'output'}
		<div class="command-bar card">
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
		</div>
	{/if}

	{#if openIssue}
		<IssueDetailModal
			projectName={openIssue.projectName}
			issueId={openIssue.issueId}
			onClose={() => openIssue = null}
			onChanged={() => { boardRefreshKey += 1; attentionTick += 1; }}
		/>
	{/if}
</div>

<style>
	/* Full-height app page: header + status row + tabs + scroll pane + command
	   bar. The layout shrinks its own padding for this route (+layout fillRoute,
	   same contract as the project workspace). */
	.shepherd-page {
		display: flex;
		flex-direction: column;
		height: calc(100dvh - var(--page-pad-top) - var(--page-pad-bottom));
		min-height: 420px;
		overflow: hidden;
	}

	/* ---------- Header (mirrors the project screen's .project-head) ---------- */
	.project-head {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: 12px 24px;
		flex-shrink: 0;
		padding-bottom: 14px;
		animation: rise-in var(--dur-3) var(--ease-out) backwards;
	}

	.ph-main {
		display: flex;
		flex-direction: column;
		gap: 8px;
		min-width: 0;
		flex: 1;
	}

	.ph-title-row {
		display: flex;
		align-items: center;
		gap: 12px;
		min-width: 0;
	}

	/* 48px: Press Start 2P is crisp only at multiples of 8px (issue #636 §2-1),
	   so the old clamp(34px,3.4vw,46px) is replaced by a fixed step size. */
	.project-title {
		font-family: var(--font-display);
		font-size: var(--fs-3xl);
		font-weight: 400;
		line-height: 0.95;
		letter-spacing: 0;
		margin: 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.ph-meta {
		display: flex;
		align-items: baseline;
		gap: 10px;
		min-width: 0;
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		margin: 0;
	}

	.header-path {
		flex-shrink: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--fs-2xs);
	}

	.ws-missing { color: var(--warning); }

	.header-status {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 10px;
		flex-shrink: 0;
	}

	.sheep-pill {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		padding: 3px 4px 3px 3px;
		border-radius: 0;
		background: var(--well);
		box-shadow: inset 0 0 0 2px #000;
	}

	.sheep-pill.working {
		box-shadow: inset 0 0 0 2px var(--live);
	}

	.sheep-ava {
		display: grid;
		place-items: center;
		width: 22px;
		height: 22px;
		border-radius: 0;
		background: var(--wool-soft);
		color: var(--wool);
		font-size: var(--fs-2xs);
		font-weight: var(--fw-semibold);
	}

	.sheep-pill.working .sheep-ava {
		background: var(--live-soft);
		color: var(--live);
	}

	.sheep-label {
		font-weight: var(--fw-medium);
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.controls {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.head-chip {
		cursor: pointer;
		text-decoration: none;
		line-height: 1.9;
		transition: border-color var(--dur-1) steps(2), color var(--dur-1) steps(2);
	}

	.head-chip:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
		text-decoration: none;
	}

	/* ---------- Status row: one line of chips ---------- */
	.status-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-shrink: 0;
		margin-bottom: 14px;
		/* Narrow screens scroll sideways instead of wrapping — a wrapped second
		   row would steal height from the output pane. Scrollbar hidden like
		   the tab bar's. */
		flex-wrap: nowrap;
		overflow-x: auto;
		scrollbar-width: none;
		-webkit-overflow-scrolling: touch;
	}

	.status-row::-webkit-scrollbar { display: none; }

	.status-row.status-error {
		gap: 12px;
		font-size: var(--fs-sm);
		color: var(--danger-text);
	}

	.status-chip {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		flex-shrink: 0;
		white-space: nowrap;
		padding: 3px 10px;
		border: 1px solid var(--border-subtle);
		border-radius: 0;
		background: var(--well);
		color: var(--text-secondary);
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		line-height: 1.6;
	}

	.status-chip.mono {
		font-family: var(--font-mono);
		font-weight: var(--fw-regular);
	}

	.status-chip.warn {
		color: var(--warning);
		background: var(--warning-soft);
		border-color: var(--warning-line);
	}

	.status-chip.off { color: var(--text-tertiary); }

	/* Workspace warning banner (moved from the top of the page, #622) */
	.banner {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: 12px 16px;
		margin-bottom: 14px;
		flex-shrink: 0;
		background: linear-gradient(var(--warning-soft), var(--warning-soft)), var(--bg-2);
		box-shadow: var(--px-ring), inset 3px 3px 0 0 var(--warning), inset -3px -3px 0 0 var(--lo);
	}

	.banner-icon { color: var(--warning); flex-shrink: 0; }

	.banner-text { flex: 1; min-width: 0; }

	.banner-text strong {
		display: block;
		font-size: var(--fs-base);
		color: var(--text-primary);
	}

	.banner-text p { margin: 4px 0 0; font-size: var(--fs-sm); color: var(--text-secondary); }

	/* ---------- Tab content: fills the remaining height ---------- */
	.content-area {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.output-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.output-fill :global(.output-viewer) {
		flex: 1;
		max-height: none !important;
		min-height: 0;
	}

	/* Empty live output: the pane stays, with guidance + a link to the last run. */
	.output-empty {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 12px;
		text-align: center;
		color: var(--text-tertiary);
		padding: var(--space-6) var(--space-4);
	}

	.output-empty p { margin: 0; font-size: var(--fs-sm); max-width: 420px; }

	.recent-link {
		font-size: var(--fs-sm);
		color: var(--accent);
		text-decoration: none;
		border: 1px solid var(--accent-line);
		background: var(--accent-soft);
		padding: 5px 12px;
		border-radius: 0;
	}

	.recent-link:hover { text-decoration: underline; }

	/* Other tabs scroll inside their own pane only. */
	.tab-fill {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 2px 2px 8px;
		display: flex;
		flex-direction: column;
	}

	/* Board pane: no scrolling here — the columns scroll inside themselves. */
	.tab-fill.board-pane { overflow: hidden; }

	/* ---------- Command bar: pinned under the output ---------- */
	.command-bar {
		position: relative; /* console-status toast anchors here (#622) */
		flex-shrink: 0;
		margin-top: 12px;
		padding: 8px;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel), var(--shadow-2);
	}


	/* ---------- Responsive (mirrors the project screen's breakpoints) ------ */
	@media (max-width: 960px) {
		.project-head {
			flex-direction: column;
			align-items: stretch;
		}

		.header-status {
			flex-direction: row;
			flex-wrap: wrap;
			align-items: center;
			justify-content: space-between;
		}
	}

	@media (max-width: 768px) {
		.shepherd-page {
			height: calc(100dvh - var(--mobile-topbar-h) - var(--page-pad-top) - var(--page-pad-bottom));
			min-height: 360px;
			overflow-x: hidden;
		}

		/* #634: on mobile the board pane itself is the scroll container — the
		   columns no longer scroll inside themselves (see ShepherdIssueBoard's
		   matching @media block). Desktop keeps `overflow:hidden` above. */
		.tab-fill.board-pane { overflow-y: auto; }

		.project-head {
			gap: 10px;
			padding-bottom: 10px;
		}

		.project-title {
			font-size: var(--fs-lg);
		}

		.command-bar {
			margin-top: 8px;
			padding: 6px;
		}
	}

	/* Short screens (square phones, landscape phones): height goes to the
	   output pane. The header folds into one row and drops the workspace path
	   (kept as the title tooltip). */
	@media (max-height: 600px) {
		.shepherd-page {
			min-height: 240px;
		}

		/* #634: short-and-narrow viewports (landscape phones) mirror the mobile
		   board — one status tab, natural heights, and the pane is the scroller.
		   Five columns here left every .col-list with clientHeight 0 in
		   measurement, so per-column scrolling could not reach the list end.
		   Mirrors the media query in ShepherdIssueBoard.svelte. */
		@media (max-height: 600px) and (max-width: 1100px) {
			.tab-fill.board-pane { overflow-y: auto; }
		}

		.project-head {
			flex-direction: row;
			flex-wrap: wrap;
			align-items: center;
			gap: 6px 8px;
			padding-bottom: 8px;
		}

		.ph-main {
			flex: 1 1 120px;
			flex-direction: row;
			align-items: center;
			gap: 8px;
		}

		.ph-main .eyebrow,
		.ph-meta {
			display: none;
		}

		.project-title {
			font-size: var(--fs-lg);
			line-height: 1.15;
		}

		.header-status {
			display: contents;
		}

		.controls {
			flex-wrap: wrap;
			justify-content: flex-end;
			margin-left: auto;
		}

		.status-row { margin-bottom: 10px; }

		.command-bar {
			margin-top: 6px;
			padding: 4px;
		}
	}

</style>
