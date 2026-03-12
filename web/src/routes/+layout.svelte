<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { accessToken, isAuthenticated, username, systemStatus, projects, sheep } from '$lib/stores.js';
	import { logout, apiGet } from '$lib/api.js';
	import { connectSSE, disconnectSSE, onSSE } from '$lib/sse.js';
	import { goto } from '$app/navigation';
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
		<button class="mobile-toggle" onclick={() => sidebarOpen = !sidebarOpen}>
			&#x2630;
		</button>

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
					<span class="nav-icon">&#x1F4CB;</span>
					Dashboard
				</a>

				<!-- Projects accordion -->
				<div class="nav-group">
					<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
					<div class="nav-item nav-group-toggle"
						class:active={$page.url.pathname.startsWith('/projects')}
						onclick={(e) => { e.stopPropagation(); projectsExpanded = !projectsExpanded; }}>
						<span class="nav-icon">&#x1F4C1;</span>
						<span class="nav-group-label">Projects</span>
						<span class="nav-chevron" class:expanded={projectsExpanded}>&#x25B6;</span>
					</div>

					{#if projectsExpanded}
						<div class="nav-sub-items">
							<a href="/projects" class="nav-sub-item"
								class:active={$page.url.pathname === '/projects'}
								onclick={(e) => e.stopPropagation()}>
								<span class="nav-sub-icon">&#x2699;</span>
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
					<span class="nav-icon">&#x1F4DD;</span>
					Tasks
				</a>
				<a href="/schedules" class="nav-item" class:active={$page.url.pathname.startsWith('/schedules')}>
					<span class="nav-icon">&#x23F0;</span>
					Schedules
				</a>
				<a href="/skills" class="nav-item" class:active={$page.url.pathname.startsWith('/skills')}>
					<span class="nav-icon">&#x1F4DA;</span>
					Skills
				</a>
				<a href="/settings" class="nav-item" class:active={$page.url.pathname.startsWith('/settings')}>
					<span class="nav-icon">&#x2699;</span>
					Settings
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
					<span class="user-icon">&#x1F464;</span>
					<span>{$username || 'anonymous'}</span>
				</div>
				<button class="btn-logout" onclick={logout}>Logout</button>
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
		width: 220px;
		background: var(--bg-secondary);
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

	.sidebar-brand {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 16px;
		font-size: 18px;
		font-weight: 600;
		border-bottom: 1px solid var(--border);
	}

	.brand-icon {
		flex-shrink: 0;
		color: var(--accent);
	}

	.sidebar-nav {
		flex: 1;
		padding: 8px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.nav-item {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 8px 12px;
		border-radius: var(--radius);
		color: var(--text-secondary);
		text-decoration: none;
		font-size: 14px;
		transition: background 0.15s, color 0.15s;
	}

	.nav-item:hover {
		background: var(--bg-tertiary);
		color: var(--text-primary);
		text-decoration: none;
	}

	.nav-item.active {
		background: var(--bg-tertiary);
		color: var(--accent);
	}

	.nav-icon {
		font-size: 16px;
		width: 20px;
		text-align: center;
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
		font-size: 10px;
		transition: transform 0.2s ease;
		color: var(--text-secondary);
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
		gap: 8px;
		padding: 5px 12px;
		border-radius: var(--radius);
		color: var(--text-secondary);
		text-decoration: none;
		font-size: 13px;
		transition: background 0.15s, color 0.15s;
	}

	.nav-sub-item:hover {
		background: var(--bg-tertiary);
		color: var(--text-primary);
		text-decoration: none;
	}

	.nav-sub-item.active {
		background: var(--bg-tertiary);
		color: var(--accent);
	}

	.nav-sub-icon {
		font-size: 12px;
		width: 14px;
		text-align: center;
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
		background: var(--accent);
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

	.stat-val.working { color: var(--accent); }
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
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 13px;
		color: var(--text-secondary);
	}

	.btn-logout {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 12px;
		padding: 4px 8px;
		border-radius: var(--radius);
	}

	.btn-logout:hover {
		color: var(--danger);
		background: rgba(248, 81, 73, 0.1);
	}

	.main-content {
		flex: 1;
		min-width: 0;
		margin-left: 220px;
		padding: 24px;
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
		position: fixed;
		top: 8px;
		left: 8px;
		z-index: 60;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		color: var(--text-primary);
		font-size: 20px;
		padding: 10px 14px;
		border-radius: var(--radius);
		min-width: 44px;
		min-height: 44px;
	}

	/* Responsive */
	@media (max-width: 768px) {
		.mobile-toggle {
			display: flex;
			align-items: center;
			justify-content: center;
		}

		.sidebar {
			transform: translateX(-100%);
			transition: transform 0.2s ease;
		}

		.sidebar.open {
			transform: translateX(0);
		}

		.main-content {
			margin-left: 0;
			padding: 12px;
			padding-top: 56px;
		}

		.nav-item {
			padding: 10px 12px;
		}

		.nav-sub-item {
			padding: 8px 12px;
		}

		.nav-sub-label {
			max-width: none;
		}

		.btn-logout {
			padding: 8px 12px;
		}
	}
</style>
