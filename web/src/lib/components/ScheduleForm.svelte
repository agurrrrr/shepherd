<script>
	import { apiGet } from '$lib/api.js';

	let { schedule = null, projects = [], fixedProject = '', onSave, onCancel } = $props();

	let name = $state(schedule?.name ?? '');
	let prompt = $state(schedule?.prompt ?? '');
	let scheduleType = $state(schedule?.schedule_type ?? 'cron');
	let cronExpr = $state(schedule?.cron_expr ?? '');
	let intervalValue = $state(schedule ? Math.floor((schedule.interval_seconds || 3600) / getIntervalDivisor(schedule.interval_seconds || 3600)) : 1);
	let intervalUnit = $state(schedule ? guessIntervalUnit(schedule.interval_seconds || 3600) : 'hours');
	let enabled = $state(schedule?.enabled ?? true);
	let selectedProject = $state(fixedProject || schedule?.project || '');

	let previewTimes = $state([]);
	let previewLoading = $state(false);
	let saving = $state(false);
	let error = $state('');

	const cronPresets = [
		{ label: '매 시간', expr: '0 * * * *' },
		{ label: '매일 오전 9시', expr: '0 9 * * *' },
		{ label: '평일 오전 9시', expr: '0 9 * * 1-5' },
		{ label: '매주 월요일', expr: '0 9 * * 1' },
		{ label: '매월 1일', expr: '0 9 1 * *' },
		{ label: '6시간마다', expr: '0 */6 * * *' },
	];

	function guessIntervalUnit(secs) {
		if (secs % 86400 === 0) return 'days';
		if (secs % 3600 === 0) return 'hours';
		return 'minutes';
	}

	function getIntervalDivisor(secs) {
		if (secs % 86400 === 0) return 86400;
		if (secs % 3600 === 0) return 3600;
		return 60;
	}

	function getIntervalSeconds() {
		const multiplier = intervalUnit === 'days' ? 86400 : intervalUnit === 'hours' ? 3600 : 60;
		return intervalValue * multiplier;
	}

	function selectPreset(expr) {
		cronExpr = expr;
		loadPreview();
	}

	async function loadPreview() {
		if (!cronExpr) { previewTimes = []; return; }
		previewLoading = true;
		try {
			const res = await apiGet(`/api/schedules/preview?cron=${encodeURIComponent(cronExpr)}`);
			previewTimes = res?.data?.next_runs || [];
		} catch {
			previewTimes = [];
		}
		previewLoading = false;
	}

	async function handleSave() {
		error = '';
		if (!name.trim()) { error = 'Name is required'; return; }
		if (!prompt.trim()) { error = 'Prompt is required'; return; }
		if (!selectedProject && !fixedProject) { error = 'Project is required'; return; }
		if (scheduleType === 'cron' && !cronExpr.trim()) { error = 'Cron expression is required'; return; }
		if (scheduleType === 'interval' && intervalValue <= 0) { error = 'Interval must be positive'; return; }

		saving = true;
		try {
			await onSave({
				name: name.trim(),
				prompt: prompt.trim(),
				schedule_type: scheduleType,
				cron_expr: scheduleType === 'cron' ? cronExpr.trim() : '',
				interval_seconds: scheduleType === 'interval' ? getIntervalSeconds() : 0,
				enabled,
				project: fixedProject || selectedProject,
			});
		} catch (e) {
			error = e.message || 'Failed to save';
		}
		saving = false;
	}
</script>

<div class="schedule-form">
	{#if error}
		<div class="form-error">{error}</div>
	{/if}

	{#if !fixedProject}
		<div class="form-group">
			<label class="form-label">Project</label>
			<select class="input" bind:value={selectedProject}>
				<option value="">Select project...</option>
				{#each projects as p}
					<option value={p.name}>{p.name}</option>
				{/each}
			</select>
		</div>
	{/if}

	<div class="form-group">
		<label class="form-label">Name</label>
		<input class="input" type="text" bind:value={name} placeholder="e.g. Daily PR Review" />
	</div>

	<div class="form-group">
		<label class="form-label">Prompt</label>
		<textarea class="input textarea" bind:value={prompt} rows="3" placeholder="e.g. Review all open PRs and summarize"></textarea>
	</div>

	<div class="form-group">
		<label class="form-label">Schedule Type</label>
		<div class="type-toggle">
			<button class="type-btn" class:active={scheduleType === 'cron'} onclick={() => scheduleType = 'cron'}>Cron</button>
			<button class="type-btn" class:active={scheduleType === 'interval'} onclick={() => scheduleType = 'interval'}>Interval</button>
		</div>
	</div>

	{#if scheduleType === 'cron'}
		<div class="form-group">
			<label class="form-label">Presets</label>
			<div class="presets">
				{#each cronPresets as preset}
					<button class="preset-btn" class:active={cronExpr === preset.expr} onclick={() => selectPreset(preset.expr)}>
						{preset.label}
					</button>
				{/each}
			</div>
		</div>

		<div class="form-group">
			<label class="form-label">Cron Expression</label>
			<div class="cron-input-row">
				<input class="input mono" type="text" bind:value={cronExpr} placeholder="0 9 * * *" onblur={loadPreview} />
				<button class="btn" onclick={loadPreview}>Preview</button>
			</div>
			<div class="cron-help">minute hour day month weekday (0=Sun)</div>
		</div>

		{#if previewTimes.length > 0}
			<div class="preview-box">
				<div class="preview-title">Next runs:</div>
				{#each previewTimes as t}
					<div class="preview-time">{t}</div>
				{/each}
			</div>
		{/if}
	{:else}
		<div class="form-group">
			<label class="form-label">Interval</label>
			<div class="interval-row">
				<input class="input interval-input" type="number" min="1" bind:value={intervalValue} />
				<select class="input interval-unit" bind:value={intervalUnit}>
					<option value="minutes">Minutes</option>
					<option value="hours">Hours</option>
					<option value="days">Days</option>
				</select>
			</div>
		</div>
	{/if}

	<div class="form-group">
		<label class="form-label">
			<input type="checkbox" bind:checked={enabled} />
			Enabled
		</label>
	</div>

	<div class="form-actions">
		{#if onCancel}
			<button class="btn" onclick={onCancel}>Cancel</button>
		{/if}
		<button class="btn btn-primary" onclick={handleSave} disabled={saving}>
			{saving ? 'Saving...' : (schedule ? 'Update' : 'Create')}
		</button>
	</div>
</div>

<style>
	.schedule-form {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.form-error {
		color: var(--danger);
		background: rgba(248, 81, 73, 0.1);
		border: 1px solid var(--danger);
		border-radius: var(--radius);
		padding: 8px 12px;
		font-size: 13px;
	}

	.form-group {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.form-label {
		font-size: 13px;
		font-weight: 500;
		color: var(--text-secondary);
	}

	.textarea {
		resize: vertical;
		min-height: 60px;
		font-family: inherit;
	}

	.type-toggle {
		display: flex;
		gap: 4px;
	}

	.type-btn {
		padding: 6px 16px;
		border: 1px solid var(--border);
		background: var(--bg-primary);
		color: var(--text-secondary);
		border-radius: var(--radius);
		cursor: pointer;
		font-size: 13px;
	}

	.type-btn.active {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.presets {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
	}

	.preset-btn {
		padding: 4px 10px;
		border: 1px solid var(--border);
		background: var(--bg-primary);
		color: var(--text-secondary);
		border-radius: 12px;
		cursor: pointer;
		font-size: 12px;
		transition: all 0.15s;
	}

	.preset-btn:hover {
		border-color: var(--accent);
		color: var(--accent);
	}

	.preset-btn.active {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.cron-input-row {
		display: flex;
		gap: 8px;
	}

	.cron-input-row .input {
		flex: 1;
	}

	.cron-help {
		font-size: 11px;
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}

	.preview-box {
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 8px 12px;
		font-size: 12px;
	}

	.preview-title {
		font-weight: 500;
		margin-bottom: 4px;
		color: var(--text-secondary);
	}

	.preview-time {
		font-family: var(--font-mono);
		color: var(--accent);
		padding: 1px 0;
	}

	.interval-row {
		display: flex;
		gap: 8px;
	}

	.interval-input {
		width: 100px;
	}

	.interval-unit {
		width: 120px;
	}

	.form-actions {
		display: flex;
		gap: 8px;
		justify-content: flex-end;
		padding-top: 8px;
	}

	.btn-primary {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.btn-primary:hover {
		opacity: 0.9;
	}

	.btn-primary:disabled {
		opacity: 0.5;
	}
</style>
