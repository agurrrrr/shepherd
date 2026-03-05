<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPatch, apiPost } from '$lib/api.js';

	let configData = {};
	let loaded = false;
	let saving = false;
	let saveMsg = '';
	let restarting = false;

	onMount(async () => {
		const res = await apiGet('/api/config');
		if (res?.data) configData = res.data;
		loaded = true;
	});

	async function save() {
		saving = true;
		saveMsg = '';
		const res = await apiPatch('/api/config', {
			language: configData.language,
			default_provider: configData.default_provider,
			max_sheep: parseInt(configData.max_sheep) || 12,
			auto_approve: configData.auto_approve,
			session_reuse: configData.session_reuse,
			include_task_history: configData.include_task_history,
			include_mcp_guide: configData.include_mcp_guide
		});
		if (res?.success) {
			saveMsg = 'Saved';
		} else {
			saveMsg = 'Error: ' + (res?.message || 'Failed to save');
		}
		saving = false;
		setTimeout(() => saveMsg = '', 3000);
	}

	async function restart() {
		if (!confirm('Restart the server?')) return;
		restarting = true;
		try {
			await apiPost('/api/system/restart', {});
		} catch {
			// Expected: server shuts down
		}
		// Wait and reload page
		setTimeout(() => {
			window.location.reload();
		}, 3000);
	}
</script>

<div class="page">
	<h1 class="page-title">Settings</h1>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else}
		<div class="settings-form card">
			<div class="setting-row">
				<label>Language</label>
				<select class="input" bind:value={configData.language}>
					<option value="ko">한국어</option>
					<option value="en">English</option>
				</select>
			</div>

			<div class="setting-row">
				<label>Default Provider</label>
				<select class="input" bind:value={configData.default_provider}>
					<option value="claude">Claude</option>
					<option value="opencode">OpenCode</option>
					<option value="auto">Auto</option>
				</select>
			</div>

			<div class="setting-row">
				<label>Max Sheep</label>
				<input class="input" type="number" bind:value={configData.max_sheep} min="1" max="50" />
			</div>

			<div class="setting-row">
				<label>Auto Approve</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.auto_approve} />
					<span>{configData.auto_approve ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="setting-section">Prompt Injection</div>

			<div class="setting-row">
				<label>Session Reuse</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.session_reuse} />
					<span>{configData.session_reuse ? 'Reuse' : 'Fresh'}</span>
				</label>
			</div>

			<div class="setting-row">
				<label>Task History</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.include_task_history} />
					<span>{configData.include_task_history ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="setting-row">
				<label>MCP Guide</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.include_mcp_guide} />
					<span>{configData.include_mcp_guide ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="setting-row readonly">
				<label>Server Host</label>
				<span class="mono">{configData.server_host}:{configData.server_port}</span>
			</div>

			<div class="setting-row readonly">
				<label>Workspace</label>
				<span class="mono">{configData.workspace_path || '(not set)'}</span>
			</div>

			<div class="setting-actions">
				<button class="btn btn-primary" onclick={save} disabled={saving}>
					{saving ? 'Saving...' : 'Save Settings'}
				</button>
				<button class="btn btn-restart" onclick={restart} disabled={restarting}>
					{restarting ? 'Restarting...' : 'Restart Server'}
				</button>
				{#if saveMsg}
					<span class="save-msg" class:error={saveMsg.startsWith('Error')}>{saveMsg}</span>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.page-title { font-size: 20px; font-weight: 600; margin-bottom: 20px; }
	.text-muted { color: var(--text-secondary); }

	.settings-form {
		max-width: 500px;
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.setting-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 16px;
	}

	.setting-row label {
		font-size: 14px;
		font-weight: 500;
		min-width: 140px;
	}

	.setting-row .input {
		flex: 1;
		max-width: 200px;
	}

	.setting-row.readonly {
		color: var(--text-secondary);
	}

	.setting-section {
		font-size: 12px;
		font-weight: 600;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.5px;
		padding-top: 12px;
		border-top: 1px solid var(--border);
	}

	.toggle {
		display: flex;
		align-items: center;
		gap: 8px;
		cursor: pointer;
		font-weight: 400;
		min-width: 0;
	}

	.setting-actions {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-top: 8px;
		padding-top: 16px;
		border-top: 1px solid var(--border);
	}

	.save-msg {
		font-size: 13px;
		color: var(--success);
	}

	.save-msg.error {
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

	@media (max-width: 768px) {
		.settings-form {
			max-width: none;
		}

		.setting-row {
			flex-direction: column;
			align-items: stretch;
			gap: 6px;
		}

		.setting-row label {
			min-width: 0;
		}

		.setting-row .input {
			max-width: none;
		}

		.setting-actions {
			flex-wrap: wrap;
		}
	}
</style>
