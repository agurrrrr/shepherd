<script>
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { projects } from '$lib/stores.js';
	import ScheduleForm from '$lib/components/ScheduleForm.svelte';

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

	function truncate(str, max) {
		if (!str) return '';
		return str.length > max ? str.substring(0, max) + '...' : str;
	}
</script>

<div class="page">
	<div class="page-header">
		<h1 class="page-title">Schedules</h1>
		<button class="btn btn-primary" onclick={openCreate}>+ New Schedule</button>
	</div>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if schedules.length === 0 && !showForm}
		<div class="card empty-state">
			<p>No schedules configured.</p>
			<p class="text-muted">Create a schedule to run tasks automatically.</p>
		</div>
	{:else}
		<div class="schedule-list">
			{#each schedules as sc (sc.id)}
				<div class="card schedule-item" class:disabled={!sc.enabled}>
					<div class="schedule-header">
						<div class="schedule-name-row">
							<span class="schedule-name">{sc.name}</span>
							<span class="schedule-type badge" class:cron={sc.schedule_type === 'cron'}>{sc.schedule_type}</span>
							<span class="schedule-expr mono">{formatSchedule(sc)}</span>
						</div>
						<div class="schedule-actions">
							<button class="btn btn-sm" onclick={() => runNow(sc)} title="Run now">Run</button>
							<button class="btn btn-sm" onclick={() => toggleEnabled(sc)}>
								{sc.enabled ? 'Disable' : 'Enable'}
							</button>
							<button class="btn btn-sm" onclick={() => openEdit(sc)}>Edit</button>
							<button class="btn btn-sm btn-danger" onclick={() => deleteSchedule(sc)}>Delete</button>
						</div>
					</div>
					<div class="schedule-prompt">{truncate(sc.prompt, 150)}</div>
					<div class="schedule-meta">
						<span class="meta-project">{sc.project}</span>
						{#if sc.next_run}
							<span>Next: {sc.next_run}</span>
						{/if}
						{#if sc.last_run}
							<span>Last: {sc.last_run}</span>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<!-- Create/Edit Modal -->
	{#if showForm}
		<div class="modal-overlay" onclick={closeForm}>
			<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
				<div class="modal-header">
					<h2>{editingSchedule ? 'Edit Schedule' : 'New Schedule'}</h2>
					<button class="btn" onclick={closeForm}>Close</button>
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
	.page-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 20px;
	}

	.page-title { font-size: 20px; font-weight: 600; }
	.text-muted { color: var(--text-secondary); }

	.btn-primary {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.schedule-list {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.schedule-item {
		transition: border-color 0.15s;
	}

	.schedule-item:hover {
		border-color: var(--accent);
	}

	.schedule-item.disabled {
		opacity: 0.5;
	}

	.schedule-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 6px;
		gap: 8px;
	}

	.schedule-name-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		min-width: 0;
	}

	.schedule-name {
		font-weight: 600;
		font-size: 14px;
	}

	.badge {
		font-size: 10px;
		padding: 2px 6px;
		border-radius: 8px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		text-transform: uppercase;
		font-weight: 600;
	}

	.badge.cron {
		background: rgba(56, 139, 253, 0.15);
		color: var(--accent);
	}

	.schedule-expr {
		font-size: 12px;
		color: var(--text-secondary);
	}

	.schedule-actions {
		display: flex;
		gap: 4px;
		flex-shrink: 0;
	}

	.btn-sm {
		padding: 3px 8px;
		font-size: 11px;
	}

	.btn-danger {
		color: var(--danger);
		border-color: var(--danger);
	}

	.btn-danger:hover {
		background: rgba(248, 81, 73, 0.1);
	}

	.schedule-prompt {
		font-size: 13px;
		color: var(--text-primary);
		margin-bottom: 6px;
		line-height: 1.4;
	}

	.schedule-meta {
		display: flex;
		gap: 12px;
		font-size: 12px;
		color: var(--text-secondary);
	}

	.meta-project {
		color: var(--accent);
	}

	.empty-state {
		text-align: center;
		padding: 40px 20px;
	}

	/* Modal */
	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 100;
		padding: 20px;
	}

	.modal-content {
		width: 100%;
		max-width: 600px;
		max-height: 85vh;
		overflow-y: auto;
		padding: 24px;
	}

	.modal-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 20px;
	}

	.modal-header h2 { font-size: 18px; }

	@media (max-width: 768px) {
		.page-header {
			flex-direction: column;
			align-items: flex-start;
			gap: 12px;
		}

		.schedule-header {
			flex-direction: column;
			align-items: flex-start;
		}

		.schedule-actions {
			flex-wrap: wrap;
		}

		.schedule-meta {
			flex-wrap: wrap;
			gap: 6px;
		}

		.modal-content {
			padding: 16px;
			max-height: 90vh;
		}
	}
</style>
