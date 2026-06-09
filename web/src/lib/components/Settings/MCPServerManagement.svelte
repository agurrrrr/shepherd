<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPost, apiPut, apiDelete } from '$lib/api.js';

	let mcpServers = $state([]);
	let loaded = $state(false);
	let editing = $state(null); // null = not editing, object = editing/creating

	async function loadServers() {
		const res = await apiGet('/api/mcp/servers');
		if (res?.data) {
			mcpServers = res.data;
			loaded = true;
		}
	}

	function openEditor(srv = null) {
		if (srv) {
			editing = { ...srv, _existing: true };
		} else {
			editing = {
				id: '',
				name: '',
				label: '',
				description: '',
				transport: 'stdio',
				command: '',
				args: '',
				url: '',
				env: '',
				enabled: true,
			};
		}
	}

	function closeEditor() {
		editing = null;
	}

	async function saveServer() {
		if (!editing) return;
		const isEdit = !!editing._existing;

		// Validate args is valid JSON array if provided
		if (editing.args) {
			try {
				const parsed = JSON.parse(editing.args);
				if (!Array.isArray(parsed)) {
					alert('Args must be a JSON array, e.g. ["-y", "@modelcontextprotocol/server-github"]');
					return;
				}
			} catch {
				alert('Args must be valid JSON array');
				return;
			}
		}

		// Validate env is valid JSON object if provided
		if (editing.env) {
			try {
				const parsed = JSON.parse(editing.env);
				if (Array.isArray(parsed)) {
					alert('Env must be a JSON object, e.g. {"GITHUB_TOKEN": "ghp_xxx"}');
					return;
				}
			} catch {
				alert('Env must be valid JSON object');
				return;
			}
		}

		const body = {
			name: editing.name,
			label: editing.label,
			description: editing.description,
			transport: editing.transport,
			command: editing.command,
			args: editing.args,
			url: editing.url,
			env: editing.env,
			enabled: editing.enabled,
		};

		let res;
		if (isEdit) {
			res = await apiPut(`/api/mcp/servers/${editing.id}`, body);
		} else {
			res = await apiPost('/api/mcp/servers', body);
		}

		if (res?.success || res?.data) {
			closeEditor();
			await loadServers();
		} else {
			alert('Error: ' + (res?.message || 'Failed to save'));
		}
	}

	async function deleteServer(srv) {
		if (!confirm(`Delete MCP server "${srv.name}"?`)) return;
		const res = await apiDelete(`/api/mcp/servers/${srv.id}`);
		if (res?.success || res?.data) {
			await loadServers();
		} else {
			alert('Error: ' + (res?.message || 'Failed to delete'));
		}
	}

	onMount(loadServers);
</script>

<div class="mcp-section">
	<div class="mcp-header">
		<h3>🔌 MCP Servers</h3>
		<button class="btn btn-sm btn-outline" onclick={() => openEditor(null)}>+ Add MCP Server</button>
	</div>

	<p class="hint">
		외부 MCP 서버(GitHub, Puppeteer, Filesystem 등)를 등록합니다. 등록된 서버는 각 프로젝트 설정에서 ON/OFF 할 수 있습니다.
	</p>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if mcpServers.length === 0}
		<p class="text-muted">No MCP servers configured yet. Add one to get started.</p>
	{:else}
		<div class="mcp-server-list">
			{#each mcpServers as srv}
				<div class="mcp-server-card">
					<div class="mcp-server-header">
						<div class="mcp-server-title">
							<strong>{srv.label || srv.name}</strong>
							<code class="mcp-server-id">{srv.name}</code>
							{#if srv.enabled}
								<span class="badge badge-success">ON</span>
							{:else}
								<span class="badge badge-muted">OFF</span>
							{/if}
							<span class="badge badge-transport">{srv.transport}</span>
						</div>
						<div class="mcp-server-actions">
							<button class="btn btn-sm btn-outline" onclick={() => openEditor(srv)}>Edit</button>
							<button class="btn btn-sm btn-outline btn-danger" onclick={() => deleteServer(srv)}>Delete</button>
						</div>
					</div>
					{#if srv.description}
						<p class="mcp-server-desc">{srv.description}</p>
					{/if}
					<div class="mcp-server-details">
						{#if srv.command}
							<div class="mcp-detail"><span class="mcp-label">Command:</span> <code>{srv.command}</code></div>
						{/if}
						{#if srv.args}
							<div class="mcp-detail"><span class="mcp-label">Args:</span> <code>{srv.args}</code></div>
						{/if}
						{#if srv.url}
							<div class="mcp-detail"><span class="mcp-label">URL:</span> <code>{srv.url}</code></div>
						{/if}
						{#if srv.env}
							<div class="mcp-detail"><span class="mcp-label">Env:</span> <code>{srv.env}</code></div>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<!-- MCP Server editor modal -->
{#if editing}
	<div class="modal-overlay" onclick={closeEditor}>
		<div class="modal-card" onclick={(e) => e.stopPropagation()}>
			<h3>{editing._existing ? 'Edit MCP Server' : 'Add MCP Server'}</h3>

			<div class="setting-row">
				<label>Name</label>
				<input class="input" type="text" bind:value={editing.name} placeholder="github" disabled={!!editing._existing} />
			</div>
			<div class="setting-row">
				<label>Label</label>
				<input class="input" type="text" bind:value={editing.label} placeholder="GitHub MCP" />
			</div>
			<div class="setting-row">
				<label>Description</label>
				<input class="input" type="text" bind:value={editing.description} placeholder="GitHub Issues, PRs, Repos" />
			</div>
			<div class="setting-row">
				<label>Transport</label>
				<select class="input" bind:value={editing.transport}>
					<option value="stdio">stdio</option>
					<option value="sse">SSE</option>
					<option value="http">HTTP</option>
				</select>
			</div>

			{#if editing.transport === 'stdio'}
				<div class="setting-row">
					<label>Command</label>
					<input class="input" type="text" bind:value={editing.command} placeholder="npx" />
				</div>
				<div class="setting-row column">
					<label>Args (JSON array)</label>
					<input class="input" type="text" bind:value={editing.args} placeholder={'["-y", "@modelcontextprotocol/server-github"]'} />
					<p class="hint">Must be a valid JSON array</p>
				</div>
			{:else}
				<div class="setting-row">
					<label>URL</label>
					<input class="input" type="text" bind:value={editing.url} placeholder="http://localhost:3001/mcp" />
				</div>
			{/if}

			<div class="setting-row column">
				<label>Environment (JSON object)</label>
				<input class="input" type="text" bind:value={editing.env} placeholder={'{"GITHUB_TOKEN": "ghp_xxx"}'} />
				<p class="hint">Optional. Must be a valid JSON object.</p>
			</div>

			<div class="setting-row">
				<label>Enabled</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={editing.enabled} />
					<span>{editing.enabled ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="mcp-editor-actions">
				<button class="btn btn-primary" onclick={saveServer}>Save</button>
				<button class="btn btn-outline" onclick={closeEditor}>Cancel</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.text-muted { color: var(--text-secondary); }
	.hint {
		font-size: 12px;
		color: var(--text-muted);
		margin: 4px 0 0;
	}

	.mcp-section {
		margin-top: 24px;
		padding-top: 24px;
		border-top: 1px solid var(--border);
	}
	.mcp-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 8px;
	}
	.mcp-header h3 {
		margin: 0;
		font-size: 16px;
	}

	.mcp-server-list {
		display: flex;
		flex-direction: column;
		gap: 12px;
		margin-top: 12px;
	}
	.mcp-server-card {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 8px;
		padding: 12px;
	}
	.mcp-server-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
	}
	.mcp-server-title {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}
	.mcp-server-id {
		font-size: 11px;
		background: var(--bg-input);
		padding: 1px 6px;
		border-radius: 4px;
		color: var(--text-secondary);
	}
	.mcp-server-desc {
		font-size: 12px;
		color: var(--text-secondary);
		margin: 4px 0;
	}
	.mcp-server-details {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
		margin-top: 6px;
	}
	.mcp-detail {
		font-size: 11px;
		color: var(--text-secondary);
	}
	.mcp-detail code {
		background: var(--bg-input);
		padding: 1px 4px;
		border-radius: 3px;
		font-size: 10px;
	}
	.mcp-label {
		color: var(--text-muted);
		margin-right: 2px;
	}
	.mcp-server-actions {
		display: flex;
		gap: 4px;
	}

	.badge {
		font-size: 10px;
		font-weight: 600;
		padding: 1px 6px;
		border-radius: 8px;
	}
	.badge-success {
		background: color-mix(in srgb, var(--success) 15%, transparent);
		color: var(--success);
	}
	.badge-muted {
		background: var(--bg-tertiary);
		color: var(--text-secondary);
	}
	.badge-transport {
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		text-transform: uppercase;
	}

	.btn-sm {
		padding: 4px 10px;
		font-size: 11px;
		font-weight: 600;
		border: none;
		border-radius: 6px;
		cursor: pointer;
		transition: opacity 0.15s;
	}
	.btn-sm:hover { opacity: 0.85; }
	.btn-sm:disabled { opacity: 0.5; cursor: not-allowed; }

	.btn-outline {
		background: transparent;
		color: var(--text-primary);
		border: 1px solid var(--border);
	}
	.btn-outline:hover {
		border-color: var(--accent);
		color: var(--accent);
	}
	.btn-danger:hover {
		border-color: var(--danger);
		color: var(--danger);
	}

	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 1001;
	}
	.modal-card {
		background: var(--bg-2);
		border: 1px solid var(--border);
		border-radius: 12px;
		padding: 24px;
		width: 90%;
		max-width: 520px;
		max-height: 80vh;
		overflow-y: auto;
		box-shadow: var(--shadow-3);
	}
	.modal-card h3 {
		margin: 0 0 16px;
		font-size: 16px;
	}

	.setting-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 12px;
	}
	.setting-row.column {
		flex-direction: column;
		align-items: stretch;
	}
	.setting-row > label:not(.toggle) {
		flex: 0 0 100px;
		font-size: 13px;
		font-weight: 500;
	}
	.setting-row .input {
		flex: 1;
		min-width: 0;
	}
	.mcp-editor-actions {
		display: flex;
		gap: 8px;
		margin-top: 16px;
	}
</style>
