<script>
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { timeAgo, firstLine, fmtCost } from '$lib/shepherdFormat.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import Pagination from '$lib/components/Pagination.svelte';

	// Shepherd task list (#621 split; filters + paging #623).
	// GET /api/shepherd/tasks now takes page/q and answers total/total_pages/
	// page, so this tab mirrors the project screen's Task History density:
	// kind chip · #id · status · summary line · model · cost · duration · time.
	// Filter values live in the URL (kind / tstatus / tq / tpage) so a reload
	// keeps them — the keys must not collide with the board's project/type/
	// watched/q, and every write COPIES the current query and only touches its
	// own keys (wiki: svelte5-select-bind-change-vs-input — selects answer on
	// `change`, so filter handlers use onchange).
	let { refreshKey = 0, onRefresh = () => {} } = $props();

	const PAGE_SIZE = 20;
	const kindLabels = { command: '지시', review: '검토', patrol: '순찰' };
	const KIND_CHIPS = [
		{ id: '', label: '전체' },
		{ id: 'command', label: '지시' },
		{ id: 'review', label: '검토' },
		{ id: 'patrol', label: '순찰' }
	];
	const STATUS_OPTIONS = [
		{ id: '', label: '전체 상태' },
		{ id: 'pending', label: '대기' },
		{ id: 'running', label: '실행 중' },
		{ id: 'completed', label: '완료' },
		{ id: 'failed', label: '실패' },
		{ id: 'stopped', label: '중지' }
	];

	let tasks = $state([]);
	let total = $state(0);
	let totalPages = $state(1);
	let pageNow = $state(1);
	let loaded = $state(false);
	let loadError = $state('');

	// Filters (URL is the source of truth after init).
	let filterKind = $state('');
	let filterStatus = $state('');
	let searchQ = $state('');

	let cancellingId = $state(null);
	// Set when a shepherd SSE event lands while the user is on page > 1: we
	// do not yank the list away, we offer the first page instead.
	let newOnFirstPage = $state(false);

	function initFiltersFromUrl() {
		const p = $page.url.searchParams;
		filterKind = p.get('kind') || '';
		filterStatus = p.get('tstatus') || '';
		searchQ = p.get('tq') || '';
		const n = parseInt(p.get('tpage') || '1', 10);
		pageNow = Number.isFinite(n) && n >= 1 ? n : 1;
	}

	function syncFiltersToUrl() {
		const params = new URLSearchParams($page.url.searchParams);
		if (filterKind) params.set('kind', filterKind);
		else params.delete('kind');
		if (filterStatus) params.set('tstatus', filterStatus);
		else params.delete('tstatus');
		if (searchQ) params.set('tq', searchQ);
		else params.delete('tq');
		if (pageNow > 1) params.set('tpage', String(pageNow));
		else params.delete('tpage');
		const qs = params.toString();
		goto(qs ? `?${qs}` : $page.url.pathname, { keepFocus: true, noScroll: true, replaceState: true });
	}

	let loadSeq = 0;

	async function loadTasks() {
		const seq = ++loadSeq;
		const params = new URLSearchParams();
		params.set('page', String(pageNow));
		params.set('limit', String(PAGE_SIZE));
		if (filterKind) params.set('kind', filterKind);
		if (filterStatus) params.set('status', filterStatus);
		if (searchQ.trim()) params.set('q', searchQ.trim());
		const res = await apiGet(`/api/shepherd/tasks?${params}`);
		if (seq !== loadSeq) return; // stale — a newer filter/page won
		if (!res?.success || !res.data) {
			loadError = '목자 작업 목록을 불러오지 못했습니다.';
			return;
		}
		loadError = '';
		tasks = res.data.tasks || [];
		total = res.data.total ?? tasks.length;
		totalPages = res.data.total_pages ?? 1;
		pageNow = res.data.page ?? pageNow;
		loaded = true;
	}

	function applyFilters(resetPage = true) {
		if (resetPage) pageNow = 1;
		newOnFirstPage = false;
		syncFiltersToUrl();
		loadTasks();
	}

	function onTaskPageChange(next) {
		pageNow = next;
		newOnFirstPage = false;
		syncFiltersToUrl();
		loadTasks();
	}

	function goToFirstPage() {
		newOnFirstPage = false;
		onTaskPageChange(1);
	}

	let searchDebounce = null;
	function onSearchInput() {
		clearTimeout(searchDebounce);
		searchDebounce = setTimeout(() => applyFilters(), 350);
	}

	async function cancelPendingTask(e, taskId) {
		e.preventDefault();
		e.stopPropagation();
		if (cancellingId) return;
		if (!confirm(`대기 중인 목자 작업 #${taskId}를 취소합니다.`)) return;
		cancellingId = taskId;
		try {
			const res = await apiPost(`/api/tasks/${taskId}/cancel`, {});
			if (res?.success) {
				await loadTasks();
			} else {
				alert(res?.message || '작업 취소에 실패했습니다.');
			}
		} catch (err) {
			alert(err?.message || '작업 취소에 실패했습니다.');
		} finally {
			cancellingId = null;
		}
	}

	function rowHref(t) {
		return `/tasks/${t.id}?from=shepherd&tab=history`;
	}

	function truncateModel(label) {
		if (!label) return '';
		const paren = label.indexOf(' (');
		if (paren !== -1) return label.slice(0, paren);
		const sep = label.indexOf(' / ');
		if (sep !== -1) return label.slice(sep + 3);
		return label;
	}

	// Duration only when both ends exist (pending/running rows show nothing).
	function durationSec(t) {
		if (!t.started_at || !t.completed_at) return 0;
		const a = new Date(String(t.started_at).replace(' ', 'T')).getTime();
		const b = new Date(String(t.completed_at).replace(' ', 'T')).getTime();
		if (Number.isNaN(a) || Number.isNaN(b) || b < a) return 0;
		return Math.round((b - a) / 1000);
	}

	function formatDuration(sec) {
		if (sec <= 0) return '';
		const h = Math.floor(sec / 3600);
		const m = Math.floor((sec % 3600) / 60);
		const s = sec % 60;
		return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
	}

	function rowText(t) {
		if (t.status === 'failed') return firstLine(t.error || t.summary || t.prompt);
		return firstLine(t.summary || t.prompt || t.error);
	}

	let unsubs = [];

	onMount(() => {
		initFiltersFromUrl();
		loadTasks();

		// #623: only the manager sheep's own events re-read this list (the
		// project board used to reload on every sheep's task_start). On page 1
		// the list updates in place; deeper pages get a "first page" offer so
		// the rows under the cursor never shift.
		const onShepherdEvent = () => {
			if (pageNow === 1) loadTasks();
			else newOnFirstPage = true;
		};
		for (const ev of ['task_start', 'task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, (data) => {
				if (data?.sheep_name === 'shepherd') onShepherdEvent();
			}));
		}
		unsubs.push(onSSE('reconnect', () => loadTasks()));
	});

	onDestroy(() => {
		unsubs.forEach((fn) => fn?.());
		clearTimeout(searchDebounce);
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

<section class="tasklist-tab" aria-label="목자 작업">
	<div class="filter-bar">
		<input
			type="text"
			class="input input-search filter-search"
			placeholder="프롬프트·요약 검색..."
			bind:value={searchQ}
			oninput={onSearchInput}
			aria-label="목자 작업 검색"
		/>

		<div class="kind-chips" role="group" aria-label="종류 필터">
			{#each KIND_CHIPS as k (k.id)}
				<button class="chip kind-chip" class:chip-accent={filterKind === k.id}
					aria-pressed={filterKind === k.id}
					onclick={() => { filterKind = k.id; applyFilters(); }}>
					{k.label}
				</button>
			{/each}
		</div>

		<!-- bind:value on <select> fires on `change`, so the handler is onchange too
		     (wiki: svelte5-select-bind-change-vs-input). -->
		<select class="input status-select" bind:value={filterStatus}
			onchange={() => applyFilters()} aria-label="상태 필터">
			{#each STATUS_OPTIONS as o (o.id)}
				<option value={o.id}>{o.label}</option>
			{/each}
		</select>

		<span class="filter-count mono num" aria-live="polite">전체 {total.toLocaleString()}건</span>

		<button class="btn btn-sm btn-ghost" onclick={async () => { await loadTasks(); onRefresh(); }}>
			<Icon name="refresh-cw" size={13} /> 새로고침
		</button>
	</div>

	{#if newOnFirstPage}
		<button class="new-notice" onclick={goToFirstPage}>
			<Icon name="chevron-up" size={13} /> 새 목자 작업이 있습니다. 첫 페이지 보기
		</button>
	{/if}

	{#if loadError}
		<div class="list-error" role="alert"><Icon name="alert-triangle" size={14} /> {loadError}</div>
	{/if}

	{#if !loaded}
		<div class="card" aria-busy="true">
			<div class="skeleton" style="height: 44px"></div>
			<div class="skeleton" style="height: 44px; margin-top: 8px"></div>
			<div class="skeleton" style="height: 44px; margin-top: 8px"></div>
		</div>
	{:else if tasks.length === 0}
		<div class="card">
			<EmptyState icon="inbox"
				title={searchQ || filterKind || filterStatus ? '조건에 맞는 목자 작업이 없습니다' : '아직 목자 작업이 없습니다'}
				description={searchQ || filterKind || filterStatus
					? '필터를 지우고 다시 보세요.'
					: 'Live Output 탭의 입력창으로 지시를 보내면 여기에 기록됩니다.'} />
		</div>
	{:else}
		<div class="task-table">
			{#each tasks as t, i (t.id)}
				{@const dur = durationSec(t)}
				<a class="task-row reveal" style="--i: {i}" href={rowHref(t)} aria-label={`목자 작업 #${t.id}`}>
					<span class="row-kind chip chip-{t.shepherd_kind === 'command' ? 'accent' : t.shepherd_kind === 'review' ? 'queued' : 'info'}">
						{kindLabels[t.shepherd_kind] || t.shepherd_kind}
					</span>
					<span class="row-id mono num">#{t.id}</span>
					<div class="row-body">
						<span class="row-text">{rowText(t)}</span>
						<span class="row-meta mono">
							{#if t.model}<span class="meta-model" title={t.model}>{truncateModel(t.model)}</span>{/if}
							{#if t.cost_usd > 0}<span class="meta-cost">{fmtCost(t.cost_usd)}</span>{/if}
							{#if dur > 0}<span class="meta-dur" title="걸린 시간">{formatDuration(dur)}</span>{/if}
						</span>
					</div>
					<StatusBadge status={t.status} />
					{#if t.status === 'pending'}
						<button class="cancel-btn" onclick={(e) => cancelPendingTask(e, t.id)}
							disabled={cancellingId === t.id} aria-label={`목자 작업 #${t.id} 취소`}>
							{cancellingId === t.id ? '…' : 'Cancel'}
						</button>
					{/if}
					<span class="row-time mono num" title={t.created_at}>{timeAgo(t.created_at)}</span>
				</a>
			{/each}
		</div>

		<Pagination page={pageNow} totalPages={totalPages} total={total} limit={PAGE_SIZE}
			onChange={onTaskPageChange} />
	{/if}
</section>

<style>
	.tasklist-tab {
		display: flex;
		flex-direction: column;
		min-height: 0;
	}

	/* ---------- Filter row ---------- */
	.filter-bar {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		margin-bottom: 12px;
	}

	.filter-search {
		flex: 1;
		min-width: 180px;
		min-height: 34px;
		font-size: var(--fs-sm);
	}

	.kind-chips {
		display: inline-flex;
		gap: 4px;
	}

	.kind-chip {
		cursor: pointer;
		border: 1px solid var(--border-subtle);
		background: var(--well);
		color: var(--text-secondary);
		font-family: inherit;
	}

	.kind-chip:hover { border-color: var(--border-strong); color: var(--text-primary); }

	.status-select {
		min-height: 34px;
		padding-top: 6px;
		padding-bottom: 6px;
		font-size: var(--fs-sm);
	}

	.filter-count {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		margin-left: auto;
	}

	/* SSE offer while the user is reading a deeper page. */
	.new-notice {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		align-self: flex-start;
		margin-bottom: 10px;
		padding: 5px 12px;
		border-radius: 0;
		border: 1px solid var(--accent-line);
		background: var(--accent-soft);
		color: var(--accent);
		font-family: inherit;
		font-size: var(--fs-xs);
		cursor: pointer;
	}

	.list-error {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 10px;
		color: var(--danger-text);
		font-size: var(--fs-xs);
	}

	/* ---------- Rows (project screen Task History density) ---------- */
	.task-table {
		display: flex;
		flex-direction: column;
		padding: 4px;
		border: 0;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel);
	}

	.task-row {
		display: grid;
		grid-template-columns: 52px 54px minmax(0, 1fr) auto auto 72px;
		align-items: center;
		gap: 12px;
		padding: 10px 12px;
		border-radius: 0;
		color: var(--text-secondary);
		text-decoration: none;
		transition: background var(--dur-1) steps(2);
	}

	.task-row + .task-row { box-shadow: 0 -2px 0 0 var(--lo); }

	.task-row:hover,
	.task-row:focus-visible {
		background: var(--bg-3);
		text-decoration: none;
		color: var(--text-secondary);
	}

	.row-kind { white-space: nowrap; }

	.row-id { color: var(--text-tertiary); font-size: var(--fs-sm); }

	.row-body { min-width: 0; display: flex; flex-direction: column; gap: 3px; }

	.row-text {
		display: block;
		font-size: var(--fs-base);
		color: var(--text-primary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.row-meta {
		display: inline-flex;
		align-items: center;
		gap: 10px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		min-width: 0;
	}

	.meta-model { max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.meta-cost { color: var(--text-secondary); }

	.cancel-btn {
		border: 1px solid var(--border-subtle);
		background: none;
		color: var(--text-secondary);
		border-radius: 0;
		padding: 3px 9px;
		font-size: var(--fs-2xs);
		cursor: pointer;
	}

	.cancel-btn:hover:not(:disabled) { border-color: var(--danger-line); color: var(--danger-text); }
	.cancel-btn:disabled { opacity: 0.6; cursor: default; }

	.row-time { font-size: var(--fs-xs); color: var(--text-tertiary); text-align: right; }

	/* Mobile (project screen pattern): body wraps to two lines, model/cost/
	   duration hide, the id and absolute time give way to space. */
	@media (max-width: 640px) {
		.task-row { grid-template-columns: 48px minmax(0, 1fr) auto; }
		.row-id, .row-time { display: none; }
		.row-text { white-space: normal; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
		.row-meta { display: none; }
	}
</style>
