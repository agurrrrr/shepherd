<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { projects } from '$lib/stores.js';
	import ScheduleForm from '$lib/components/ScheduleForm.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Icon from '$lib/components/Icon.svelte';

	let schedules = $state([]);
	let loaded = $state(false);
	let unsubs = [];

	// Create/Edit modal
	let showForm = $state(false);
	let editingSchedule = $state(null);

	onMount(async () => {
		await loadSchedules();

		unsubs.push(onSSE('schedule_created', () => loadSchedules()));
		unsubs.push(onSSE('schedule_updated', () => loadSchedules()));
		unsubs.push(onSSE('schedule_deleted', () => loadSchedules()));
		unsubs.push(onSSE('schedule_triggered', () => loadSchedules()));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	async function loadSchedules() {
		const res = await apiGet('/api/schedules');
		if (res?.data) {
			schedules = res.data;
		}
		loaded = true;
	}

	function openCreate() {
		editingSchedule = null;
		showForm = true;
	}

	function openEdit(sc) {
		editingSchedule = sc;
		showForm = true;
	}

	function closeForm() {
		showForm = false;
		editingSchedule = null;
	}

	async function handleSave(data) {
		const projectName = data.project;
		if (editingSchedule) {
			const res = await apiPatch(`/api/projects/${encodeURIComponent(projectName)}/schedules/${editingSchedule.id}`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to update');
		} else {
			const res = await apiPost(`/api/projects/${encodeURIComponent(projectName)}/schedules`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to create');
		}
		closeForm();
		await loadSchedules();
	}

	async function toggleEnabled(sc) {
		await apiPatch(`/api/projects/${encodeURIComponent(sc.project)}/schedules/${sc.id}`, {
			enabled: !sc.enabled
		});
		await loadSchedules();
	}

	async function deleteSchedule(sc) {
		if (!confirm(`Delete schedule "${sc.name}"?`)) return;
		await apiDelete(`/api/projects/${encodeURIComponent(sc.project)}/schedules/${sc.id}`);
		await loadSchedules();
	}

	async function runNow(sc) {
		const res = await apiPost(`/api/projects/${encodeURIComponent(sc.project)}/schedules/${sc.id}/run`, {});
		if (res?.data?.task_id) {
			await loadSchedules();
		}
	}

	function formatSchedule(sc) {
		if (sc.schedule_type === 'cron') return sc.cron_expr;
		const secs = sc.interval_seconds;
		if (secs >= 86400 && secs % 86400 === 0) return `Every ${secs / 86400}d`;
		if (secs >= 3600 && secs % 3600 === 0) return `Every ${secs / 3600}h`;
		return `Every ${secs / 60}m`;
	}

	// Plain-English reading of the common cron shapes; '' when unsure.
	const DOW = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
	const DOW_LONG = ['Sundays', 'Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays'];
	function humanCron(expr) {
		const p = String(expr || '').trim().split(/\s+/);
		if (p.length !== 5) return '';
		const [m, h, dom, mon, dow] = p;
		const num = (v) => /^\d+$/.test(v);
		if (num(m) && h === '*' && dom === '*' && mon === '*' && dow === '*') return `Hourly at :${m.padStart(2, '0')}`;
		if (!num(m) || !num(h) || mon !== '*') return '';
		const t = `${h.padStart(2, '0')}:${m.padStart(2, '0')}`;
		if (dom === '*' && dow === '*') return `Daily at ${t}`;
		if (dom === '*' && num(dow)) return `${DOW_LONG[+dow % 7]} at ${t}`;
		if (dom === '*' && /^\d-\d$/.test(dow)) return `${DOW[+dow[0] % 7]}–${DOW[+dow[2] % 7]} at ${t}`;
		if (num(dom) && dow === '*') return `Monthly on the ${dom}${['th', 'st', 'nd', 'rd'][(+dom % 10 > 3 || [11, 12, 13].includes(+dom)) ? 0 : +dom % 10]} at ${t}`;
		return '';
	}

	// Instrument Serif draws "1" almost like "l" at this size, so numerals in
	// the cadence label are set in the UI sans ("Every 1h", "Daily at 11:00").
	function cadenceParts(text) {
		return String(text).split(/(\d+(?::\d+)?)/).filter(Boolean).map((t) => ({ t, num: /^\d/.test(t) }));
	}

	let enabledCount = $derived(schedules.filter((s) => s.enabled).length);

	function truncate(str, max) {
		if (!str) return '';
		return str.length > max ? str.substring(0, max) + '...' : str;
	}
</script>

<div class="page">
	<PageHeader
		eyebrow="Watch · Rhythm"
		title="Schedules"
		count={schedules.length}
		subtitle={`Recurring work the flock picks up on its own. ${enabledCount} of ${schedules.length} active.`}
	>
		<button class="btn btn-primary" onclick={openCreate}>
			<Icon name="plus" size={14} stroke={2.2} />
			New schedule
		</button>
	</PageHeader>

	{#if !loaded}
		<div class="schedule-list" aria-busy="true">
			{#each Array(5) as _}
				<div class="card schedule-item"><div class="skeleton" style="height: 14px; width: 30%"></div><div class="skeleton" style="height: 10px; width: 80%; margin-top: 14px"></div></div>
			{/each}
		</div>
	{:else if schedules.length === 0 && !showForm}
		<div class="card">
			<EmptyState icon="list" title="No rhythm yet" description="Create a schedule to run tasks automatically — nightly checks, weekly reviews, anything recurring.">
				<button class="btn btn-primary" onclick={openCreate}><Icon name="plus" size={14} /> New schedule</button>
			</EmptyState>
		</div>
	{:else}
		<div class="schedule-list">
			{#each schedules as sc, i (sc.id)}
				{@const human = sc.schedule_type === 'cron' ? humanCron(sc.cron_expr) : ''}
				<article class="card schedule-item reveal" class:disabled={!sc.enabled} style="--i: {i}">
					<div class="when">
						{#if sc.schedule_type === 'cron' && !human}
							<span class="when-main raw mono">{sc.cron_expr}</span>
						{:else}
							<span class="when-main">{#each cadenceParts(human || formatSchedule(sc)) as part}{#if part.num}<span class="when-num">{part.t}</span>{:else}{part.t}{/if}{/each}</span>
						{/if}
						<span class="when-sub mono">
							<span class="type" class:cron={sc.schedule_type === 'cron'}>{sc.schedule_type}</span>
							{#if human}{sc.cron_expr}{/if}
						</span>
					</div>

					<div class="what">
						<div class="schedule-name-row">
							<span class="schedule-name">{sc.name}</span>
							<a class="chip" href="/projects/{encodeURIComponent(sc.project)}">{sc.project}</a>
						</div>
						<p class="schedule-prompt">{truncate(sc.prompt, 220)}</p>
						<div class="schedule-meta mono num">
							{#if sc.next_run}
								<span><span class="k">Next</span> {sc.next_run}</span>
							{/if}
							{#if sc.last_run}
								<span><span class="k">Last</span> {sc.last_run}</span>
							{/if}
						</div>
					</div>

					<div class="schedule-actions">
						<label class="enable" title={sc.enabled ? 'Disable' : 'Enable'}>
							<input type="checkbox" class="switch" checked={sc.enabled} onchange={() => toggleEnabled(sc)} aria-label={sc.enabled ? `Disable ${sc.name}` : `Enable ${sc.name}`} />
						</label>
						<button class="btn btn-sm" onclick={() => runNow(sc)} title="Run now">
							<Icon name="play" size={11} stroke={2.4} /> Run
						</button>
						<button class="btn btn-sm btn-ghost btn-icon" onclick={() => openEdit(sc)} title="Edit" aria-label="Edit {sc.name}">
							<Icon name="edit" size={14} />
						</button>
						<button class="btn btn-sm btn-ghost btn-icon del" onclick={() => deleteSchedule(sc)} title="Delete" aria-label="Delete {sc.name}">
							<Icon name="trash" size={14} />
						</button>
					</div>
				</article>
			{/each}
		</div>
	{/if}

	<!-- Create/Edit Modal — only Close/Cancel closes (no outside-click dismiss) -->
	{#if showForm}
		<div class="modal-overlay">
			<div class="modal-content card" role="dialog" aria-modal="true" aria-label={editingSchedule ? 'Edit schedule' : 'New schedule'}>
				<div class="modal-header">
					<h2>{editingSchedule ? 'Edit schedule' : 'New schedule'}</h2>
					<button class="btn btn-sm btn-ghost btn-icon" onclick={closeForm} aria-label="Close"><Icon name="x" size={15} /></button>
				</div>
				<ScheduleForm
					schedule={editingSchedule}
					projects={$projects}
					onSave={handleSave}
					onCancel={closeForm}
				/>
			</div>
		</div>
	{/if}
</div>

<style>
	.page { max-width: var(--content-max); }

	.schedule-list {
		display: flex;
		flex-direction: column;
		gap: 10px;
	}

	.schedule-item {
		display: grid;
		grid-template-columns: 190px minmax(0, 1fr) auto;
		gap: 20px;
		align-items: start;
		padding: 18px 20px;
		transition: border-color var(--dur-1) ease, opacity var(--dur-2) ease;
	}

	.schedule-item:hover {
		border-color: var(--border-strong);
	}

	.schedule-item.disabled {
		opacity: 0.62;
	}

	.schedule-item.disabled:hover {
		opacity: 0.85;
	}

	.when {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding-right: 18px;
		border-right: 1px solid var(--border-subtle);
		min-height: 100%;
	}

	.when-main {
		font-family: var(--font-display);
		font-size: 22px;
		line-height: 1.1;
		color: var(--text-primary);
	}

	.when-num {
		font-family: var(--font-sans);
		font-size: 0.86em;
		font-weight: var(--fw-medium);
		font-variant-numeric: tabular-nums;
		letter-spacing: -0.02em;
	}

	.when-main.raw {
		font-size: var(--fs-md);
		letter-spacing: 0.02em;
	}

	.disabled .when-main {
		color: var(--text-secondary);
	}

	.when-sub {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.type {
		padding: 0 6px;
		line-height: 16px;
		border-radius: var(--radius-full);
		font-size: 9.5px;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		background: rgba(237, 230, 218, 0.05);
		color: var(--text-secondary);
	}

	.type.cron {
		background: var(--info-soft);
		color: var(--info);
	}

	.what {
		display: flex;
		flex-direction: column;
		gap: 8px;
		min-width: 0;
	}

	.schedule-name-row {
		display: flex;
		align-items: center;
		gap: 10px;
		flex-wrap: wrap;
		min-width: 0;
	}

	.schedule-name {
		font-weight: var(--fw-semibold);
		font-size: 15px;
		letter-spacing: -0.01em;
	}

	a.chip:hover {
		color: var(--text-primary);
		text-decoration: none;
		border-color: var(--border-strong);
	}

	.schedule-prompt {
		font-size: var(--fs-sm);
		line-height: 1.55;
		color: var(--text-secondary);
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}

	.schedule-meta {
		display: flex;
		gap: 16px;
		flex-wrap: wrap;
		font-size: var(--fs-2xs);
		color: var(--text-secondary);
	}

	.k {
		margin-right: 4px;
		font-size: 9.5px;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.schedule-actions {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.enable {
		display: inline-flex;
		align-items: center;
		padding: 0 8px 0 0;
		cursor: pointer;
	}

	.del:hover {
		color: var(--danger);
		background: var(--danger-soft);
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 18px;
	}

	.modal-header h2 {
		font-family: var(--font-display);
		font-size: 30px;
		font-weight: 400;
	}

	@media (max-width: 900px) {
		.schedule-item {
			grid-template-columns: minmax(0, 1fr);
			gap: 12px;
		}

		.when {
			flex-direction: row;
			align-items: baseline;
			justify-content: space-between;
			padding-right: 0;
			padding-bottom: 10px;
			border-right: none;
			border-bottom: 1px solid var(--border-subtle);
		}

		.schedule-actions {
			justify-content: flex-end;
		}
	}
</style>
