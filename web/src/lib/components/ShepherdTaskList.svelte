<script>
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { timeAgo, firstLine, fmtCost } from '$lib/shepherdFormat.js';
	import { t, translate, enumLabel, formatNumber } from '$lib/i18n/index.js';
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
	// Chip ids stay as the URL filter values; the labels come from the dictionary
	// (`tasklist.kind.*`) so a locale switch re-labels them without a reload.
	const KIND_CHIPS = [
		{ id: '', key: 'all' },
		{ id: 'command', key: 'command' },
		{ id: 'review', key: 'review' },
		{ id: 'patrol', key: 'patrol' }
	];
	const STATUS_OPTIONS = [
		{ id: '', key: '' },
		{ id: 'pending', key: 'pending' },
		{ id: 'running', key: 'running' },
		{ id: 'completed', key: 'completed' },
		{ id: 'failed', key: 'failed' },
		{ id: 'stopped', key: 'stopped' }
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
			loadError = translate('tasklist.loadError');
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
		if (!confirm(translate('tasklist.cancelConfirm', { id: taskId }))) return;
		cancellingId = taskId;
		try {
			const res = await apiPost(`/api/tasks/${taskId}/cancel`, {});
			if (res?.success) {
				await loadTasks();
			} else {
				alert(res?.message || translate('tasklist.cancelFailed'));
			}
		} catch (err) {
			alert(err?.message || translate('tasklist.cancelFailed'));
		} finally {
			cancellingId = null;
		}
	}

	function rowHref(task) {
		return `/tasks/${task.id}?from=shepherd&tab=history`;
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
	function durationSec(task) {
		if (!task.started_at || !task.completed_at) return 0;
		const a = new Date(String(task.started_at).replace(' ', 'T')).getTime();
		const b = new Date(String(task.completed_at).replace(' ', 'T')).getTime();
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

	function rowText(task) {
		if (task.status === 'failed') return firstLine(task.error || task.summary || task.prompt);
		return firstLine(task.summary || task.prompt || task.error);
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

<section class="tasklist-tab" aria-label={$t('tasklist.aria.section')}>
	<div class="filter-bar">
		<input
			type="text"
			class="input input-search filter-search"
			placeholder={$t('tasklist.searchPlaceholder')}
			bind:value={searchQ}
			oninput={onSearchInput}
			aria-label={$t('tasklist.searchAria')}
		/>

		<div class="kind-chips" role="group" aria-label={$t('tasklist.kindGroupAria')}>
			{#each KIND_CHIPS as k (k.id)}
				<button class="chip kind-chip" class:chip-accent={filterKind === k.id}
					aria-pressed={filterKind === k.id}
					onclick={() => { filterKind = k.id; applyFilters(); }}>
					{$t(`tasklist.kind.${k.key}`)}
				</button>
			{/each}
		</div>

		<!-- bind:value on <select> fires on `change`, so the handler is onchange too
		     (wiki: svelte5-select-bind-change-vs-input). -->
		<select class="input status-select" bind:value={filterStatus}
			onchange={() => applyFilters()} aria-label={$t('tasklist.statusFilterAria')}>
			{#each STATUS_OPTIONS as o (o.id)}
				<option value={o.id}>{o.key === '' ? $t('tasklist.statusAll') : $t(`tasklist.status.${o.key}`)}</option>
			{/each}
		</select>

		<span class="filter-count mono num" aria-live="polite">{$t('tasklist.count', { count: formatNumber(total) })}</span>

		<button class="btn btn-sm btn-ghost" onclick={async () => { await loadTasks(); onRefresh(); }}>
			<Icon name="refresh-cw" size={13} /> {$t('common.refresh')}
		</button>
	</div>

	{#if newOnFirstPage}
		<button class="new-notice" onclick={goToFirstPage}>
			<Icon name="chevron-up" size={13} /> {$t('tasklist.newOnFirstPage')}
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
				title={searchQ || filterKind || filterStatus ? $t('tasklist.emptyFilteredTitle') : $t('tasklist.emptyTitle')}
				description={searchQ || filterKind || filterStatus
					? $t('tasklist.emptyFilteredDesc')
					: $t('tasklist.emptyDesc')} />
		</div>
	{:else}
		<div class="task-table">
			{#each tasks as task, i (task.id)}
				{@const dur = durationSec(task)}
				<a class="task-row reveal" style="--i: {i}" href={rowHref(task)} aria-label={$t('tasklist.rowAria', { id: task.id })}>
					<span class="row-kind chip chip-{task.shepherd_kind === 'command' ? 'accent' : task.shepherd_kind === 'review' ? 'queued' : 'info'}">
						{enumLabel($t, 'tasklist.kind', task.shepherd_kind)}
					</span>
					<span class="row-id mono num">#{task.id}</span>
					<div class="row-body">
						<span class="row-text">{rowText(task)}</span>
						<span class="row-meta mono">
							{#if task.model}<span class="meta-model" title={task.model}>{truncateModel(task.model)}</span>{/if}
							{#if task.cost_usd > 0}<span class="meta-cost">{fmtCost(task.cost_usd)}</span>{/if}
							{#if dur > 0}<span class="meta-dur" title={$t('tasklist.durationTitle')}>{formatDuration(dur)}</span>{/if}
						</span>
					</div>
					<StatusBadge status={task.status} />
					{#if task.status === 'pending'}
						<button class="cancel-btn" onclick={(e) => cancelPendingTask(e, task.id)}
							disabled={cancellingId === task.id} aria-label={$t('tasklist.cancelAria', { id: task.id })}>
							{cancellingId === task.id ? '…' : 'Cancel'}
						</button>
					{/if}
					<span class="row-time mono num" title={task.created_at}>{timeAgo(task.created_at)}</span>
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
