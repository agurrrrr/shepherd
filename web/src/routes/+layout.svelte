<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { accessToken, isAuthenticated, username, systemStatus, projects, sheep, shepherdAttention } from '$lib/stores.js';
	import { logout, apiGet } from '$lib/api.js';
	import { refreshShepherdAttention } from '$lib/attention.js';
	import { connectSSE, disconnectSSE, onSSE } from '$lib/sse.js';
	import { syncPushSubscription } from '$lib/push.js';
	import { setLocale } from '$lib/i18n/index.js';
	import { goto, onNavigate } from '$app/navigation';
	import Icon from '$lib/components/Icon.svelte';
	import BrandMark from '$lib/components/BrandMark.svelte';
	import '@fontsource/press-start-2p/400.css';
	import '../app.css';

	let { children } = $props();

	let unsubscribers = [];
	let projectsExpanded = $state(false);
	let sidebarOpen = $state(false);

	// Auth guard
	$effect(() => {
		if (typeof window !== 'undefined' && $page.url.pathname !== '/login' && !$accessToken) {
			goto('/login');
		}
	});

	// Connect SSE when authenticated
	$effect(() => {
		if ($accessToken) {
			isAuthenticated.set(true);
			connectSSE();
		}
	});

	// UI language follows the server `language` setting (#662). The login
	// screen, and the first paint before /api/config answers, use the locale
	// the i18n module stored on the last visit. Fetch once per sign-in: a token
	// refresh replaces accessToken but must not refetch the config.
	let localeSynced = false;
	$effect(() => {
		if (!$accessToken) {
			localeSynced = false;
			return;
		}
		if (localeSynced) return;
		localeSynced = true;
		apiGet('/api/config')
			.then((res) => setLocale(res?.data?.language))
			.catch(() => {});
	});

	// Auto-expand projects accordion when on a project detail page
	$effect(() => {
		if ($page.url.pathname.startsWith('/projects/') && $page.url.pathname !== '/projects') {
			projectsExpanded = true;
		}
	});

	// Sidebar stats from systemStatus store
	let stats = $derived({
		working: $systemStatus?.tasks?.running ?? 0,
		pending: $systemStatus?.tasks?.pending ?? 0,
		completed: $systemStatus?.tasks?.completed ?? 0,
		failed: $systemStatus?.tasks?.failed ?? 0
	});

	// Shepherd (목자) state for the sidebar dot + attention badge (#598/#599).
	// worker.List()/systemStatus exclude the manager sheep, so the sidebar asks
	// /api/shepherd/status directly. The attention count comes from the shared
	// shepherdAttention store — the Shepherd page's panel keeps it fresh too.
	let shepherdWorking = $state(false);

	async function refreshShepherdStatus() {
		const res = await apiGet('/api/shepherd/status');
		if (res?.data) shepherdWorking = res.data.status === 'working';
	}

	// SSE bursts (task_start → output → complete) collapse into one attention
	// refresh — same debounce window as the board (#599).
	let attentionTimer = null;
	function scheduleAttentionRefresh() {
		clearTimeout(attentionTimer);
		attentionTimer = setTimeout(() => refreshShepherdAttention(), 600);
	}

	// Share of finished tasks that succeeded — drives the sidebar flock meter.
	let finished = $derived(stats.completed + stats.failed);
	let successRate = $derived(finished > 0 ? stats.completed / finished : 0);
	let initial = $derived(($username || '?').trim().charAt(0).toUpperCase());

	// Cross-fade the main pane between routes (sidebar stays put). Query-only
	// changes (tabs, filters) skip the transition so they feel instant.
	// The main pane is named only while a transition runs: a view-transition-name
	// makes the element a stacking context, which would otherwise trap every
	// modal overlay inside <main> underneath the fixed sidebar.
	onNavigate((navigation) => {
		if (typeof document === 'undefined' || !document.startViewTransition) return;
		if (navigation.from?.url.pathname === navigation.to?.url.pathname) return;
		const root = document.documentElement;
		root.classList.add('vt-page');
		return new Promise((resolve) => {
			const transition = document.startViewTransition(async () => {
				resolve();
				await navigation.complete;
			});
			transition.finished.finally(() => root.classList.remove('vt-page'));
		});
	});

	// Full-height app routes (project workspace, shepherd console #622) trade
	// page padding for room.
	let fillRoute = $derived(/^\/(projects\/[^/]+|shepherd)\/?$/.test($page.url.pathname));

	// Projects flagged hidden are left out of the sidebar (still reachable via /projects)
	let visibleProjects = $derived(($projects || []).filter(p => !p.hidden));

	// Helper: get sheep status for a project
	function getSheepStatus(projectName) {
		const sheepList = $sheep || [];
		const s = sheepList.find(s => s.project === projectName);
		return s?.status || 'idle';
	}

	onMount(async () => {
		if ($accessToken) {
			// Load independently: a slow endpoint (e.g. task counters) must not
			// delay the sidebar project list.
			apiGet('/api/projects')
				.then(res => { if (res?.data) projects.set(res.data); })
				.catch(() => {});
			apiGet('/api/sheep')
				.then(res => { if (res?.data) sheep.set(res.data); })
				.catch(() => {});
			apiGet('/api/system/status')
				.then(res => { if (res?.data) systemStatus.set(res.data); })
				.catch(() => {});
			refreshShepherdStatus().catch(() => {});
			refreshShepherdAttention().catch(() => {});
		}

		// Refresh stats + sheep status on SSE events
		unsubscribers.push(
			onSSE('status_change', async (data) => {
				await refreshStatus();
				// Update sheep store for sidebar status dots
				sheep.update(list => list.map(s =>
					s.name === data.sheep_name ? { ...s, status: data.status } : s
				));
				// The manager sheep is not in the sheep store — refresh its
				// sidebar dot from the dedicated endpoint instead.
				if (data.sheep_name === 'shepherd') refreshShepherdStatus();
			}),
			onSSE('provider_change', (data) => {
				// Update sheep store when provider changes (rate limit fallback / manual change)
				sheep.update(list => list.map(s =>
					s.name === data.sheep_name ? { ...s, provider: data.provider } : s
				));
			}),
			onSSE('sheep_renamed', (data) => {
				// #638: a rename changes the row's key, so patch both stores in
				// place instead of refetching — sidebar dots key by project name,
				// and the flock view keys by sheep name.
				if (!data?.old_name || !data?.new_name) return;
				sheep.update(list => list.map(s =>
					s.name === data.old_name
						? { ...s, name: data.new_name, project: data.project || s.project }
						: s
				));
				projects.update(list => list.map(p =>
					data.project && p.name === data.project
						? { ...p, sheep: data.new_name }
						: (p.sheep === data.old_name ? { ...p, sheep: data.new_name } : p)
				));
			}),
			onSSE('task_start', () => { refreshStatus(); refreshShepherdStatus(); scheduleAttentionRefresh(); }),
			onSSE('task_complete', () => { refreshStatus(); refreshShepherdStatus(); scheduleAttentionRefresh(); }),
			onSSE('task_fail', () => { refreshStatus(); refreshShepherdStatus(); scheduleAttentionRefresh(); })
		);

		syncPushSubscription().catch(() => {});

		return () => disconnectSSE();
	});

	onDestroy(() => {
		unsubscribers.forEach(fn => fn?.());
		clearTimeout(attentionTimer);
	});

	async function refreshStatus() {
		const res = await apiGet('/api/system/status');
		if (res?.data) systemStatus.set(res.data);
	}
</script>

{#snippet navLink(href, icon, label, active, dot = '', badge = 0, badgeHref = '')}
	<!-- #623: badgeHref turns the count chip into its own link (the Shepherd
	     row's 확인 필요 badge opens ?tab=attention). The chip cannot nest
	     inside the nav <a>, so it sits next to it in a row wrapper. -->
	<div class="nav-row">
		<a {href} class="nav-item" class:active aria-current={active ? 'page' : undefined}>
			<Icon name={icon} size={17} stroke={1.75} />
			<span>{label}</span>
			{#if dot || (badge > 0 && !badgeHref)}
				<span class="nav-tail">
					{#if dot}
						<span class="status-dot {dot}" aria-hidden="true"></span>
					{/if}
					{#if badge > 0 && !badgeHref}
						<span class="nav-badge mono num" title="{badge}개 확인 필요">{badge}</span>
					{/if}
				</span>
			{/if}
		</a>
		{#if badge > 0 && badgeHref}
			<a class="nav-badge nav-badge-link mono num" href={badgeHref}
				title="{badge}개 확인 필요 — 확인 필요 탭에서 열기"
				aria-label="확인 필요 {badge}건 — 확인 필요 탭에서 열기">{badge}</a>
		{/if}
	</div>
{/snippet}

{#if $page.url.pathname === '/login'}
	{@render children?.()}
{:else if $accessToken}
	<div class="app-layout">
		<!-- Mobile top bar -->
		<header class="mobile-topbar">
			<button class="mobile-toggle"
				aria-label={sidebarOpen ? 'Close navigation' : 'Open navigation'}
				aria-expanded={sidebarOpen}
				onclick={() => sidebarOpen = !sidebarOpen}>
				<Icon name={sidebarOpen ? 'x' : 'menu'} size={20} />
			</button>
			<a href="/" class="mobile-brand">
				<BrandMark size={24} live={stats.working > 0} />
				<span class="brand-word">Shepherd</span>
			</a>
			{#if stats.working > 0}
				<span class="mobile-live" title="Working sheep">
					<span class="live-dot"></span>{stats.working}
				</span>
			{/if}
		</header>

		<!-- Mobile backdrop -->
		{#if sidebarOpen}
			<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
			<div class="sidebar-backdrop" onclick={() => sidebarOpen = false}></div>
		{/if}

		<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
		<aside class="sidebar" class:open={sidebarOpen} onclick={() => sidebarOpen = false}>
			<a href="/" class="sidebar-brand">
				<BrandMark size={30} live={stats.working > 0} />
				<span class="brand-word">Shepherd</span>
			</a>

			<nav class="sidebar-nav" aria-label="Primary">
				<span class="nav-label">Watch</span>
				{@render navLink('/', 'layout-dashboard', 'Dashboard', $page.url.pathname === '/')}
				{@render navLink('/shepherd', 'sheep', 'Shepherd', $page.url.pathname.startsWith('/shepherd'), shepherdWorking ? 'working' : '', $shepherdAttention, '/shepherd?tab=attention')}

				<!-- Projects accordion -->
				<div class="nav-group">
					<button type="button" class="nav-item nav-group-toggle"
						class:active={$page.url.pathname.startsWith('/projects')}
						aria-expanded={projectsExpanded}
						aria-controls="projects-submenu"
						onclick={(e) => { e.stopPropagation(); projectsExpanded = !projectsExpanded; }}>
						<Icon name="folder" size={17} stroke={1.75} />
						<span class="nav-group-label">Projects</span>
						<span class="nav-count mono">{visibleProjects.length}</span>
						<span class="nav-chevron" class:expanded={projectsExpanded}>
							<Icon name="chevron-right" size={14} />
						</span>
					</button>

					{#if projectsExpanded}
						<div class="nav-sub-items" id="projects-submenu">
							<a href="/projects" class="nav-sub-item manage"
								class:active={$page.url.pathname === '/projects'}
								onclick={(e) => e.stopPropagation()}>
								<Icon name="settings" size={13} />
								<span>Manage all</span>
							</a>
							{#each visibleProjects as p}
								<a href="/projects/{encodeURIComponent(p.name)}" class="nav-sub-item"
									class:active={decodeURIComponent($page.url.pathname) === `/projects/${p.name}`}
									onclick={(e) => e.stopPropagation()}>
									<span class="status-dot {getSheepStatus(p.name)}"></span>
									<span class="nav-sub-label" title={p.name}>{p.name}</span>
								</a>
							{/each}
							{#if visibleProjects.length === 0}
								<span class="nav-sub-empty">No projects</span>
							{/if}
						</div>
					{/if}
				</div>

				{@render navLink('/tasks', 'list-checks', 'Tasks', $page.url.pathname.startsWith('/tasks'))}
				{@render navLink('/schedules', 'alarm-clock', 'Schedules', $page.url.pathname.startsWith('/schedules'))}

				<span class="nav-label">Configure</span>
				{@render navLink('/skills', 'book-open', 'Skills', $page.url.pathname.startsWith('/skills'))}
				{@render navLink('/settings', 'settings', 'Settings', $page.url.pathname.startsWith('/settings'))}
			</nav>

			<!-- Flock meter -->
			<section class="flock-meter" aria-label="Task statistics">
				<div class="meter-head">
					<span class="meter-title">Flock</span>
					<span class="meter-rate mono" title="Completed ÷ (completed + failed)">
						{Math.round(successRate * 100)}<span class="pct">%</span> ok
					</span>
				</div>
				<div class="meter-bar" aria-hidden="true">
					<span class="meter-fill" style="--ratio: {successRate}"></span>
				</div>
				<dl class="meter-grid">
					<div class="meter-cell" data-tone="live">
						<dt>Working</dt>
						<dd class="mono">{#if stats.working > 0}<span class="live-dot"></span>{/if}{stats.working}</dd>
					</div>
					<div class="meter-cell" data-tone="queued">
						<dt>Pending</dt>
						<dd class="mono">{stats.pending}</dd>
					</div>
					<div class="meter-cell" data-tone="success">
						<dt>Done</dt>
						<dd class="mono">{stats.completed.toLocaleString()}</dd>
					</div>
					<div class="meter-cell" data-tone="danger">
						<dt>Failed</dt>
						<dd class="mono">{stats.failed.toLocaleString()}</dd>
					</div>
				</dl>
			</section>

			<div class="sidebar-footer">
				<div class="user-info">
					<span class="avatar" aria-hidden="true">{initial}</span>
					<span class="user-name">{$username || 'anonymous'}</span>
				</div>
				<button class="btn-logout" aria-label="Sign out" title="Sign out" onclick={logout}>
					<Icon name="log-out" size={15} />
				</button>
			</div>
		</aside>

		<main class="main-content" class:fill={fillRoute}>
			{@render children?.()}
		</main>
	</div>
{:else}
	<div class="loading-screen">
		<BrandMark size={40} live />
	</div>
{/if}

<style>
	.app-layout {
		display: flex;
		min-height: 100vh;
	}

	/* ================= Sidebar ================= */
	.sidebar {
		width: var(--sidebar-w);
		background: var(--bg-base);
		/* Pixel edge: a black rule and a light rule outside the right side */
		box-shadow: 3px 0 0 0 #000, 6px 0 0 0 var(--hi);
		display: flex;
		flex-direction: column;
		position: fixed;
		top: 0;
		left: 0;
		bottom: 0;
		z-index: 50;
		overflow-y: auto;
		scrollbar-width: thin;
	}

	.sidebar-backdrop {
		display: none;
		position: fixed;
		inset: 0;
		background: var(--bg-overlay);
		z-index: 49;
		animation: fade-in var(--dur-1) steps(2) backwards;
	}

	.sidebar-brand {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 22px 20px 18px;
		color: var(--text-primary);
		text-decoration: none;
	}

	.sidebar-brand:hover {
		text-decoration: none;
		color: var(--text-primary);
	}

	/* 16px: Press Start 2P at 2×; 24px would push past the 248px sidebar. */
	.brand-word {
		font-family: var(--font-display);
		font-size: 16px;
		line-height: 1;
		letter-spacing: 0;
		padding-top: 2px;
		color: var(--highlight);
		text-shadow: 2px 2px 0 var(--p-plum);
	}

	.sidebar-nav {
		flex: 1;
		padding: 4px 12px 12px;
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.nav-label {
		font-family: var(--font-label);
		font-size: var(--fs-label);
		font-weight: var(--fw-regular);
		text-transform: uppercase;
		letter-spacing: 0;
		color: var(--accent);
		padding: 14px 10px 6px;
	}

	.nav-label:first-child {
		padding-top: 6px;
	}

	.nav-item {
		position: relative;
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 7px 10px;
		border-radius: 0;
		color: var(--text-secondary);
		text-decoration: none;
		font-size: var(--fs-base);
		font-weight: var(--fw-regular);
		letter-spacing: 0;
		/* Reset for <button> variant (nav-group-toggle) */
		background: none;
		border: none;
		width: 100%;
		text-align: left;
		font-family: inherit;
	}

	.nav-item:hover {
		background: var(--bg-2);
		color: var(--text-primary);
		text-decoration: none;
	}

	/* "You are here": card face, yellow words, outline with a light bevel,
	   and a blinking square at the right end. */
	.nav-item.active {
		background: var(--bg-2);
		color: var(--highlight);
		box-shadow: var(--px-ring), inset 3px 3px 0 0 var(--hi);
	}

	.nav-item.active::after {
		content: '';
		position: absolute;
		right: 10px;
		top: calc(50% - 3px);
		width: 6px;
		height: 6px;
		background: var(--highlight);
		animation: px-blink 1s steps(1) infinite;
	}

	/* The marker would sit on top of the tail (status dot, badge) */
	.nav-item.active:has(.nav-tail)::after,
	.nav-group-toggle.active::after {
		display: none;
	}

	.nav-item :global(.icon) {
		flex-shrink: 0;
		color: currentColor;
	}

	/* Projects accordion */
	.nav-group {
		display: flex;
		flex-direction: column;
	}

	.nav-group-toggle {
		cursor: pointer;
		user-select: none;
	}

	.nav-group-label {
		flex: 1;
	}

	.nav-count {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		padding: 0 6px;
		border-radius: 0;
		background: var(--well);
		box-shadow: inset 0 0 0 2px #000;
		line-height: 18px;
	}

	/* Attention badge slot on nav items (Shepherd row, #598; badge became a
	   link to the 확인 필요 tab in #623) */
	.nav-row {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.nav-row .nav-item { min-width: 0; }

	.nav-tail {
		margin-left: auto;
		display: inline-flex;
		align-items: center;
		gap: 7px;
	}

	.nav-badge {
		margin-left: auto;
		font-size: var(--fs-2xs);
		font-weight: var(--fw-regular);
		color: #000;
		background: var(--danger);
		box-shadow: var(--px-ring-2);
		padding: 0 6px;
		border-radius: 0;
		line-height: 18px;
	}

	/* #623: the badge is its own link — flex-shrink: 0 keeps it beside the
	   (possibly truncated) label instead of collapsing. */
	.nav-badge-link {
		flex-shrink: 0;
		margin-left: auto;
		text-decoration: none;
		cursor: pointer;
	}

	.nav-badge-link:hover { background: var(--danger-hover); color: #000; text-decoration: none; }

	.nav-chevron {
		display: inline-flex;
		color: var(--text-tertiary);
	}

	.nav-chevron.expanded {
		transform: rotate(90deg);
	}

	.nav-sub-items {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin: 4px 0 6px 18px;
		padding-left: 10px;
		border-left: 3px solid var(--bg-2);
		animation: rise-in var(--dur-2) var(--ease-out) backwards;
	}

	.nav-sub-item {
		display: flex;
		align-items: center;
		gap: 9px;
		padding: 4px 10px;
		border-radius: 0;
		color: var(--text-secondary);
		text-decoration: none;
		font-size: var(--fs-sm);
	}

	.nav-sub-item:hover {
		background: var(--bg-2);
		color: var(--text-primary);
		text-decoration: none;
	}

	.nav-sub-item.active {
		background: var(--bg-2);
		color: var(--highlight);
	}

	.nav-sub-item.manage {
		color: var(--text-tertiary);
		font-size: var(--fs-xs);
	}

	.nav-sub-item.manage:hover {
		color: var(--text-primary);
	}

	.nav-sub-item.manage.active {
		color: var(--highlight);
	}

	.nav-sub-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 160px;
	}

	.nav-sub-empty {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		padding: 4px 10px;
		font-style: normal;
	}

	/* Status dot: a square in a 2px black ring */
	.status-dot {
		width: 6px;
		height: 6px;
		margin: 2px;
		border-radius: 0;
		flex-shrink: 0;
		background: var(--p-dgray);
		box-shadow: var(--px-ring-2);
	}

	.status-dot.idle {
		background: var(--p-dgray);
	}

	.status-dot.working {
		background: var(--live);
		animation: px-blink 1s steps(1) infinite;
	}

	.status-dot.error {
		background: var(--danger);
	}

	.live-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		margin-right: 6px;
		border-radius: 0;
		background: var(--live);
		vertical-align: middle;
		animation: px-blink 1s steps(1) infinite;
	}

	/* ================= Flock meter ================= */
	.flock-meter {
		margin: 3px 15px 15px;
		padding: 14px 14px 12px;
		border-radius: 0;
		background: var(--bg-2);
		box-shadow: var(--px-ring), var(--px-bevel);
	}

	.meter-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		margin-bottom: 10px;
	}

	.meter-title {
		font-family: var(--font-label);
		font-size: var(--fs-label);
		line-height: 1;
		color: var(--highlight);
		text-transform: uppercase;
	}

	.meter-rate {
		font-size: var(--fs-2xs);
		color: var(--success);
	}

	.meter-rate .pct {
		color: var(--success);
	}

	/* Black track, chunky green cells */
	.meter-bar {
		position: relative;
		height: 12px;
		border-radius: 0;
		background: #000;
		overflow: hidden;
		margin-bottom: 12px;
	}

	.meter-fill {
		position: absolute;
		top: 2px;
		bottom: 2px;
		left: 2px;
		/* Grows by width, not scaleX, so the cells keep their size */
		width: calc((100% - 4px) * var(--ratio, 0));
		background: repeating-linear-gradient(90deg, var(--success) 0 6px, transparent 6px 8px);
		transition: width 0.8s steps(8);
	}

	.meter-grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}

	.meter-cell {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
		padding: 6px 8px;
		background: var(--well);
		box-shadow: inset 0 0 0 2px #000;
	}

	.meter-cell dt {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		letter-spacing: 0;
	}

	.meter-cell dd {
		font-size: var(--fs-md);
		font-weight: var(--fw-regular);
		line-height: 1.5;
		color: var(--text-primary);
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.meter-cell[data-tone="live"] dd { color: var(--live); }
	.meter-cell[data-tone="queued"] dd { color: var(--queued-text); }
	.meter-cell[data-tone="success"] dd { color: var(--success); }
	.meter-cell[data-tone="danger"] dd { color: var(--danger-text); }

	/* ================= Footer ================= */
	.sidebar-footer {
		padding: 12px 14px 14px 16px;
		border-top: 3px solid #000;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
	}

	.user-info {
		display: inline-flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
	}

	.avatar {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 28px;
		height: 28px;
		flex-shrink: 0;
		margin: 2px;
		border-radius: 0;
		font-family: var(--font-display);
		font-size: var(--fs-label);
		color: #000;
		background: var(--accent);
		box-shadow: var(--px-ring-2);
	}

	.user-name {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.btn-logout {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 30px;
		height: 30px;
		background: none;
		border: 0;
		color: var(--text-secondary);
		border-radius: 0;
	}

	.btn-logout:hover {
		color: var(--highlight);
		background: var(--bg-2);
	}

	/* ================= Main ================= */
	.main-content {
		flex: 1;
		min-width: 0;
		margin-left: var(--sidebar-w);
		padding: var(--page-pad-top) clamp(20px, 3.4vw, 48px) var(--page-pad-bottom);
		overflow-x: hidden;
	}

	:global(html.vt-page) .main-content {
		view-transition-name: page;
	}

	.main-content.fill {
		--page-pad-top: 26px;
		--page-pad-bottom: 20px;
	}

	.loading-screen {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 100vh;
	}

	/* ================= Mobile top bar ================= */
	.mobile-topbar {
		display: none;
	}

	.mobile-toggle {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		background: transparent;
		border: 0;
		color: var(--text-secondary);
		padding: 0;
		border-radius: 0;
		width: 40px;
		height: 40px;
	}

	.mobile-toggle:hover {
		background: var(--bg-2);
		color: var(--highlight);
	}

	.mobile-brand {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		color: var(--text-primary);
		text-decoration: none;
	}

	.mobile-brand:hover {
		color: var(--text-primary);
		text-decoration: none;
	}

	.mobile-brand .brand-word {
		font-size: 16px;
	}

	.mobile-live {
		margin-left: auto;
		display: inline-flex;
		align-items: center;
		margin: 2px;
		padding: 1px 7px;
		border-radius: 0;
		background: var(--live);
		box-shadow: var(--px-ring-2);
		color: #000;
		font-family: var(--font-mono);
		font-size: var(--fs-xs);
	}

	/* === Responsive ===
	   Breakpoints:
	   - ≤480px : Titan 2 (1440x1440 @ ~3-4 DPR ≈ 360-480 CSS px) and small phones
	   - ≤768px : tablet portrait / standard mobile
	*/
	@media (max-width: 768px) {
		.mobile-topbar {
			display: flex;
			align-items: center;
			gap: 12px;
			position: fixed;
			top: 0;
			left: 0;
			right: 0;
			height: var(--mobile-topbar-h);
			padding: 0 12px;
			z-index: 45;
			background: var(--bg-base);
			box-shadow: 0 3px 0 0 #000;
		}

		.sidebar-backdrop {
			display: block;
		}

		.sidebar {
			transform: translateX(-100%);
			transition: transform var(--dur-2) steps(4);
			box-shadow: 3px 0 0 0 #000, 6px 0 0 0 var(--hi), var(--shadow-3);
		}

		.sidebar.open {
			transform: translateX(0);
		}

		.main-content {
			margin-left: 0;
			padding: var(--space-4);
			padding-top: calc(var(--mobile-topbar-h) + var(--page-pad-top));
		}

		.nav-item {
			padding: 10px 10px;
			min-height: 40px;
		}

		.nav-sub-item {
			padding: var(--space-2) 10px;
			min-height: 36px;
		}

		.nav-sub-label {
			max-width: none;
		}

		/* The page sizes itself with --page-pad-bottom; the mobile padding
		   shorthand (--space-4/--space-3) would otherwise overflow it. */
		.main-content.fill {
			--page-pad-top: 12px;
			--page-pad-bottom: 12px;
			padding-bottom: var(--page-pad-bottom);
		}
	}

	/* Short screens: the project workspace is a fixed-height app page, so
	   its padding comes straight out of the output pane. */
	@media (max-height: 600px) {
		.main-content.fill {
			--page-pad-top: 8px;
			--page-pad-bottom: 8px;
		}

		.mobile-toggle {
			width: 36px;
			height: 36px;
		}

		.mobile-brand .brand-word {
			font-size: 16px;
		}
	}

	/* Titan 2 (1440x1440 square @ ~3 DPR ≈ 480 CSS px) and very small screens */
	@media (max-width: 480px) {
		.sidebar {
			width: min(288px, 88vw);
		}

		.main-content {
			padding: var(--space-3);
			padding-top: calc(var(--mobile-topbar-h) + var(--page-pad-top));
		}

		.sidebar-brand {
			padding: 18px 16px 14px;
		}
	}
</style>
