<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPut } from '$lib/api.js';

	let { projectName } = $props();

	let mcpServers = $state([]);
	let loaded = $state(false);
	let saving = $state(false);
	let saveMsg = $state('');

	// Track per-server enabled state locally
	let serverEnabled = $state({});

	async function loadSettings() {
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/mcp-servers`);
		if (res?.data) {
			mcpServers = res.data;
			// Initialize local enabled state
			const enabled = {};
			for (const srv of res.data) {
				enabled[srv.name] = srv.project_enabled;
			}
			serverEnabled = enabled;
			loaded = true;
		}
	}

	function toggleServer(name) {
		serverEnabled = { ...serverEnabled, [name]: !serverEnabled[name] };
	}

	async function saveSettings() {
		saving = true;
		saveMsg = '';

		const body = {};
		for (const srv of mcpServers) {
			body[srv.name] = { enabled: serverEnabled[srv.name] };
		}

		const res = await apiPut(`/api/projects/${encodeURIComponent(projectName)}/mcp-servers`, body);
		if (res?.success || res?.data) {
			saveMsg = 'Settings saved';
		} else {
			saveMsg = 'Error: ' + (res?.message || 'Failed to save');
		}
		saving = false;
		setTimeout(() => { saveMsg = ''; }, 3000);
	}

	onMount(loadSettings);
</script>

<div class="project-settings">
	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if mcpServers.length === 0}
		<p class="text-muted">No MCP servers configured. Register MCP servers in Settings → Embedded tab first.</p>
	{:else}
		<div class="project-settings-section">
			<h4>🔌 MCP Servers</h4>
			<p class="hint">Toggle which MCP servers are available for this project.</p>

			<div class="mcp-toggle-list">
				{#each mcpServers as srv}
					<div class="mcp-toggle-item">
						<div class="mcp-toggle-info">
							<span class="mcp-toggle-name">{srv.label || srv.name}</span>
							<code class="mcp-toggle-id">{srv.name}</code>
							<span class="badge badge-transport">{srv.transport}</span>
							{#if srv.description}
								<span class="mcp-toggle-desc">{srv.description}</span>
							{/if}
						</div>
						<div class="mcp-toggle-control">
							<span class="mcp-toggle-status">{serverEnabled[srv.name] ? 'ON' : 'OFF'}</span>
							<label class="toggle">
								<input
									type="checkbox"
									checked={serverEnabled[srv.name]}
									onchange={() => toggleServer(srv.name)}
								/>
								<span class="toggle-slider"></span>
							</label>
						</div>
					</div>
				{/each}
			</div>

			<div class="project-settings-actions">
				<button class="btn btn-primary" onclick={saveSettings} disabled={saving}>
					{saving ? 'Saving...' : 'Save'}
				</button>
				{#if saveMsg}
					<span class="save-msg">{saveMsg}</span>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.text-muted { color: var(--text-secondary); }
	.hint {
		font-size: 12px;
		color: var(--text-muted);
		margin: 4px 0 12px;
	}

	.project-settings-section {
		margin-bottom: 24px;
	}
	.project-settings-section h4 {
		margin: 0 0 4px;
		font-size: 15px;
	}

	.mcp-toggle-list {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.mcp-toggle-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 10px 12px;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 8px;
		gap: 12px;
	}
	.mcp-toggle-info {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		min-width: 0;
	}
	.mcp-toggle-name {
		font-size: 14px;
		font-weight: 600;
	}
	.mcp-toggle-id {
		font-size: 11px;
		background: var(--bg-input);
		padding: 1px 6px;
		border-radius: 4px;
		color: var(--text-secondary);
	}
	.mcp-toggle-desc {
		font-size: 12px;
		color: var(--text-secondary);
	}
	.badge-transport {
		font-size: 10px;
		font-weight: 600;
		padding: 1px 6px;
		border-radius: 8px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		text-transform: uppercase;
	}

	.mcp-toggle-control {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-shrink: 0;
	}
	.mcp-toggle-status {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-secondary);
		min-width: 24px;
		text-align: center;
	}

	.toggle {
		position: relative;
		display: inline-block;
		width: 40px;
		height: 22px;
		cursor: pointer;
	}
	.toggle input {
		opacity: 0;
		width: 0;
		height: 0;
	}
	.toggle-slider {
		position: absolute;
		inset: 0;
		background: var(--bg-tertiary);
		border-radius: 22px;
		transition: background 0.2s;
	}
	.toggle-slider::before {
		content: '';
		position: absolute;
		width: 16px;
		height: 16px;
		left: 3px;
		top: 3px;
		background: white;
		border-radius: 50%;
		transition: transform 0.2s;
	}
	.toggle input:checked + .toggle-slider {
		background: var(--accent);
	}
	.toggle input:checked + .toggle-slider::before {
		transform: translateX(18px);
	}

	.project-settings-actions {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-top: 16px;
	}
	.save-msg {
		font-size: 13px;
		color: var(--text-secondary);
	}
</style>
