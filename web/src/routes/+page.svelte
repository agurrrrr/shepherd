<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet } from '$lib/api.js';
	import { sheep, systemStatus, projects, username } from '$lib/stores.js';
	import { onSSE } from '$lib/sse.js';
	import SheepCard from '$lib/components/SheepCard.svelte';
	import SectionHeader from '$lib/components/SectionHeader.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Tabs from '$lib/components/Tabs.svelte';
	import MonitorGrid from '$lib/components/MonitorGrid.svelte';
	import FlockField from '$lib/components/FlockField.svelte';
	import UsageBars from '$lib/components/UsageBars.svelte';
	import CountUp from '$lib/components/CountUp.svelte';

	let loaded = $state(false);
	let dashboard = $state(null);
	let unsubscribers = [];
	let activityWindow = $state('1d');
	let activityProject = $state('');
	let refreshSeq = 0;
	let monitoring = $state(false);
	let now = $state(new Date());
	let clock;

	const activityTabs = [
		{ value: '5h', label: '5h' },
		{ value: '1d', label: '1d' },
		{ value: '7d', label: '7d' },
		{ value: '1m', label: '1M' },
		{ value: '3m', label: '3M' },
		{ value: '6m', label: '6M' },
		{ value: '1y', label: '1Y' }
	];

	let projectOptions = $derived([...($projects || [])].sort((a, b) => a.name.localeCompare(b.name)));

	// 1,284 → 1.3K, 14,321,505 → 14.3M
	function fmtCompact(n) {
		const v = Number(n) || 0;
		if (v >= 1e9) return (v / 1e9).toFixed(1) + 'B';
		if (v >= 1e6) return (v / 1e6).toFixed(1) + 'M';
		if (v >= 1e3) return (v / 1e3).toFixed(1) + 'K';
		return String(v);
	}

	// Duration in HH:MM:SS format.
	function fmtDuration(sec) {
		const s = Math.max(0, Math.floor(Number(sec) || 0));
		const hh = String(Math.floor(s / 3600)).padStart(2, '0');
		const mm = String(Math.floor((s % 3600) / 60)).padStart(2, '0');
		const ss = String(s % 60).padStart(2, '0');
		return `${hh}:${mm}:${ss}`;
	}

	// Long spans read better as "36h 48m" than a clock.
	function fmtSpan(sec) {
		const s = Math.max(0, Math.floor(Number(sec) || 0));
		const h = Math.floor(s / 3600);
		const m = Math.floor((s % 3600) / 60);
		if (h >= 24) return `${Math.floor(h / 24)}d ${h % 24}h`;
		if (h > 0) return `${h}h ${String(m).padStart(2, '0')}m`;
		return `${m}m ${String(s % 60).padStart(2, '0')}s`;
	}

	function fmtCost(n) {
		const v = Number(n) || 0;
		return v >= 100 ? `$${Math.round(v).toLocaleString()}` : `$${v.toFixed(2)}`;
	}

	// Split "provider/model..." so the provider prefix can be muted in the UI.
	function splitModelKey(key) {
		const k = String(key || '');
		const i = k.indexOf('/');
		if (i <= 0) return { prefix: '', rest: k };
		return { prefix: k.slice(0, i), rest: k.slice(i + 1) };
	}

	onMount(async () => {
		clock = setInterval(() => (now = new Date()), 30_000);

		await refreshData();
		loaded = true;

		unsubscribers.push(
			onSSE('status_change', () => refreshData()),
			onSSE('task_complete', () => refreshData()),
			onSSE('task_start', () => refreshData()),
			onSSE('task_fail', () => refreshData())
		);
	});

	onDestroy(() => {
		unsubscribers.forEach(fn => fn?.());
		clearInterval(clock);
	});

	async function refreshData() {
		// SSE refreshes and project-filter changes can overlap; only the latest
		// response may land, or a stale project's activity could win the race.
		const seq = ++refreshSeq;
		const dashUrl = activityProject
			? `/api/dashboard?project=${encodeURIComponent(activityProject)}`
			: '/api/dashboard';
		const [dashRes, sheepRes] = await Promise.all([
			apiGet(dashUrl),
			apiGet('/api/sheep')
		]);
		if (seq !== refreshSeq) return;
		if (dashRes?.data) {
			dashboard = dashRes.data;
			// Keep systemStatus in sync for sidebar
			systemStatus.set({
				sheep: { total: dashRes.data.sheep.total, working: dashRes.data.sheep.working, idle: dashRes.data.sheep.idle, error: dashRes.data.sheep.error },
				projects: dashRes.data.projects,
				tasks: dashRes.data.tasks
			});
		}
		if (sheepRes?.data) sheep.set(sheepRes.data);
	}

	function timeAgo(dateStr) {
		const date = new Date(dateStr.replace(' ', 'T'));
		const diff = Math.floor((now - date) / 1000);
		if (diff < 60) return 'just now';
		if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
		if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
		return Math.floor(diff / 86400) + 'd ago';
	}

	// Hidden projects leave the sidebar (+layout.svelte) and they leave the flock
	// pasture too — a sheep assigned to one is still reachable via /projects and
	// /sheep, it just does not graze on the dashboard. Sheep with no project stay.
	// Only the pasture and its legend follow this filter: the Awake tile, the hero
	// line, the Live section and the monitor keep counting every sheep, so work
	// running in a hidden project is never silently dropped from view.
	let hiddenProjects = $derived(
		new Set(($projects || []).filter((p) => p.hidden).map((p) => p.name))
	);
	let flockSheep = $derived(
		($sheep || []).filter((s) => !s.project || !hiddenProjects.has(s.project))
	);
	let hiddenSheep = $derived(
		($sheep || []).filter((s) => s.project && hiddenProjects.has(s.project))
	);
	let workingSheep = $derived($sheep.filter(s => s.status === 'working' && s.project));

	function setActivityProject(name) {
		activityProject = name;
		refreshData();
	}

	function statusIcon(status) {
		switch (status) {
			case 'completed': return { name: 'check', tone: 'success' };
			case 'failed': return { name: 'x', tone: 'danger' };
			case 'running': return { name: 'zap', tone: 'live' };
			case 'pending': return { name: 'hourglass', tone: 'queued' };
			case 'stopped': return { name: 'square', tone: 'warning' };
			default: return { name: 'circle', tone: 'idle' };
		}
	}

	// --- Hero copy -------------------------------------------------------
	let greeting = $derived.by(() => {
		const h = now.getHours();
		if (h >= 5 && h < 12) return 'Good morning';
		if (h >= 12 && h < 18) return 'Good afternoon';
		if (h >= 18 && h < 23) return 'Good evening';
		return 'Keeping watch';
	});

	let dateLine = $derived(
		now.toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short' }) +
		' · ' +
		now.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
	);

	let totals = $derived({
		sheep: dashboard?.sheep?.total ?? $sheep.length,
		working: dashboard?.sheep?.working ?? workingSheep.length,
		error: dashboard?.sheep?.error ?? 0,
		pending: dashboard?.tasks?.pending ?? 0,
		completed: dashboard?.tasks?.completed ?? 0,
		failed: dashboard?.tasks?.failed ?? 0
	});

	// The pasture legend counts only what the pasture draws, so the numbers add up
	// to the sheep on screen. Fall back to the backend counts while the sheep
	// store is still empty.
	let flockTotals = $derived.by(() => {
		if (($sheep || []).length === 0) {
			return {
				total: dashboard?.sheep?.total ?? 0,
				working: dashboard?.sheep?.working ?? 0,
				error: dashboard?.sheep?.error ?? 0
			};
		}
		return {
			total: flockSheep.length,
			working: flockSheep.filter((s) => s.status === 'working').length,
			error: flockSheep.filter((s) => s.status === 'error').length
		};
	});

	let finished = $derived(totals.completed + totals.failed);
	let successRate = $derived(finished > 0 ? totals.completed / finished : 0);

	let heroLine = $derived.by(() => {
		const w = totals.working;
		const p = totals.pending;
		const awake = w === 0
			? 'The whole flock is resting.'
			: `${w} of ${totals.sheep} sheep ${w === 1 ? 'is' : 'are'} awake${workingSheep.length === 1 ? ` on ${workingSheep[0].project}` : ''}.`;
		const queue = p === 0 ? 'Nothing is waiting in the fold.' : `${p} ${p === 1 ? 'task waits' : 'tasks wait'} in the fold.`;
		return `${awake} ${queue}`;
	});

	// Section ordinals shift when the live section is absent.
	let ord = $derived.by(() => {
		const live = workingSheep.length > 0;
		return { flock: '01', live: '02', usage: live ? '03' : '02', recent: live ? '04' : '03' };
	});

	// --- Usage data ------------------------------------------------------
	let activity = $derived(dashboard?.activity?.[activityWindow] || { items: [], total: 0, total_cost: 0 });
	let activityTokens = $derived(activity.items.reduce((s, it) => s + (it.total_tokens || 0), 0));
	let activityItems = $derived([...activity.items].sort((a, b) => (b.total_tokens || 0) - (a.total_tokens || 0)).map((it) => ({
		key: it.project,
		label: it.project,
		muted: it.project === '(unassigned)',
		value: it.total_tokens || 0,
		display: fmtCompact(it.total_tokens),
		sub: `${it.tasks} ${it.tasks === 1 ? 'task' : 'tasks'}`,
		details: [
			['Tasks', it.tasks.toLocaleString()],
			['Tokens', (it.total_tokens || 0).toLocaleString()],
			['Cost', fmtCost(it.cost_usd)]
		]
	})));

	let byModel = $derived(dashboard?.by_model?.[activityWindow] || { items: [], total_completed: 0, total_tokens: 0, total_duration_sec: 0 });
	let modelItems = $derived([...byModel.items].sort((a, b) => (b.total_tokens || 0) - (a.total_tokens || 0)).map((it) => {
		const parts = splitModelKey(it.key);
		return {
			key: it.key,
			label: parts.rest,
			prefix: parts.prefix,
			muted: it.key === '(unknown)',
			value: it.total_tokens || 0,
			display: fmtCompact(it.total_tokens),
			sub: `${it.completed} done`,
			details: [
				['Completed', it.completed.toLocaleString()],
				['Prompt', (it.prompt_tokens || 0).toLocaleString()],
				['Completion', (it.completion_tokens || 0).toLocaleString()],
				['Run time', fmtSpan(it.duration_sec)]
			]
		};
	}));
</script>

{#if monitoring}
	<MonitorGrid working={workingSheep} onClose={() => (monitoring = false)} />
{/if}

<div class="dashboard">
	<!-- ============ Hero ============ -->
	<header class="hero">
		<div class="hero-text">
			<span class="eyebrow hero-eyebrow">
				<span class="moon" aria-hidden="true"><Icon name="moon" size={12} stroke={2} /></span>
				{dateLine}
			</span>
			<h1 class="hero-title">
				{greeting},<br class="hero-break" />
				<em>{$username || 'shepherd'}</em><span class="hero-dot">.</span>
			</h1>
			<p class="hero-line">{heroLine}</p>
		</div>
		<div class="hero-actions">
			<button class="btn btn-live monitor-btn" onclick={() => (monitoring = true)} title="실행 중인 양 모니터링">
				<Icon name="monitor" size={15} />
				모니터링
				{#if workingSheep.length > 0}
					<span class="monitor-count num">{workingSheep.length}</span>
				{/if}
			</button>
		</div>
	</header>

	{#if !loaded}
		<div class="overview" aria-busy="true">
			<div class="card flock-card"><div class="skeleton" style="height: 190px"></div></div>
			<div class="tiles">
				{#each Array(4) as _}
					<div class="card tile"><div class="skeleton" style="height: 14px; width: 40%"></div><div class="skeleton" style="height: 36px; width: 55%; margin-top: 14px"></div></div>
				{/each}
			</div>
		</div>
	{:else if dashboard}
		<!-- ============ 01 Flock + tiles ============ -->
		<section class="overview">
			<div class="card flock-card">
				<div class="flock-head">
					<SectionHeader index={ord.flock} eyebrow="Pasture" title="The flock" />
					<ul class="legend" aria-label="Legend">
						<li><span class="key live"></span>Working <b class="num">{flockTotals.working}</b></li>
						<li><span class="key rest"></span>Resting <b class="num">{Math.max(0, flockTotals.total - flockTotals.working - flockTotals.error)}</b></li>
						{#if flockTotals.error > 0}
							<li><span class="key danger"></span>Error <b class="num">{flockTotals.error}</b></li>
						{/if}
						{#if hiddenSheep.length > 0}
							<li><span class="key hidden"></span>Hidden <b class="num">{hiddenSheep.length}</b></li>
						{/if}
					</ul>
				</div>
				<FlockField sheep={flockSheep} />
			</div>

			<div class="tiles">
				<div class="card tile tile-live" class:on={totals.working > 0}>
					<span class="tile-label">
						{#if totals.working > 0}<span class="live-dot"></span>{/if}
						Awake
					</span>
					<span class="tile-value"><CountUp value={totals.working} /></span>
					<span class="tile-sub">of {totals.sheep} sheep</span>
				</div>
				<div class="card tile">
					<span class="tile-label">Queued</span>
					<span class="tile-value"><CountUp value={totals.pending} /></span>
					<span class="tile-sub">{totals.pending === 1 ? 'task' : 'tasks'} in the fold</span>
				</div>
				<div class="card tile">
					<span class="tile-label">Today</span>
					<span class="tile-value"><CountUp value={dashboard.today?.total ?? 0} /></span>
					<span class="tile-sub">
						<span class="ok">{dashboard.today?.completed ?? 0} done</span>
						{#if (dashboard.today?.failed ?? 0) > 0}
							· <span class="bad">{dashboard.today.failed} failed</span>
						{/if}
					</span>
				</div>
				<div class="card tile">
					<span class="tile-label">Success rate</span>
					<span class="tile-value"><CountUp value={successRate * 100} format={(n) => Math.round(n)} /><small>%</small></span>
					<span class="meter" aria-hidden="true"><span style="--r: {successRate}"></span></span>
					<span class="tile-sub num">{totals.completed.toLocaleString()} of {finished.toLocaleString()} finished</span>
				</div>
			</div>
		</section>

		<!-- ============ 02 Live ============ -->
		{#if workingSheep.length > 0}
			<section class="block live-block">
				<SectionHeader
					index={ord.live}
					eyebrow="Live"
					tone="live"
					title={workingSheep.length === 1 ? 'One sheep on watch' : `${workingSheep.length} sheep on watch`}
				/>
				<div class="sheep-grid">
					{#each workingSheep as s, i (s.name)}
						<div class="reveal" style="--i: {i}">
							<SheepCard
								name={s.name}
								project={s.project}
								status={s.status}
								provider={s.provider}
							/>
						</div>
					{/each}
				</div>
			</section>
		{/if}

		<!-- ============ 03 Usage ============ -->
		<section class="block">
			<SectionHeader index={ord.usage} eyebrow="Usage" title="Where the tokens went">
				<select
					class="project-filter"
					value={activityProject}
					onchange={(e) => setActivityProject(e.currentTarget.value)}
					aria-label="Usage project filter"
				>
					<option value="">All projects</option>
					{#each projectOptions as p (p.name)}
						<option value={p.name}>{p.name}</option>
					{/each}
				</select>
				<Tabs
					tabs={activityTabs}
					value={activityWindow}
					onChange={(v) => (activityWindow = v)}
					ariaLabel="Usage window"
					size="sm"
				/>
			</SectionHeader>

			<div class="usage-grid">
				<div class="card usage-card">
					<div class="usage-head">
						<h3 class="panel-title">By project</h3>
						<span class="usage-total mono num">{activity.total.toLocaleString()} tasks · {fmtCompact(activityTokens)} tokens · {fmtCost(activity.total_cost)}</span>
					</div>
					{#if activityItems.length > 0}
						{#key activityWindow + activityProject}
							<UsageBars items={activityItems} limit={9} label="Tokens by project" />
						{/key}
					{:else}
						<EmptyState icon="sheep" title="A quiet stretch" description="No activity in this window. Try a wider range or start a task above." />
					{/if}
				</div>

				<div class="card usage-card">
					<div class="usage-head">
						<h3 class="panel-title">By model</h3>
						<span class="usage-total mono num">{(byModel.total_completed || 0).toLocaleString()} done · {fmtCompact(byModel.total_tokens)} tokens · {fmtSpan(byModel.total_duration_sec)}</span>
					</div>
					{#if modelItems.length > 0}
						{#key activityWindow + activityProject}
							<UsageBars items={modelItems} limit={9} label="Tokens by model" />
						{/key}
					{:else}
						<EmptyState icon="sheep" title="Nothing finished yet" description="Widen the window or wait for a task to complete." />
					{/if}
				</div>
			</div>
		</section>

		<!-- ============ 04 Recent ============ -->
		{@const recentTasks = dashboard.recent_tasks || []}
		<section class="block">
			<SectionHeader index={ord.recent} eyebrow="Log" title="Recent tasks">
				<a href="/tasks" class="btn btn-ghost btn-sm">
					All tasks <Icon name="arrow-right" size={13} />
				</a>
			</SectionHeader>
			{#if recentTasks.length > 0}
				<ol class="recent-list card">
					{#each recentTasks as task, i (task.id)}
						{@const icn = statusIcon(task.status)}
						<li class="reveal" style="--i: {i}">
							<a href="/tasks/{task.id}" class="recent-item" class:failed={task.status === 'failed'}>
								<span class="recent-icon" data-tone={icn.tone} title={task.status}>
									<Icon name={icn.name} size={13} stroke={2.4} label={task.status} />
								</span>
								<div class="recent-body">
									<span class="recent-prompt">{task.summary || task.prompt}</span>
									<span class="recent-meta">
										{#if task.project}<span class="chip">{task.project}</span>{/if}
										{#if task.sheep}<span class="recent-sheep">{task.sheep}</span>{/if}
										{#if task.status === 'running'}<span class="badge badge-running">running</span>{/if}
										{#if task.status === 'failed' && task.error}
											<span class="recent-error">{task.error}</span>
										{/if}
									</span>
								</div>
								<div class="recent-side">
									<span class="recent-time">{timeAgo(task.created_at)}</span>
									<span class="recent-metrics mono num">
										{#if task.duration_sec > 0}<span title="실행 시간">{fmtDuration(task.duration_sec)}</span>{/if}
										{#if task.total_tokens > 0}<span title="토큰 사용량">{fmtCompact(task.total_tokens)}</span>{/if}
									</span>
								</div>
							</a>
						</li>
					{/each}
				</ol>
			{:else}
				<div class="card">
					<EmptyState
						icon="sheep"
						title="The flock is resting"
						description="No tasks yet. Use the command bar above to start your first one."
					/>
				</div>
			{/if}
		</section>
	{/if}
</div>

<style>
	.dashboard {
		max-width: var(--content-max);
	}

	/* ================= Hero ================= */
	.hero {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-5);
		margin-bottom: var(--space-7);
	}

	.hero-text {
		display: flex;
		flex-direction: column;
		gap: 14px;
		min-width: 0;
	}

	.hero-eyebrow {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		animation: rise-in 600ms var(--ease-out) backwards;
	}

	.moon {
		display: inline-flex;
		color: var(--highlight);
	}

	/* 48px (24px under 480px): crisp for Press Start 2P and the Galmuri11
	   fallback that draws a Hangul user name. */
	.hero-title {
		font-family: var(--font-display);
		font-size: var(--fs-4xl);
		font-weight: 400;
		line-height: 1.25;
		letter-spacing: 0;
		color: var(--text-primary);
		text-shadow: 4px 4px 0 #000;
		overflow-wrap: anywhere;
		animation: rise-in 700ms var(--ease-out) 60ms backwards;
	}

	.hero-title em {
		font-style: normal;
		color: var(--highlight);
		text-shadow: 4px 4px 0 var(--p-plum);
	}

	.hero-dot {
		color: var(--accent);
	}

	.hero-break {
		display: none;
	}

	.hero-line {
		max-width: 60ch;
		font-size: var(--fs-md);
		line-height: var(--lh-snug);
		color: var(--text-secondary);
		animation: rise-in 700ms var(--ease-out) 140ms backwards;
	}

	.hero-actions {
		flex-shrink: 0;
		animation: fade-in 700ms ease 240ms backwards;
	}

	.monitor-count {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 18px;
		height: 18px;
		padding: 0 5px;
		font-size: var(--fs-2xs);
		font-family: var(--font-mono);
		color: var(--live);
		background: #000;
		border-radius: 0;
	}

	/* ================= Overview ================= */
	.overview {
		display: grid;
		grid-template-columns: minmax(0, 1.65fr) minmax(0, 1fr);
		gap: var(--space-4);
		margin-bottom: var(--space-9);
	}

	.flock-card {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		padding: 22px 22px 18px;
		animation: rise-in 700ms var(--ease-out) 240ms backwards;
	}

	.flock-head {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-4);
		flex-wrap: wrap;
	}

	.flock-head :global(.section-header) {
		margin: 0;
	}

	.legend {
		display: flex;
		gap: 16px;
		list-style: none;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		padding-bottom: 2px;
	}

	.legend li {
		display: inline-flex;
		align-items: center;
		gap: 7px;
	}

	.legend b {
		font-weight: var(--fw-semibold);
		color: var(--text-primary);
	}

	.key {
		width: 8px;
		height: 8px;
		border-radius: 0;
		box-shadow: var(--px-ring-2);
	}

	.key.live { background: var(--live); }
	.key.rest { background: var(--text-primary); }
	.key.danger { background: var(--danger); }
	/* Hidden-project sheep are counted in the legend but never drawn — a dim key
	   says "these exist, they are just not grazing here". */
	.key.hidden { background: var(--text-tertiary); opacity: 0.45; }

	.legend li .key.hidden + b {
		color: var(--text-secondary);
	}

	.tiles {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: var(--space-4);
	}

	.tile {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 18px 20px;
		overflow: hidden;
		animation: rise-in 700ms var(--ease-out) backwards;
	}

	.tile:nth-child(1) { animation-delay: 280ms; }
	.tile:nth-child(2) { animation-delay: 320ms; }
	.tile:nth-child(3) { animation-delay: 360ms; }
	.tile:nth-child(4) { animation-delay: 400ms; }

	.tile-label {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		font-family: var(--font-label);
		font-size: var(--fs-label);
		line-height: 2;
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.tile-value {
		margin-top: auto;
		padding-top: 10px;
		font-family: var(--font-num);
		font-size: var(--fs-2xl);
		font-weight: 400;
		line-height: 1.25;
		letter-spacing: 0;
		color: var(--text-primary);
		text-shadow: 3px 3px 0 #000;
	}

	.tile-value small {
		margin-left: 2px;
		font-size: var(--fs-xs);
		font-weight: 400;
		letter-spacing: 0;
		color: var(--text-tertiary);
		text-shadow: none;
	}

	.tile-sub {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
	}

	.tile-sub .ok { color: var(--success); }
	.tile-sub .bad { color: var(--danger-text); }

	.tile-live.on .tile-label,
	.tile-live.on .tile-value {
		color: var(--live);
	}

	/* Black track, chunky green cells inset by 2px */
	.meter {
		position: relative;
		display: block;
		height: 12px;
		margin: 8px 0 4px;
		border-radius: 0;
		background: #000;
		overflow: hidden;
	}

	.meter span {
		position: absolute;
		top: 2px;
		bottom: 2px;
		left: 2px;
		width: calc((100% - 4px) * var(--r));
		border-radius: 0;
		background: repeating-linear-gradient(90deg, var(--success) 0 6px, transparent 6px 8px);
		transition: width 900ms steps(8);
		animation: meter-grow 1200ms steps(8) 400ms backwards;
	}

	@keyframes meter-grow {
		from { width: 0; }
	}

	/* ================= Sections ================= */
	.block {
		margin-bottom: var(--space-9);
	}

	.sheep-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
		gap: var(--space-4);
	}

	.sheep-grid > div {
		display: flex;
		min-width: 0;
	}

	.sheep-grid > div > :global(.sheep-card) {
		flex: 1;
		min-width: 0;
	}

	.project-filter {
		max-width: 200px;
		padding: 5px 30px 5px 10px;
		font-size: var(--fs-sm);
		border-radius: 0;
	}

	.usage-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
		gap: var(--space-4);
		align-items: start;
	}

	.usage-card {
		padding: 18px 22px 16px;
	}

	.usage-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-3);
		flex-wrap: wrap;
		margin-bottom: 12px;
		padding-bottom: 12px;
		box-shadow: 0 3px 0 0 var(--lo);
	}

	.usage-total {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.usage-card .panel-title {
		font-family: var(--font-label);
		font-size: var(--fs-label);
		font-weight: var(--fw-regular);
		line-height: 2;
		text-transform: uppercase;
		color: var(--highlight);
	}

	/* ================= Recent ================= */
	.recent-list {
		list-style: none;
		padding: 6px;
	}

	.recent-list li + li {
		box-shadow: 0 -2px 0 0 var(--lo);
	}

	.recent-item {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		gap: 14px;
		align-items: start;
		padding: 12px 14px;
		border-radius: 0;
		color: inherit;
		text-decoration: none;
	}

	.recent-item:hover {
		background: var(--bg-3);
		color: inherit;
		text-decoration: none;
	}

	.recent-item:hover .recent-prompt {
		color: var(--text-primary);
	}

	.recent-icon {
		display: grid;
		place-items: center;
		width: 22px;
		height: 22px;
		margin: 3px 2px 2px;
		border-radius: 0;
		color: #000;
		background: var(--p-dgray);
		box-shadow: var(--px-ring-2);
	}

	/* Status fill with a black glyph (also on red: white would be 3.55:1) */
	.recent-icon[data-tone="success"] { background: var(--success); }
	.recent-icon[data-tone="live"] { background: var(--live); }
	.recent-icon[data-tone="danger"] { background: var(--danger); }
	.recent-icon[data-tone="queued"] { background: var(--queued); }
	.recent-icon[data-tone="warning"] { background: var(--warning); }

	.recent-body {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.recent-prompt {
		font-size: var(--fs-sm);
		line-height: var(--lh-snug);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.recent-meta {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}

	.recent-sheep {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.recent-error {
		min-width: 0;
		font-size: var(--fs-2xs);
		color: var(--danger-text);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.recent-side {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 6px;
		white-space: nowrap;
	}

	.recent-time {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.recent-metrics {
		display: inline-flex;
		gap: 10px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	/* ================= Responsive ================= */
	@media (max-width: 1180px) {
		.overview {
			grid-template-columns: 1fr;
		}

		.tiles {
			grid-template-columns: repeat(4, minmax(0, 1fr));
		}

		.usage-grid {
			grid-template-columns: 1fr;
		}
	}

	@media (max-width: 768px) {
		.hero {
			flex-direction: column;
			align-items: flex-start;
			gap: var(--space-4);
			margin-bottom: var(--space-5);
		}

		.hero-title {
			font-size: var(--fs-4xl);
		}

		.hero-break {
			display: inline;
		}

		.hero-line {
			font-size: var(--fs-sm);
		}

		.overview,
		.block {
			margin-bottom: var(--space-7);
		}

		.tiles {
			grid-template-columns: 1fr 1fr;
			gap: var(--space-3);
		}

		.tile {
			padding: 14px 16px;
		}

		.tile-value {
			font-size: var(--fs-lg);
		}

		/* Press Start 2P draws 24px per glyph; four-digit counts in a
		   half-width phone tile need the 16px step. */
		.tile-value {
			font-size: 16px;
		}

		.flock-card {
			padding: 16px 14px 8px;
		}

		.legend {
			gap: 12px;
		}

		.sheep-grid {
			grid-template-columns: 1fr;
		}

		.usage-card {
			padding: 14px 16px;
		}

		.recent-item {
			grid-template-columns: auto minmax(0, 1fr);
			padding: 10px 8px;
			gap: 10px;
		}

		.recent-side {
			grid-column: 2;
			flex-direction: row;
			align-items: center;
			justify-content: flex-start;
			gap: 10px;
		}

		.recent-prompt {
			white-space: normal;
			display: -webkit-box;
			-webkit-line-clamp: 2;
			-webkit-box-orient: vertical;
		}
	}

	@media (max-width: 480px) {
		.project-filter {
			flex: 1;
			max-width: none;
		}
	}
</style>
