<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { accessToken, isAuthenticated, username, systemStatus, projects, sheep } from '$lib/stores.js';
	import { logout, apiGet } from '$lib/api.js';
	import { connectSSE, disconnectSSE, onSSE } from '$lib/sse.js';
	import { goto } from '$app/navigation';
	import Icon from '$lib/components/Icon.svelte';
	import '../app.css';

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

	// Helper: get sheep status for a project
	function getSheepStatus(projectName) {
		const sheepList = $sheep || [];
		const s = sheepList.find(s => s.project === projectName);
		return s?.status || 'idle';
	}

	onMount(async () => {
		if ($accessToken) {
			// Load system status, projects, and sheep in parallel
			const [statusRes, projectsRes, sheepRes] = await Promise.all([
				apiGet('/api/system/status'),
				apiGet('/api/projects'),
				apiGet('/api/sheep')
			]);
			if (statusRes?.data) systemStatus.set(statusRes.data);
			if (projectsRes?.data) projects.set(projectsRes.data);
			if (sheepRes?.data) sheep.set(sheepRes.data);
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

{#if $page.url.pathname === '/login'}
	<slot />
{:else if $accessToken}
	<div class="app-layout">
		<!-- Mobile hamburger -->
		<button class="mobile-toggle"
			aria-label={sidebarOpen ? 'Close navigation' : 'Open navigation'}
			aria-expanded={sidebarOpen}
			onclick={() => sidebarOpen = !sidebarOpen}>
			<Icon name="menu" size={22} />
		</button>

		<!-- Mobile backdrop -->
		{#if sidebarOpen}
			<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
			<div class="sidebar-backdrop" onclick={() => sidebarOpen = false}></div>
		{/if}

		<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
		<aside class="sidebar" class:open={sidebarOpen} onclick={() => sidebarOpen = false}>
			<div class="sidebar-brand">
				<svg class="brand-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="22" height="22">
					<path d="M12 3c2.5 0 4.5 2 4.5 4.5S14.5 12 12 12" />
					<line x1="12" y1="12" x2="12" y2="22" />
				</svg>
				<span class="brand-text">Shepherd</span>
			</div>

			<nav class="sidebar-nav">
				<a href="/" class="nav-item" class:active={$page.url.pathname === '/'}>
					<Icon name="layout-dashboard" size={18} />
					<span>Dashboard</span>
				</a>

				<!-- Projects accordion -->
				<div class="nav-group">
					<button type="button" class="nav-item nav-group-toggle"
						class:active={$page.url.pathname.startsWith('/projects')}
						aria-expanded={projectsExpanded}
						aria-controls="projects-submenu"
						onclick={(e) => { e.stopPropagation(); projectsExpanded = !projectsExpanded; }}>
						<Icon name="folder" size={18} />
						<span class="nav-group-label">Projects</span>
						<span class="nav-chevron" class:expanded={projectsExpanded}>
							<Icon name="chevron-right" size={14} />
						</span>
					</button>

					{#if projectsExpanded}
						<div class="nav-sub-items" id="projects-submenu">
							<a href="/projects" class="nav-sub-item"
								class:active={$page.url.pathname === '/projects'}
								onclick={(e) => e.stopPropagation()}>
								<Icon name="settings" size={14} />
								<span>Manage</span>
							</a>
							{#each $projects as p}
								<a href="/projects/{encodeURIComponent(p.name)}" class="nav-sub-item"
									class:active={decodeURIComponent($page.url.pathname) === `/projects/${p.name}`}
									onclick={(e) => e.stopPropagation()}>
									<span class="status-dot {getSheepStatus(p.name)}"></span>
									<span class="nav-sub-label" title={p.name}>{p.name}</span>
								</a>
							{/each}
							{#if $projects.length === 0}
								<span class="nav-sub-empty">No projects</span>
							{/if}
						</div>
					{/if}
				</div>

				<a href="/tasks" class="nav-item" class:active={$page.url.pathname === '/tasks'}>
					<Icon name="list-checks" size={18} />
					<span>Tasks</span>
				</a>
				<a href="/schedules" class="nav-item" class:active={$page.url.pathname.startsWith('/schedules')}>
					<Icon name="alarm-clock" size={18} />
					<span>Schedules</span>
				</a>
				<a href="/skills" class="nav-item" class:active={$page.url.pathname.startsWith('/skills')}>
					<Icon name="book-open" size={18} />
					<span>Skills</span>
				</a>
				<a href="/settings" class="nav-item" class:active={$page.url.pathname.startsWith('/settings')}>
					<Icon name="settings" size={18} />
					<span>Settings</span>
				</a>
			</nav>

			<!-- Sidebar stats -->
			<div class="sidebar-stats">
				<div class="stats-title">Stats</div>
				<div class="stat-row">
					<span class="stat-label">Working</span>
					<span class="stat-val working">{stats.working}</span>
				</div>
				<div class="stat-row">
					<span class="stat-label">Pending</span>
					<span class="stat-val pending">{stats.pending}</span>
				</div>
				<div class="stat-row">
					<span class="stat-label">Completed</span>
					<span class="stat-val completed">{stats.completed}</span>
				</div>
				<div class="stat-row">
					<span class="stat-label">Failed</span>
					<span class="stat-val failed">{stats.failed}</span>
				</div>
			</div>

			<div class="sidebar-footer">
				<div class="user-info">
					<Icon name="user" size={14} />
					<span>{$username || 'anonymous'}</span>
				</div>
				<button class="btn-logout" aria-label="Sign out" onclick={logout}>
					<Icon name="log-out" size={14} />
					<span>Logout</span>
				</button>
			</div>
		</aside>

		<main class="main-content">
			<slot />
		</main>
	</div>
{:else}
	<div class="loading-screen">Loading...</div>
{/if}

<style>
	.app-layout {
		display: flex;
		min-height: 100vh;
	}

	.sidebar {
		width: var(--sidebar-w);
		background: var(--bg-2);
		border-right: 1px solid var(--border);
		display: flex;
		flex-direction: column;
		position: fixed;
		top: 0;
		left: 0;
		bottom: 0;
		z-index: 50;
		overflow-y: auto;
	}

	.sidebar-backdrop {
		display: none;
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.5);
		z-index: 49;
		backdrop-filter: blur(2px);
		-webkit-backdrop-filter: blur(2px);
	}

	.sidebar-brand {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-4);
		font-size: var(--fs-md);
		font-weight: var(--fw-semibold);
		letter-spacing: -0.01em;
		border-bottom: 1px solid var(--border);
	}

	.brand-icon {
		flex-shrink: 0;
		color: var(--accent);
	}

	.sidebar-nav {
		flex: 1;
		padding: var(--space-2);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.nav-item {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius);
		color: var(--text-secondary);
		text-decoration: none;
		font-size: var(--fs-base);
		transition: background 0.15s, color 0.15s, box-shadow 0.15s;
		/* Reset for <button> variant (nav-group-toggle) */
		background: none;
		border: none;
		width: 100%;
		text-align: left;
		font-family: inherit;
	}

	.nav-item:hover {
		background: var(--bg-3);
		color: var(--text-primary);
		text-decoration: none;
	}

	.nav-item.active {
		background: var(--bg-3);
		color: var(--accent);
		font-weight: var(--fw-medium);
		box-shadow: inset 2px 0 0 var(--accent);
	}

	.nav-item :global(.icon) {
		flex-shrink: 0;
		color: currentColor;
		opacity: 0.85;
	}

	.nav-item.active :global(.icon) {
		opacity: 1;
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

	.nav-chevron {
		display: inline-flex;
		transition: transform 0.2s ease;
		color: var(--text-tertiary);
	}

	.nav-chevron.expanded {
		transform: rotate(90deg);
	}

	.nav-sub-items {
		display: flex;
		flex-direction: column;
		gap: 1px;
		padding-left: 8px;
	}

	.nav-sub-item {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: 5px var(--space-3);
		border-radius: var(--radius);
		color: var(--text-secondary);
		text-decoration: none;
		font-size: var(--fs-sm);
		transition: background 0.15s, color 0.15s, box-shadow 0.15s;
	}

	.nav-sub-item:hover {
		background: var(--bg-3);
		color: var(--text-primary);
		text-decoration: none;
	}

	.nav-sub-item.active {
		background: var(--bg-3);
		color: var(--accent);
		font-weight: var(--fw-medium);
	}

	.nav-sub-item :global(.icon) {
		opacity: 0.75;
	}

	.nav-sub-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 140px;
	}

	.nav-sub-empty {
		font-size: 12px;
		color: var(--text-secondary);
		padding: 4px 12px;
		font-style: italic;
	}

	/* Status dot */
	.status-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.status-dot.idle {
		background: var(--text-secondary);
	}

	.status-dot.working {
		background: var(--live);
		box-shadow: 0 0 0 3px var(--live-soft);
		animation: pulse 1.5s ease-in-out infinite;
	}

	.status-dot.error {
		background: var(--danger);
	}

	@keyframes pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	/* Sidebar stats */
	.sidebar-stats {
		padding: 12px 16px;
		border-top: 1px solid var(--border);
	}

	.stats-title {
		font-size: 11px;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
		margin-bottom: 8px;
	}

	.stat-row {
		display: flex;
		justify-content: space-between;
		padding: 2px 0;
		font-size: 13px;
	}

	.stat-label {
		color: var(--text-secondary);
	}

	.stat-val {
		font-weight: 600;
		font-family: var(--font-mono);
		font-size: 13px;
	}

	.stat-val.working { color: var(--live); }
	.stat-val.pending { color: var(--warning); }
	.stat-val.completed { color: var(--success); }
	.stat-val.failed { color: var(--danger); }

	/* Sidebar footer */
	.sidebar-footer {
		padding: 12px;
		border-top: 1px solid var(--border);
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.user-info {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.btn-logout {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: var(--fs-xs);
		padding: 4px 8px;
		border-radius: var(--radius);
		transition: color 0.15s, background 0.15s;
	}

	.btn-logout:hover {
		color: var(--danger);
		background: var(--danger-soft);
	}

	.main-content {
		flex: 1;
		min-width: 0;
		margin-left: var(--sidebar-w);
		padding: var(--space-6);
		overflow-x: hidden;
	}

	.loading-screen {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 100vh;
		color: var(--text-secondary);
	}

	/* Mobile toggle */
	.mobile-toggle {
		display: none;
		align-items: center;
		justify-content: center;
		position: fixed;
		top: var(--space-2);
		left: var(--space-2);
		z-index: 60;
		background: var(--bg-2);
		border: 1px solid var(--border);
		color: var(--text-primary);
		padding: 0;
		border-radius: var(--radius);
		min-width: 44px;
		min-height: 44px;
		box-shadow: var(--shadow-1);
		transition: background 0.15s, border-color 0.15s;
	}

	.mobile-toggle:hover {
		background: var(--bg-3);
		border-color: var(--border-strong);
	}

	/* === Responsive ===
	   Breakpoints:
	   - ≤480px : Titan 2 (1440x1440 @ ~3-4 DPR ≈ 360-480 CSS px) and small phones
	   - ≤768px : tablet portrait / standard mobile
	*/
	@media (max-width: 768px) {
		.mobile-toggle {
			display: inline-flex;
		}

		.sidebar-backdrop {
			display: block;
		}

		.sidebar {
			transform: translateX(-100%);
			transition: transform 0.2s ease;
			box-shadow: var(--shadow-3);
		}

		.sidebar.open {
			transform: translateX(0);
		}

		.main-content {
			margin-left: 0;
			padding: var(--space-3);
			padding-top: calc(var(--mobile-topbar-h) + var(--space-2));
		}

		.nav-item {
			padding: 10px var(--space-3);
			min-height: 40px;
		}

		.nav-sub-item {
			padding: var(--space-2) var(--space-3);
			min-height: 36px;
		}

		.nav-sub-label {
			max-width: none;
		}

		.btn-logout {
			padding: var(--space-2) var(--space-3);
			min-height: 36px;
		}
	}

	/* Titan 2 (1440x1440 square @ ~3 DPR ≈ 480 CSS px) and very small screens */
	@media (max-width: 480px) {
		.sidebar {
			/* Full-width drawer on tiny screens (less swipeable target on a square viewport) */
			width: min(280px, 88vw);
		}

		.main-content {
			padding: var(--space-2);
			padding-top: calc(var(--mobile-topbar-h) + var(--space-1));
		}

		.sidebar-brand {
			padding: var(--space-3);
			font-size: var(--fs-md);
		}

		.sidebar-nav {
			padding: var(--space-2) 6px;
		}

		.sidebar-stats {
			padding: var(--space-2) var(--space-3);
		}

		.sidebar-footer {
			padding: var(--space-2) var(--space-3);
		}

		.mobile-toggle {
			top: 6px;
			left: 6px;
			padding: 8px 12px;
			min-width: 40px;
			min-height: 40px;
		}
	}
</style>
