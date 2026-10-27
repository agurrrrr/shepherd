<script>
	import Icon from './Icon.svelte';
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
			<h4><Icon name="database" size={15} /> MCP Servers</h4>
			<p class="hint">MCP servers are off by default. Toggle a server on to make it available for this project.</p>

			<div class="mcp-toggle-list">
				{#each mcpServers as srv}
					<div class="mcp-toggle-item" class:on={serverEnabled[srv.name]}>
						<div class="mcp-toggle-info">
							<span class="mcp-toggle-name">{srv.label || srv.name}</span>
							<code class="mcp-toggle-id">{srv.name}</code>
							<span class="badge badge-transport">{srv.transport}</span>
							{#if srv.description}
								<span class="mcp-toggle-desc">{srv.description}</span>
							{/if}
						</div>
						<div class="mcp-toggle-control">
							<span class="mcp-toggle-status mono">{serverEnabled[srv.name] ? 'ON' : 'OFF'}</span>
							<input
								type="checkbox"
								class="switch"
								checked={serverEnabled[srv.name]}
								onchange={() => toggleServer(srv.name)}
								aria-label={`${srv.label || srv.name} for this project`}
							/>
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
		font-size: var(--fs-xs);
		color: var(--text-muted);
		margin: 4px 0 12px;
	}

	.project-settings-section {
		margin-bottom: 24px;
	}
	.project-settings-section h4 {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 0 0 4px;
		font-size: var(--fs-md);
		font-weight: var(--fw-semibold);
	}

	.project-settings-section h4 :global(svg) {
		color: var(--accent);
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
		padding: 12px 14px;
		background: var(--bg-secondary);
		border: 0;
		border-radius: 0;
		box-shadow: var(--px-ring), var(--px-bevel);
		gap: 12px;
	}
	.mcp-toggle-item:hover {
		border-color: var(--border-strong);
	}
	.mcp-toggle-item.on {
		border-color: var(--accent-line);
		background: linear-gradient(var(--accent-soft), var(--accent-soft)), var(--bg-secondary);
	}
	.mcp-toggle-info {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
		min-width: 0;
	}
	.mcp-toggle-name {
		font-size: var(--fs-sm);
		font-weight: var(--fw-semibold);
	}
	.mcp-toggle-id {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		background: var(--well);
		border: 1px solid var(--border-subtle);
		padding: 0 7px;
		border-radius: 0;
		color: var(--text-secondary);
	}
	.mcp-toggle-desc {
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}
	.badge-transport {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		letter-spacing: var(--tracking-caps);
		padding: 0 6px;
		border-radius: 0;
		background: var(--well);
		color: var(--text-tertiary);
		text-transform: uppercase;
	}

	.mcp-toggle-control {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-shrink: 0;
	}
	.mcp-toggle-status {
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		letter-spacing: var(--tracking-caps);
		color: var(--text-tertiary);
		min-width: 24px;
		text-align: center;
	}
	.on .mcp-toggle-status {
		color: var(--accent);
	}

	.project-settings-actions {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-top: 16px;
	}
	.save-msg {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
	}
</style>
