<script>
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { appendLiveOutput } from '$lib/liveOutput.js';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import ShepherdConsole from '$lib/components/ShepherdConsole.svelte';
	import IssueDetailModal from '$lib/components/IssueDetailModal.svelte';
	import AttentionPanel from '$lib/components/AttentionPanel.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';

	// Shepherd (목자) console (#598) + issue board and needs-attention panel
	// (#599). Sections: workspace warning, status card, console + live output,
	// recent shepherd tasks, the patrol log (#600), then the cross-project
	// issue board with the attention panel beside it.

	let status = $state(null);      // GET /api/shepherd/status
	let tasks = $state([]);         // GET /api/shepherd/tasks
	let loaded = $state(false);
	let unsubs = [];

	let liveOutput = $state([]);
	let liveOutputOpen = $state(false); // tracks an open (non-newline-terminated) tail line

	const kindLabels = { command: '지시', review: '검토', patrol: '순찰' };

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

	async function loadTasks() {
		const res = await apiGet('/api/shepherd/tasks?limit=20');
		if (res?.data) tasks = res.data.tasks || [];
	}

	// ---- Issue board (#599) -------------------------------------------------
	// GET /api/issues (cross-project). One request per status column; the
	// `counts` map rides on every response, so column headers come from the
	// first column that answers. Filters live in the URL query so a reload
	// keeps them (wiki: svelte5-select-bind-change-vs-input — selects read
	// their bound value on `change`, so filter handlers use onchange).

	const BOARD_COLUMNS = ['todo', 'in_progress', 'testing', 'failed', 'done'];
	const columnLabels = {
		todo: '작업전',
		in_progress: '작업중',
		testing: '테스트',
		failed: '실패',
		done: '성공'
	};
	const typeLabels = { design: '설계', feature: '기능', bug: '버그' };
	const typeIcons = { design: 'layers', feature: 'sparkles', bug: 'bug' };
	const DONE_WINDOW_MS = 7 * 24 * 3600 * 1000;
	const BOARD_LIMIT = 50;

	// Filter state (URL is the source of truth after init).
	let filterProjects = $state([]);   // empty = all in-scope projects
	let filterType = $state('');       // '' | design | feature | bug
	let filterWatched = $state('');    // '' | true | false
	let searchQ = $state('');

	// Board data.
	let board = $state({});            // status -> issue[]
	let boardCounts = $state({});      // status -> total (filter applied, status excluded)
	let maxReviewRounds = $state(0);   // review cap for the k/max chip
	let boardLoaded = $state(false);
	let boardError = $state('');
	let doneExpanded = $state(false);   // "더 보기" for the done column
	let narrowTab = $state('todo');    // active column on narrow screens
	let projectChoices = $state([]);   // visible project names for the filter chips

	// Attention panel refresh counter (shared debounce with the board).
	let attentionTick = $state(0);

	// ---- Patrol log (#600) ---------------------------------------------------
	// GET /api/shepherd/patrols — every pass (auto or manual) stores a record:
	// when the code looked, how many findings were new, which shepherd task it
	// woke (+ that task's first summary line), and why a wake was skipped.
	// POST /api/shepherd/patrols/run is the "지금 순찰" button — same rules as
	// the ticker pass, so it can legitimately report a skip.
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
				await Promise.all([loadPatrols(), loadStatus(), loadTasks()]);
			}
		} catch (e) {
			patrolMsg = '순찰 실행에 실패했습니다: ' + (e?.message || e);
		} finally {
			patrolBusy = false;
			setTimeout(() => (patrolMsg = ''), 6000);
		}
	}

	// Detail modal target.
	let openIssue = $state(null);      // { projectName, issueId }

	function initFiltersFromUrl() {
		const p = $page.url.searchParams;
		filterProjects = (p.get('project') || '').split(',').map((s) => s.trim()).filter(Boolean);
		filterType = p.get('type') || '';
		filterWatched = p.get('watched') || '';
		searchQ = p.get('q') || '';
	}

	function syncFiltersToUrl() {
		const params = new URLSearchParams();
		if (filterProjects.length) params.set('project', filterProjects.join(','));
		if (filterType) params.set('type', filterType);
		if (filterWatched) params.set('watched', filterWatched);
		if (searchQ) params.set('q', searchQ);
		const qs = params.toString();
		goto(qs ? `?${qs}` : $page.url.pathname, { keepFocus: true, noScroll: true, replaceState: true });
	}

	function toggleProjectFilter(name) {
		filterProjects = filterProjects.includes(name)
			? filterProjects.filter((p) => p !== name)
			: [...filterProjects, name];
		syncFiltersToUrl();
		loadBoard();
	}

	function applyBoardFilters() {
		syncFiltersToUrl();
		loadBoard();
	}

	function resetBoardFilters() {
		filterProjects = [];
		filterType = '';
		filterWatched = '';
		searchQ = '';
		doneExpanded = false;
		syncFiltersToUrl();
		loadBoard();
	}

	let boardSeq = 0;

	function boardQuery(statusName) {
		const params = new URLSearchParams();
		params.set('status', statusName);
		params.set('limit', String(BOARD_LIMIT));
		params.set('sort', 'updated');
		if (filterProjects.length) params.set('project', filterProjects.join(','));
		if (filterType) params.set('type', filterType);
		if (filterWatched) params.set('watched', filterWatched);
		if (searchQ) params.set('q', searchQ);
		return `/api/issues?${params.toString()}`;
	}

	async function loadBoard() {
		const seq = ++boardSeq;
		boardError = '';
		try {
			const responses = await Promise.all(BOARD_COLUMNS.map((s) => apiGet(boardQuery(s))));
			if (seq !== boardSeq) return; // stale — a newer filter won
			const next = {};
			for (let i = 0; i < BOARD_COLUMNS.length; i++) {
				const res = responses[i];
				if (!res?.success) {
					boardError = res?.message || '이슈 보드를 불러오지 못했습니다.';
					return;
				}
				next[BOARD_COLUMNS[i]] = res.data || [];
				if (res.counts) boardCounts = res.counts;
				if (res.max_review_rounds) maxReviewRounds = res.max_review_rounds;
			}
			board = next;
			boardLoaded = true;
		} catch (e) {
			if (seq === boardSeq) boardError = '이슈 보드를 불러오지 못했습니다.';
		}
	}

	let searchDebounce = null;
	function onSearchInput() {
		clearTimeout(searchDebounce);
		searchDebounce = setTimeout(applyBoardFilters, 350);
	}

	// Done column: recent 7 days first ("더 보기" reveals the rest of the page).
	function doneItems() {
		const all = board.done || [];
		const cutoff = Date.now() - DONE_WINDOW_MS;
		const parse = (s) => {
			if (!s) return 0;
			const t = new Date(String(s).replace(' ', 'T')).getTime();
			return Number.isNaN(t) ? 0 : t;
		};
		const sorted = [...all].sort((a, b) => parse(b.completed_at || b.updated_at) - parse(a.completed_at || a.updated_at));
		if (doneExpanded) return sorted;
		return sorted.filter((it) => parse(it.completed_at || it.updated_at) >= cutoff);
	}

	function openBoardIssue(issue) {
		openIssue = { projectName: issue.project_name, issueId: issue.id };
	}

	function openAttentionIssue(item) {
		if (!item.target_id || !item.project) return;
		openIssue = { projectName: item.project, issueId: item.target_id };
	}

	// SSE → debounced board + attention refresh (#599): events arrive in
	// bursts (task_start then output etc.), so collapse them into one reload.
	let sseTimer = null;
	function scheduleBoardRefresh() {
		clearTimeout(sseTimer);
		sseTimer = setTimeout(() => {
			loadBoard();
			attentionTick += 1; // AttentionPanel reloads on tick (store updates too)
			loadPatrols(); // an auto pass may have stored a record (#600)
		}, 600);
	}

	onMount(async () => {
		initFiltersFromUrl();
		await Promise.all([loadStatus(), loadTasks(), loadBoard(), loadPatrols()]);
		loaded = true;

		apiGet('/api/projects')
			.then((res) => {
				if (res?.data) projectChoices = res.data.filter((p) => !p.hidden).map((p) => p.name);
			})
			.catch(() => {});

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
			if (data.sheep_name === 'shepherd') {
				liveOutput = [`▶ ${data.prompt}`, ''];
				liveOutputOpen = false;
				loadStatus();
			}
			scheduleBoardRefresh(); // board counts every project's task (#599)
			loadTasks();
		}));

		for (const ev of ['task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, (data) => {
				if (data.sheep_name === 'shepherd') {
					loadStatus();
					loadTasks();
				}
				scheduleBoardRefresh(); // board + attention: one debounced pass
			}));
		}

		// The manager sheep is excluded from the sheep store / systemStatus, so
		// its status_change needs the dedicated refresh like the sidebar does.
		unsubs.push(onSSE('status_change', (data) => {
			if (data.sheep_name === 'shepherd') loadStatus();
		}));

		// Events missed while the SSE connection was down: re-read everything.
		unsubs.push(onSSE('reconnect', () => {
			loadStatus();
			loadTasks();
			scheduleBoardRefresh();
		}));
	});

	onDestroy(() => {
		unsubs.forEach(fn => fn?.());
		clearTimeout(sseTimer);
		clearTimeout(searchDebounce);
	});

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
	<!-- ============ Patrol log (#600) ============ -->
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

	<!-- ============ Issue board + attention panel (#599) ============ -->
	<section class="block" aria-label="이슈 보드">
		<SectionHeader index="04" eyebrow="Board" title="전 프로젝트 이슈 보드"
			subtitle="관리 범위 안의 이슈를 상태로 봅니다. 카드를 누르면 상세에서 감시·실행을 조절합니다.">
			<button class="btn btn-sm btn-ghost" onclick={loadBoard}>
				<Icon name="refresh-cw" size={13} /> 새로고침
			</button>
		</SectionHeader>

		<div class="board-filters">
			<input
				type="text"
				class="input input-search board-search"
				placeholder="제목 검색..."
				bind:value={searchQ}
				oninput={onSearchInput}
			/>
			<!-- bind:value on <select> fires on `change`, so the handler is onchange too
			     (wiki: svelte5-select-bind-change-vs-input). -->
			<select class="input" bind:value={filterType} onchange={applyBoardFilters} aria-label="유형 필터">
				<option value="">모든 유형</option>
				<option value="design">설계</option>
				<option value="feature">기능</option>
				<option value="bug">버그</option>
			</select>
			<select class="input" bind:value={filterWatched} onchange={applyBoardFilters} aria-label="감시 필터">
				<option value="">감시 전체</option>
				<option value="true">감시 중만</option>
				<option value="false">감시 외만</option>
			</select>
			<button class="btn btn-ghost" onclick={resetBoardFilters}><Icon name="rotate-ccw" size={13} /> 초기화</button>
		</div>

		{#if projectChoices.length > 0}
			<div class="board-projects" role="group" aria-label="프로젝트 필터">
				{#each projectChoices as name (name)}
					<button
						class="proj-chip chip"
						class:chip-accent={filterProjects.includes(name)}
						onclick={() => toggleProjectFilter(name)}
						aria-pressed={filterProjects.includes(name)}
					>{name}</button>
				{/each}
			</div>
		{/if}

		<div class="board-layout">
			<div class="board-main">
				{#if boardError}
					<div class="board-error" role="alert"><Icon name="alert-triangle" size={14} /> {boardError}</div>
				{/if}

				<!-- Narrow screens: status tabs + one list. Wide screens: 5 columns. -->
				<div class="narrow-tabs" role="tablist" aria-label="보드 상태">
					{#each BOARD_COLUMNS as col (col)}
						<button class="narrow-tab" class:active={narrowTab === col} role="tab"
							aria-selected={narrowTab === col} onclick={() => narrowTab = col}>
							{columnLabels[col]}
							<span class="mono num narrow-count">{boardCounts[col] ?? (board[col] || []).length}</span>
						</button>
					{/each}
				</div>

				<div class="board-columns">
					{#each BOARD_COLUMNS as col (col)}
						<div class="board-col" class:is-active={narrowTab === col}>
							<div class="col-head">
								<span class="col-title">{columnLabels[col]}</span>
								<span class="col-count mono num">{boardCounts[col] ?? (board[col] || []).length}</span>
							</div>
							<div class="col-list">
								{#if !boardLoaded}
									{#each Array(2) as _}
										<div class="skeleton" style="height: 64px"></div>
									{/each}
								{:else}
									{#each (col === 'done' ? doneItems() : board[col] || []) as issue (issue.id)}
										<div class="board-card" role="button" tabindex="0"
											onclick={() => openBoardIssue(issue)}
											onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openBoardIssue(issue); } }}>
											<div class="card-top">
												<span class="chip proj-badge" title={issue.project_name}>{issue.project_name}</span>
												<span class="type-badge {issue.type}">
													<Icon name={typeIcons[issue.type] || 'file-text'} size={11} /> {typeLabels[issue.type] || issue.type}
												</span>
												{#if issue.shepherd_watch}
													<span class="watch-flag" title="목자 감시 중 (회차 {issue.shepherd_rounds ?? 0}{maxReviewRounds ? '/' + maxReviewRounds : ''})">
														<Icon name="eye" size={12} />
														{maxReviewRounds ? `${issue.shepherd_rounds ?? 0}/${maxReviewRounds}` : ''}
													</span>
												{/if}
											</div>
											<div class="card-title">#{issue.id} {issue.title}</div>
											{#if issue.shepherd_last_review}
												<div class="card-review" title="목자 마지막 검토">{issue.shepherd_last_review}</div>
											{/if}
											<div class="card-foot">
												{#if issue.last_task_status}
													<span class="task-state mono num" class:ok={issue.last_task_status === 'completed'} class:bad={issue.last_task_status === 'failed'}>작업 {issue.last_task_status}</span>
												{/if}
												<span class="mono num card-time">{issue.updated_at}</span>
											</div>
										</div>
									{/each}
									{#if col === 'done' && !doneExpanded && (board.done || []).length > doneItems().length}
										<button class="btn btn-sm btn-ghost more" onclick={() => { doneExpanded = true; }}>
											더 보기 ({(board.done || []).length - doneItems().length})
										</button>
									{/if}
									{#if (board[col] || []).length === 0 && !(col === 'done' && doneItems().length > 0)}
										<p class="col-empty">비어 있음</p>
									{/if}
								{/if}
							</div>
						</div>
					{/each}
				</div>
			</div>

			<aside class="board-side" aria-label="확인 필요 항목">
				<SectionHeader eyebrow="Attention" title="확인 필요" subtitle="실패·정체·상한 — 심각도 순서대로" />
				<AttentionPanel tick={attentionTick} onOpenIssue={openAttentionIssue} />
			</aside>
		</div>
	</section>

	{#if openIssue}
		<IssueDetailModal
			projectName={openIssue.projectName}
			issueId={openIssue.issueId}
			onClose={() => openIssue = null}
			onChanged={() => { loadBoard(); attentionTick += 1; }}
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

	/* ===== Issue board (#599) ===== */
	.board-filters {
		display: flex;
		gap: 8px;
		margin-bottom: 10px;
		flex-wrap: wrap;
	}

	.board-search {
		flex: 1;
		min-width: 180px;
		min-height: 34px;
		font-size: var(--fs-sm);
	}

	.board-filters select {
		min-height: 34px;
		padding-top: 6px;
		padding-bottom: 6px;
		font-size: var(--fs-sm);
	}

	.board-projects {
		display: flex;
		gap: 6px;
		flex-wrap: wrap;
		margin-bottom: 14px;
	}

	.proj-chip {
		cursor: pointer;
		border: 1px solid var(--border-subtle);
		background: rgba(237, 230, 218, 0.04);
		color: var(--text-secondary);
		font-family: inherit;
	}

	.proj-chip:hover { border-color: var(--border-strong); color: var(--text-primary); }

	.board-error {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 10px;
		color: var(--danger);
		font-size: var(--fs-xs);
	}

	.board-layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 340px;
		gap: var(--space-4);
		align-items: start;
	}

	.board-main { min-width: 0; }

	.narrow-tabs { display: none; }

	.board-columns {
		display: grid;
		grid-template-columns: repeat(5, minmax(0, 1fr));
		gap: 10px;
	}

	.board-col {
		min-width: 0;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		background: var(--bg-2);
		padding: 10px;
	}

	.col-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		margin-bottom: 8px;
	}

	.col-title {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-secondary);
	}

	.col-count {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.col-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
		max-height: 62vh;
		overflow-y: auto;
	}

	.col-empty {
		margin: 4px 0;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.board-card {
		padding: 8px 10px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius);
		background: var(--bg-3);
		box-shadow: var(--sheen);
		cursor: pointer;
		transition: border-color var(--dur-1) ease, background var(--dur-1) ease;
	}

	.board-card:hover,
	.board-card:focus-visible {
		border-color: var(--border-strong);
		background: var(--bg-2);
	}

	.card-top {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
		margin-bottom: 6px;
	}

	.proj-badge {
		max-width: 110px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--fs-2xs);
	}

	.type-badge {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 0 7px;
		border-radius: var(--radius-full);
		font-size: var(--fs-2xs);
		line-height: 1.8;
		background: rgba(237, 230, 218, 0.05);
		color: var(--text-secondary);
	}

	.type-badge.feature { background: var(--accent-soft); color: var(--accent); }
	.type-badge.bug { background: var(--danger-soft); color: var(--danger); }
	.type-badge.design { background: var(--queued-soft); color: var(--queued); }

	.watch-flag {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-size: var(--fs-2xs);
		font-family: var(--font-mono);
		color: var(--accent);
	}

	.card-title {
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
		color: var(--text-primary);
		line-height: 1.4;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}

	.card-review {
		margin-top: 5px;
		font-size: var(--fs-2xs);
		color: var(--text-secondary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.card-foot {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		margin-top: 6px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.task-state.ok { color: var(--success); }
	.task-state.bad { color: var(--danger); }

	.more { align-self: flex-start; }

	.board-side {
		position: sticky;
		top: var(--space-4);
	}

	@media (max-width: 1100px) {
		.board-layout { grid-template-columns: minmax(0, 1fr); }

		.board-side { position: static; }

		.board-columns { grid-template-columns: repeat(3, minmax(0, 1fr)); }
	}

	@media (max-width: 860px) {
		/* Narrow screens: status tabs pick one visible column. The columns stay
		   in the DOM; only the active one shows. */
		.narrow-tabs {
			display: flex;
			gap: 6px;
			flex-wrap: wrap;
			margin-bottom: 10px;
		}

		.narrow-tab {
			display: inline-flex;
			align-items: center;
			gap: 6px;
			padding: 5px 10px;
			border: 1px solid var(--border-subtle);
			border-radius: var(--radius-full);
			background: none;
			color: var(--text-secondary);
			font-family: inherit;
			font-size: var(--fs-xs);
			cursor: pointer;
		}

		.narrow-tab.active {
			border-color: var(--accent);
			background: var(--accent-soft);
			color: var(--accent);
		}

		.narrow-count { font-size: var(--fs-2xs); }

		.board-columns { grid-template-columns: minmax(0, 1fr); }

		.board-col { display: none; }
		.board-col.is-active { display: block; }
	}
</style>
