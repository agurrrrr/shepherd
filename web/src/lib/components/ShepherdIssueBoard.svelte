<script>
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { apiGet } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import Icon from '$lib/components/Icon.svelte';

	// Cross-project issue board (#621, split out of routes/shepherd/+page.svelte;
	// board itself from #599). GET /api/issues (cross-project). One request per
	// status column; the `counts` map rides on every response, so column headers
	// come from the first column that answers. Filters live in the URL query so
	// a reload keeps them (wiki: svelte5-select-bind-change-vs-input — selects
	// read their bound value on `change`, so filter handlers use onchange).
	//
	// #622: the board sits in its own tab and uses the full width — the
	// needs-attention panel moved to the 확인 필요 tab (page-owned). Cards ask
	// the page to open the shared IssueDetailModal through
	// `onOpenIssue({ projectName, issueId })`; when the modal changes something
	// the page bumps `refreshKey` and the list reloads.
	let { refreshKey = 0, onOpenIssue = () => {} } = $props();

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

	function initFiltersFromUrl() {
		const p = $page.url.searchParams;
		filterProjects = (p.get('project') || '').split(',').map((s) => s.trim()).filter(Boolean);
		filterType = p.get('type') || '';
		filterWatched = p.get('watched') || '';
		searchQ = p.get('q') || '';
	}

	// Copy the CURRENT query and only rewrite the board filter keys (#622).
	// Building a fresh URLSearchParams used to wipe `tab=board`, so a reload
	// after touching a filter landed back on Live Output.
	function syncFiltersToUrl() {
		const params = new URLSearchParams($page.url.searchParams);
		if (filterProjects.length) params.set('project', filterProjects.join(','));
		else params.delete('project');
		if (filterType) params.set('type', filterType);
		else params.delete('type');
		if (filterWatched) params.set('watched', filterWatched);
		else params.delete('watched');
		if (searchQ) params.set('q', searchQ);
		else params.delete('q');
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
		onOpenIssue({ projectName: issue.project_name, issueId: issue.id });
	}

	let unsubs = [];
	let sseCleanup = null;

	onMount(() => {
		initFiltersFromUrl();
		loadBoard();

		apiGet('/api/projects')
			.then((res) => {
				if (res?.data) projectChoices = res.data.filter((p) => !p.hidden).map((p) => p.name);
			})
			.catch(() => {});

		// SSE → debounced board refresh (#599): events arrive in bursts
		// (task_start then output etc.), so collapse them into one reload.
		let sseTimer = null;
		const scheduleBoardRefresh = () => {
			clearTimeout(sseTimer);
			sseTimer = setTimeout(() => {
				loadBoard();
			}, 600);
		};
		for (const ev of ['task_start', 'task_complete', 'task_fail', 'task_stop']) {
			unsubs.push(onSSE(ev, scheduleBoardRefresh)); // board counts every project's task (#599)
		}
		// Events missed while the SSE connection was down: re-read everything.
		unsubs.push(onSSE('reconnect', scheduleBoardRefresh));

		sseCleanup = () => clearTimeout(sseTimer);
	});

	onDestroy(() => {
		unsubs.forEach((fn) => fn?.());
		sseCleanup?.();
		clearTimeout(searchDebounce);
	});

	// Page-requested reloads (detail modal changed an issue). Skip the effect's
	// first run — onMount already loaded.
	let first = true;
	$effect(() => {
		refreshKey;
		if (first) {
			first = false;
			return;
		}
		loadBoard();
	});
</script>

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
</section>

<style>
	/* #623: the board sits in its own full-height tab pane (the page gives it
	   a flex column with overflow hidden). The filter rows and project chips
	   stay pinned at the top of the tab; only .col-list scrolls. */
	.block {
		display: flex;
		flex-direction: column;
		flex: 1;
		min-height: 0;
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
		background: var(--well);
		color: var(--text-secondary);
		font-family: inherit;
	}

	.proj-chip:hover { border-color: var(--border-strong); color: var(--text-primary); }

	.board-error {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 10px;
		color: var(--danger-text);
		font-size: var(--fs-xs);
	}

	.narrow-tabs { display: none; }

	.board-columns {
		display: grid;
		grid-template-columns: repeat(5, minmax(0, 1fr));
		gap: 10px;
		flex: 1;
		min-height: 0;
		align-items: stretch;
	}

	.board-col {
		display: flex;
		flex-direction: column;
		min-width: 0;
		min-height: 0;
		border: 1px solid var(--border-subtle);
		border-radius: 0;
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
		letter-spacing: var(--tracking-caps);
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
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	.col-empty {
		margin: 4px 0;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.board-card {
		padding: 8px 10px;
		border: 0;
		border-radius: 0;
		background: var(--bg-3);
		box-shadow: var(--px-ring), var(--px-bevel);
		cursor: pointer;
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
		border-radius: 0;
		font-size: var(--fs-2xs);
		line-height: 1.8;
		background: var(--well);
		color: var(--text-secondary);
	}

	.type-badge.feature { background: var(--accent-soft); color: var(--accent); }
	.type-badge.bug { background: var(--danger-soft); color: var(--danger-text); }
	.type-badge.design { background: var(--queued-soft); color: var(--queued-text); }

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
	.task-state.bad { color: var(--danger-text); }

	.more { align-self: flex-start; }

	/* #623: the board lost its 340px side panel, so it owns the whole tab.
	   Five columns stay five columns down to the mobile breakpoint — the old
	   1100px "three columns" step was sized for the split layout and left
	   dead space on a full-width screen. Below 768px (the mobile topbar's
	   own breakpoint) the status tabs pick one visible column instead.

	   #634: on these narrow screens the board no longer scrolls inside a
	   column. The page turns the whole board pane into the scroll container
	   (+page.svelte: `.tab-fill.board-pane { overflow-y: auto }`), so every
	   section here must grow to its natural content height instead of being
	   pinned to the pane height. Without `flex: 0 0 auto` on .block and
	   .board-columns, `flex:1; min-height:0` capped them at the leftover pane
	   height and the list overflow was clipped by the pane again — the bug.

	   The short-screen branch (max-height:600px AND max-width:1100px, e.g. a
	   landscape phone at 844×390) joins it: measured there, five columns left
	   only ~99px of width and the folded header squeezed every .col-list to
	   clientHeight 0 — no inner scroller could reach the list end. One status
	   tab + pane scroll is the only layout that reaches the bottom on such a
	   short viewport. Wide-but-short desktop windows (>1100px) keep the five
	   columns; their columns stay tall enough to scroll inside. */
	@media (max-width: 768px), (max-height: 600px) and (max-width: 1100px) {
		/* Narrow screens: status tabs pick one visible column. The columns stay
		   in the DOM; only the active one shows. */
		.narrow-tabs {
			display: flex;
			gap: 6px;
			flex-wrap: wrap;
			margin-bottom: 8px;
		}

		.narrow-tab {
			display: inline-flex;
			align-items: center;
			gap: 6px;
			padding: 5px 10px;
			border: 1px solid var(--border-subtle);
			border-radius: 0;
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

		/* The pane scrolls, so the section and the grid take natural height. */
		.block { flex: 0 0 auto; }
		.board-columns { flex: 0 0 auto; grid-template-columns: minmax(0, 1fr); }

		/* Kept as a flex column (not `display:block`): the col-head/col-list
		   chain stays intact and .col-list simply grows to its content height.
		   Scrolling is the pane's job here — see the note above (#634). */
		.board-col { display: none; }
		.board-col.is-active { display: flex; flex-direction: column; }

		/* No inner scroller on mobile/landscape: the list expands fully so the
		   pane's single vertical scroll reaches every card and "더 보기". */
		.col-list { overflow: visible; }

		/* ~78 project chips wrapped onto many rows ate the whole viewport on a
		   phone (#634). One horizontally scrollable row keeps them reachable and
		   leaves the height to the list. */
		.board-projects {
			flex-wrap: nowrap;
			overflow-x: auto;
			padding-bottom: 6px; /* room for the horizontal scrollbar */
			margin-bottom: 8px;
			scrollbar-width: thin;
		}

		.proj-chip { flex: 0 0 auto; white-space: nowrap; }

		/* Trim the pinned header stack so more of the list fits above the fold. */
		.board-filters { margin-bottom: 8px; }
	}
</style>
