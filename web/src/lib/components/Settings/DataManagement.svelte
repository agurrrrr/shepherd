<script>
	import { apiDownload, apiUpload } from '$lib/api.js';

	/** @type {Array<{name: string}>} */
	export let projectsList;

	let backupBusy = false;
	let backupMsg = '';
	let exportBusy = false;
	let exportProject = '';
	let exportMsg = '';
	let importFile = null;
	let importBusy = false;
	let importPreview = null;
	let importResult = null;
	let importMsg = '';

	function triggerDownload(blob, filename) {
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = filename;
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
		setTimeout(() => URL.revokeObjectURL(url), 1000);
	}

	async function downloadBackup() {
		backupBusy = true;
		backupMsg = '';
		try {
			const blob = await apiDownload('/api/settings/db-backup');
			if (!blob) {
				backupMsg = 'Error: backup failed';
				return;
			}
			const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
			triggerDownload(blob, `shepherd-${ts}.db`);
			backupMsg = 'Downloaded';
		} catch (err) {
			backupMsg = 'Error: ' + (err?.message || 'download failed');
		} finally {
			backupBusy = false;
			setTimeout(() => backupMsg = '', 3000);
		}
	}

	async function exportTasks() {
		exportBusy = true;
		exportMsg = '';
		try {
			const url = exportProject
				? `/api/settings/tasks-export?project=${encodeURIComponent(exportProject)}`
				: '/api/settings/tasks-export';
			const blob = await apiDownload(url);
			if (!blob) {
				exportMsg = 'Error: export failed';
				return;
			}
			const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
			const namePart = exportProject ? `-${exportProject}` : '';
			triggerDownload(blob, `shepherd-tasks${namePart}-${ts}.jsonl`);
			exportMsg = 'Downloaded';
		} catch (err) {
			exportMsg = 'Error: ' + (err?.message || 'export failed');
		} finally {
			exportBusy = false;
			setTimeout(() => exportMsg = '', 3000);
		}
	}

	function onImportFileChange(e) {
		const f = e.target.files?.[0] || null;
		importFile = f;
		importPreview = null;
		importResult = null;
		importMsg = '';
	}

	async function previewImport() {
		if (!importFile) return;
		importBusy = true;
		importMsg = '';
		importResult = null;
		try {
			const fd = new FormData();
			fd.append('file', importFile);
			const res = await apiUpload('/api/settings/tasks-import-preview', fd);
			if (res?.success) {
				importPreview = res.data;
			} else {
				importPreview = null;
				importMsg = 'Error: ' + (res?.message || 'preview failed');
			}
		} finally {
			importBusy = false;
		}
	}

	async function confirmImport() {
		if (!importFile) return;
		if (!confirm('Import these tasks now?')) return;
		importBusy = true;
		importMsg = '';
		try {
			const fd = new FormData();
			fd.append('file', importFile);
			const res = await apiUpload('/api/settings/tasks-import', fd);
			if (res?.success) {
				importResult = res.data;
				importPreview = null;
			} else {
				importMsg = 'Error: ' + (res?.message || 'import failed');
			}
		} finally {
			importBusy = false;
		}
	}
</script>

<!-- Backup -->
<div class="card">
	<h2 class="section-title">Database Backup</h2>
	<p class="sync-desc">
		Download a consistent SQLite snapshot via <code>VACUUM INTO</code>. Same-machine restore: stop shepherd, replace <code>shepherd.db</code> with the downloaded file, then start it again.
	</p>
	<div class="sync-actions">
		<button class="btn btn-sm" onclick={downloadBackup} disabled={backupBusy}>
			{backupBusy ? 'Preparing...' : 'Download Backup'}
		</button>
		{#if backupMsg}
			<span class="sync-msg" class:error={backupMsg.startsWith('Error')}>{backupMsg}</span>
		{/if}
	</div>
</div>

<!-- Export -->
<div class="card">
	<h2 class="section-title">Task History — Export</h2>
	<p class="sync-desc">
		Export task records as JSONL (one task per line). Project records are <strong>not</strong> included — paths are machine-specific. On the target machine, the receiving project must already exist with the same name.
	</p>
	<div class="setting-row">
		<label>Project</label>
		<select class="input" bind:value={exportProject}>
			<option value="">(All projects)</option>
			{#each projectsList as p}
				<option value={p.name}>{p.name}</option>
			{/each}
		</select>
	</div>
	<div class="sync-actions">
		<button class="btn btn-sm" onclick={exportTasks} disabled={exportBusy}>
			{exportBusy ? 'Preparing...' : 'Export Tasks'}
		</button>
		{#if exportMsg}
			<span class="sync-msg" class:error={exportMsg.startsWith('Error')}>{exportMsg}</span>
		{/if}
	</div>
</div>

<!-- Import -->
<div class="card">
	<h2 class="section-title">Task History — Import</h2>
	<p class="sync-desc">
		Import a JSONL dump from another machine. Records are matched by <code>project_name</code>; tasks for projects that don't exist here are skipped. Re-importing the same dump is safe — duplicates are detected by (project, prompt, created_at).
	</p>
	<div class="import-controls">
		<input type="file" accept=".jsonl,.ndjson,application/x-ndjson,application/json" onchange={onImportFileChange} />
		<button class="btn btn-sm" onclick={previewImport} disabled={!importFile || importBusy}>
			{importBusy ? 'Working...' : 'Preview'}
		</button>
		{#if importPreview}
			<button class="btn btn-sm btn-restart" onclick={confirmImport} disabled={importBusy}>
				{importBusy ? 'Importing...' : 'Confirm Import'}
			</button>
		{/if}
	</div>
	{#if importMsg}
		<div class="sync-msg" class:error={importMsg.startsWith('Error')}>{importMsg}</div>
	{/if}
	{#if importPreview}
		<div class="import-preview">
			<div><strong>Total in file:</strong> {importPreview.total}</div>
			<div><strong>Will import:</strong> {importPreview.matched}</div>
			<div><strong>Will skip (no matching project):</strong> {importPreview.skipped}</div>
			{#if importPreview.matched_by_project && Object.keys(importPreview.matched_by_project).length > 0}
				<div class="preview-detail">
					<div class="preview-detail-title">Matched by project:</div>
					<ul>
						{#each Object.entries(importPreview.matched_by_project) as [name, count]}
							<li><code>{name}</code>: {count}</li>
						{/each}
					</ul>
				</div>
			{/if}
			{#if importPreview.skipped_by_project && Object.keys(importPreview.skipped_by_project).length > 0}
				<div class="preview-detail">
					<div class="preview-detail-title">Skipped (project not found here):</div>
					<ul>
						{#each Object.entries(importPreview.skipped_by_project) as [name, count]}
							<li><code>{name}</code>: {count}</li>
						{/each}
					</ul>
				</div>
			{/if}
		</div>
	{/if}
	{#if importResult}
		<div class="import-result">
			<div><strong>Imported:</strong> {importResult.imported}</div>
			<div><strong>Skipped:</strong> {importResult.skipped}</div>
			<div><strong>Duplicates:</strong> {importResult.duplicates}</div>
			{#if importResult.failed > 0}
				<div class="error-text"><strong>Failed:</strong> {importResult.failed}</div>
			{/if}
		</div>
	{/if}
</div>

<style>
	.card {
		max-width: 640px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.section-title {
		font-size: 16px;
		font-weight: 600;
		margin-bottom: 16px;
	}
	.sync-desc {
		font-size: 13px;
		color: var(--text-secondary);
		line-height: 1.5;
		margin-bottom: 12px;
	}
	.sync-desc code {
		background: var(--bg-tertiary, #2a2a2a);
		padding: 1px 5px;
		border-radius: 3px;
		font-size: 12px;
	}
	.sync-actions {
		display: flex;
		align-items: center;
		gap: 12px;
	}
	.sync-msg {
		font-size: 13px;
		color: var(--success);
	}
	.sync-msg.error {
		color: var(--danger);
	}

	.setting-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		column-gap: 16px;
		row-gap: 6px;
	}
	.setting-row > label:not(.toggle) {
		font-size: 14px;
		font-weight: 500;
		flex: 0 0 140px;
	}
	.setting-row .input {
		flex: 1 1 200px;
		max-width: 240px;
		min-width: 0;
	}

	.import-controls {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
		align-items: center;
	}
	.import-preview,
	.import-result {
		font-size: 13px;
		line-height: 1.6;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: 10px 12px;
	}
	.import-preview ul,
	.import-result ul {
		margin: 4px 0 0;
		padding-left: 18px;
	}
	.preview-detail {
		margin-top: 8px;
	}
	.preview-detail-title {
		font-weight: 600;
		font-size: 12px;
		color: var(--text-secondary);
	}
	.error-text {
		color: var(--danger);
	}
	.btn-restart {
		padding: 6px 16px;
		font-size: 13px;
		font-weight: 600;
		background: var(--bg-tertiary);
		color: var(--text-primary);
		border: 1px solid var(--border);
		border-radius: 6px;
		cursor: pointer;
		transition: background 0.15s, border-color 0.15s;
	}
	.btn-restart:hover {
		border-color: var(--accent);
		background: var(--bg-secondary);
	}
	.btn-restart:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.btn-sm {
		padding: 4px 12px;
		font-size: 12px;
		font-weight: 600;
		background: var(--accent);
		color: white;
		border: none;
		border-radius: 6px;
		cursor: pointer;
		transition: opacity 0.15s;
	}
	.btn-sm:hover { opacity: 0.85; }
	.btn-sm:disabled { opacity: 0.5; cursor: not-allowed; }

	@media (max-width: 768px) {
		.card { max-width: none; }
	}
</style>
