<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { accessToken, isAuthenticated, username, systemStatus, projects, sheep } from '$lib/stores.js';
	import { logout, apiGet } from '$lib/api.js';
	import { connectSSE, disconnectSSE, onSSE } from '$lib/sse.js';
	import { syncPushSubscription } from '$lib/push.js';
	import { goto, onNavigate } from '$app/navigation';
	import Icon from '$lib/components/Icon.svelte';
	import BrandMark from '$lib/components/BrandMark.svelte';
	import '@fontsource-variable/geist';
	import '@fontsource-variable/geist-mono';
	import '@fontsource/instrument-serif/400.css';
	import '@fontsource/instrument-serif/400-italic.css';
	import 'pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css';
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

	// Full-height app routes (project workspace) trade page padding for room.
	let fillRoute = $derived(/^\/projects\/[^/]+\/?$/.test($page.url.pathname));

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
		}

		// Refresh stats + sheep status on SSE events
		unsubscribers.push(
			onSSE('status_change', async (data) => {
				await refreshStatus();
				// Update sheep store for sidebar status dots
				sheep.update(list => list.map(s =>
					s.name === data.sheep_name ? { ...s, status: data.status } : s
				));
			}),
			onSSE('provider_change', (data) => {
				// Update sheep store when provider changes (rate limit fallback / manual change)
				sheep.update(list => list.map(s =>
					s.name === data.sheep_name ? { ...s, provider: data.provider } : s
				));
			}),
			onSSE('task_start', refreshStatus),
			onSSE('task_complete', refreshStatus),
			onSSE('task_fail', refreshStatus)
		);

		syncPushSubscription().catch(() => {});

		return () => disconnectSSE();
	});

	onDestroy(() => {
		unsubscribers.forEach(fn => fn?.());
	});

	async function refreshStatus() {
		const res = await apiGet('/api/system/status');
		if (res?.data) systemStatus.set(res.data);
	}
</script>

{#snippet navLink(href, icon, label, active)}
	<a {href} class="nav-item" class:active aria-current={active ? 'page' : undefined}>
		<Icon name={icon} size={17} stroke={1.75} />
		<span>{label}</span>
	</a>
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
		background:
			linear-gradient(180deg, rgba(244, 184, 96, 0.035), transparent 220px),
			rgba(12, 12, 14, 0.82);
		backdrop-filter: blur(18px) saturate(140%);
		-webkit-backdrop-filter: blur(18px) saturate(140%);
		border-right: 1px solid var(--border-subtle);
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
		background: rgba(5, 5, 6, 0.6);
		z-index: 49;
		backdrop-filter: blur(4px);
		-webkit-backdrop-filter: blur(4px);
		animation: fade-in var(--dur-2) ease backwards;
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

	.brand-word {
		font-family: var(--font-display);
		font-size: 27px;
		line-height: 1;
		letter-spacing: -0.01em;
		padding-top: 2px;
	}

	.sidebar-nav {
		flex: 1;
		padding: 4px 12px 12px;
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.nav-label {
		font-family: var(--font-mono);
		font-size: 10px;
		font-weight: var(--fw-medium);
		text-transform: uppercase;
		letter-spacing: var(--tracking-caps);
		color: var(--text-tertiary);
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
		border-radius: var(--radius);
		color: var(--text-secondary);
		text-decoration: none;
		font-size: var(--fs-base);
		font-weight: var(--fw-medium);
		letter-spacing: -0.005em;
		transition: background var(--dur-1) ease, color var(--dur-1) ease;
		/* Reset for <button> variant (nav-group-toggle) */
		background: none;
		border: none;
		width: 100%;
		text-align: left;
		font-family: inherit;
	}

	.nav-item:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
		text-decoration: none;
	}

	.nav-item.active {
		background:
			linear-gradient(90deg, rgba(244, 184, 96, 0.11), rgba(244, 184, 96, 0.02)),
			rgba(237, 230, 218, 0.03);
		color: var(--text-primary);
		box-shadow: inset 0 0 0 1px rgba(244, 184, 96, 0.12);
	}

	.nav-item.active::before {
		content: '';
		position: absolute;
		left: -12px;
		top: 8px;
		bottom: 8px;
		width: 3px;
		border-radius: 0 3px 3px 0;
		background: var(--accent);
		box-shadow: 0 0 12px var(--accent-glow);
	}

	.nav-item :global(.icon) {
		flex-shrink: 0;
		color: currentColor;
		opacity: 0.8;
		transition: color var(--dur-1) ease, opacity var(--dur-1) ease;
	}

	.nav-item.active :global(.icon) {
		opacity: 1;
		color: var(--accent);
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
		font-size: 10.5px;
		color: var(--text-tertiary);
		padding: 0 6px;
		border-radius: var(--radius-full);
		background: rgba(237, 230, 218, 0.05);
		line-height: 18px;
	}

	.nav-chevron {
		display: inline-flex;
		transition: transform var(--dur-2) var(--ease-out);
		color: var(--text-tertiary);
	}

	.nav-chevron.expanded {
		transform: rotate(90deg);
	}

	.nav-sub-items {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: 1px;
		margin: 2px 0 6px 18px;
		padding-left: 10px;
		border-left: 1px solid var(--border-subtle);
		animation: rise-in var(--dur-2) var(--ease-out) backwards;
	}

	.nav-sub-item {
		display: flex;
		align-items: center;
		gap: 9px;
		padding: 4px 10px;
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		text-decoration: none;
		font-size: var(--fs-sm);
		transition: background var(--dur-1) ease, color var(--dur-1) ease;
	}

	.nav-sub-item:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
		text-decoration: none;
	}

	.nav-sub-item.active {
		background: rgba(244, 184, 96, 0.08);
		color: var(--text-primary);
	}

	.nav-sub-item.manage {
		color: var(--text-tertiary);
		font-size: var(--fs-xs);
	}

	.nav-sub-item.manage:hover,
	.nav-sub-item.manage.active {
		color: var(--text-primary);
	}

	.nav-sub-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 160px;
	}

	.nav-sub-empty {
		font-size: 12px;
		color: var(--text-tertiary);
		padding: 4px 10px;
		font-style: italic;
	}

	/* Status dot */
	.status-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.status-dot.idle {
		background: rgba(237, 230, 218, 0.22);
	}

	.status-dot.working {
		background: var(--live);
		animation: pulse-ring 1.8s var(--ease-out) infinite;
	}

	.status-dot.error {
		background: var(--danger);
		box-shadow: 0 0 6px rgba(255, 107, 97, 0.6);
	}

	.live-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		margin-right: 6px;
		border-radius: 50%;
		background: var(--live);
		vertical-align: middle;
		animation: pulse-ring 1.8s var(--ease-out) infinite;
	}

	/* ================= Flock meter ================= */
	.flock-meter {
		margin: 0 12px 12px;
		padding: 14px 14px 12px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		background:
			radial-gradient(120% 90% at 0% 0%, rgba(95, 224, 194, 0.05), transparent 60%),
			rgba(237, 230, 218, 0.02);
	}

	.meter-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		margin-bottom: 10px;
	}

	.meter-title {
		font-family: var(--font-display);
		font-style: italic;
		font-size: 19px;
		line-height: 1;
		color: var(--text-primary);
	}

	.meter-rate {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.meter-rate .pct {
		color: var(--text-tertiary);
	}

	.meter-bar {
		position: relative;
		height: 4px;
		border-radius: 4px;
		background: rgba(255, 107, 97, 0.5);
		overflow: hidden;
		margin-bottom: 12px;
	}

	.meter-fill {
		position: absolute;
		inset: 0;
		background: linear-gradient(90deg, #7fc46a, var(--success));
		transform-origin: left center;
		transform: scaleX(var(--ratio, 0));
		transition: transform 0.8s var(--ease-out);
		border-right: 2px solid var(--bg-2);
	}

	.meter-grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px 10px;
	}

	.meter-cell {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
	}

	.meter-cell dt {
		font-size: 10.5px;
		color: var(--text-tertiary);
		letter-spacing: 0.01em;
	}

	.meter-cell dd {
		font-size: var(--fs-md);
		font-weight: var(--fw-medium);
		line-height: 1.2;
		color: var(--text-primary);
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.meter-cell[data-tone="live"] dd { color: var(--live); }
	.meter-cell[data-tone="queued"] dd { color: var(--queued); }
	.meter-cell[data-tone="danger"] dd { color: var(--danger); }

	/* ================= Footer ================= */
	.sidebar-footer {
		padding: 12px 14px 14px 16px;
		border-top: 1px solid var(--border-subtle);
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
		border-radius: 50%;
		font-family: var(--font-display);
		font-size: 16px;
		color: var(--accent-ink);
		background: radial-gradient(circle at 30% 25%, #ffd99c, var(--accent) 70%);
		box-shadow: 0 0 0 1px rgba(255, 214, 150, 0.4), 0 4px 12px -4px var(--accent-glow);
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
		border: 1px solid transparent;
		color: var(--text-tertiary);
		border-radius: var(--radius);
		transition: color var(--dur-1) ease, background var(--dur-1) ease, border-color var(--dur-1) ease;
	}

	.btn-logout:hover {
		color: var(--danger);
		background: var(--danger-soft);
		border-color: rgba(255, 107, 97, 0.2);
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
		border: 1px solid var(--border);
		color: var(--text-primary);
		padding: 0;
		border-radius: var(--radius);
		width: 40px;
		height: 40px;
		transition: background var(--dur-1) ease, border-color var(--dur-1) ease;
	}

	.mobile-toggle:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
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
		font-size: 23px;
	}

	.mobile-live {
		margin-left: auto;
		display: inline-flex;
		align-items: center;
		padding: 3px 10px;
		border-radius: var(--radius-full);
		background: var(--live-soft);
		border: 1px solid rgba(95, 224, 194, 0.22);
		color: var(--live);
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
			background: rgba(13, 13, 15, 0.78);
			backdrop-filter: blur(16px) saturate(140%);
			-webkit-backdrop-filter: blur(16px) saturate(140%);
			border-bottom: 1px solid var(--border-subtle);
		}

		.sidebar-backdrop {
			display: block;
		}

		.sidebar {
			transform: translateX(-100%);
			transition: transform var(--dur-3) var(--ease-out);
			box-shadow: var(--shadow-3);
			background: rgba(14, 14, 16, 0.96);
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

		.main-content.fill {
			--page-pad-top: 12px;
			--page-pad-bottom: 12px;
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
