<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPatch, apiPost } from '$lib/api.js';

	let configData = {};
	let loaded = false;
	let saving = false;
	let saveMsg = '';
	let restarting = false;

	let mcpStatus = { claude: null, opencode: null };
	let mcpLoaded = false;
	let mcpRegistering = {};
	let mcpError = '';

	let syncing = false;
	let syncResult = '';

	let preview = null;
	let previewLoading = false;
	let previewError = '';
	let previewMode = 'streaming'; // streaming | compact | withGuide
	let previewOpen = false;

	let modelOptions = { claude: [], opencode: [] };

	onMount(async () => {
		const [configRes, mcpRes, modelRes] = await Promise.all([
			apiGet('/api/config'),
			apiGet('/api/mcp/status'),
			apiGet('/api/config/model-options')
		]);
		if (configRes?.data) configData = configRes.data;
		if (mcpRes?.data) mcpStatus = mcpRes.data;
		if (modelRes?.data) modelOptions = modelRes.data;
		mcpLoaded = true;
		loaded = true;
	});

	// If the configured model is not present in the select's options
	// (e.g. user edited config.json manually), append it so it stays visible.
	function optionsWithCurrent(opts, current) {
		if (!current) return opts;
		if (opts.some((o) => o.id === current)) return opts;
		return [...opts, { id: current, label: current + ' (not in config)' }];
	}

	async function loadPreview() {
		previewLoading = true;
		previewError = '';
		const res = await apiGet('/api/config/system-prompt-preview');
		if (res?.data) {
			preview = res.data;
		} else {
			previewError = res?.message || 'Failed to load preview';
		}
		previewLoading = false;
	}

	async function togglePreview() {
		previewOpen = !previewOpen;
		if (previewOpen && !preview) {
			await loadPreview();
		}
	}

	async function registerMCP(provider) {
		mcpRegistering[provider] = true;
		mcpError = '';
		const res = await apiPost('/api/mcp/register', { provider });
		if (res?.success) {
			const statusRes = await apiGet('/api/mcp/status');
			if (statusRes?.data) mcpStatus = statusRes.data;
		} else {
			mcpError = res?.message || 'Registration failed';
		}
		mcpRegistering[provider] = false;
	}

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
			include_mcp_guide: configData.include_mcp_guide,
			enable_file_browser: configData.enable_file_browser,
			custom_prompt_claude: configData.custom_prompt_claude || '',
			custom_prompt_opencode: configData.custom_prompt_opencode || '',
			model_claude: configData.model_claude || '',
			model_opencode: configData.model_opencode || ''
		});
		if (res?.success) {
			saveMsg = 'Saved';
			if (previewOpen) await loadPreview();
		} else {
			saveMsg = 'Error: ' + (res?.message || 'Failed to save');
		}
		saving = false;
		setTimeout(() => saveMsg = '', 3000);
	}

	async function syncSkills() {
		syncing = true;
		syncResult = '';
		const res = await apiPost('/api/skills/sync-all');
		if (res?.success) {
			const d = res.data;
			syncResult = `Synced ${d.synced} skills to ${d.projects} projects` + (d.errors > 0 ? ` (${d.errors} errors)` : '');
		} else {
			syncResult = 'Error: ' + (res?.message || 'Sync failed');
		}
		syncing = false;
		setTimeout(() => syncResult = '', 5000);
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
				<label>Claude Model</label>
				<select class="input" bind:value={configData.model_claude}>
					{#each optionsWithCurrent(modelOptions.claude, configData.model_claude) as opt}
						<option value={opt.id}>{opt.label}</option>
					{/each}
				</select>
			</div>

			<div class="setting-row">
				<label>OpenCode Model</label>
				<select class="input" bind:value={configData.model_opencode}>
					{#each optionsWithCurrent(modelOptions.opencode, configData.model_opencode) as opt}
						<option value={opt.id}>{opt.label}</option>
					{/each}
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

			<div class="setting-row">
				<label>File Browser</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.enable_file_browser} />
					<span>{configData.enable_file_browser ? 'Enabled' : 'Disabled'}</span>
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

			<div class="setting-row column">
				<label>Custom Prompt — Claude</label>
				<textarea
					class="input textarea"
					bind:value={configData.custom_prompt_claude}
					rows="6"
					placeholder="Claude Code 실행 시 추가로 전달할 지시문을 입력하세요."
				></textarea>
				<span class="hint">Injected only when the task runs on Claude Code.</span>
			</div>

			<div class="setting-row column">
				<label>Custom Prompt — OpenCode</label>
				<textarea
					class="input textarea"
					bind:value={configData.custom_prompt_opencode}
					rows="6"
					placeholder="OpenCode 실행 시 추가로 전달할 지시문을 입력하세요."
				></textarea>
				<span class="hint">Injected only when the task runs on OpenCode.</span>
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

		<div class="preview-section card">
			<div class="preview-header">
				<h2 class="section-title">System Prompt Preview</h2>
				<button class="btn btn-sm btn-outline" onclick={togglePreview}>
					{previewOpen ? 'Hide' : 'Show'}
				</button>
			</div>
			<p class="preview-desc">
				Task 실행 시 Claude/OpenCode에 실제로 전달되는 시스템 프롬프트입니다. MCP 도구 리스트, 최근 작업 히스토리, 프로젝트 스킬 요약, 그리고 Custom Prompt가 포함됩니다. (Sheep별 히스토리·스킬은 Sheep 컨텍스트에서만 채워집니다.)
			</p>
			{#if previewOpen}
				{#if previewLoading}
					<p class="text-muted">Loading...</p>
				{:else if previewError}
					<p class="preview-error">{previewError}</p>
				{:else if preview}
					<div class="preview-tabs">
						<button class="preview-tab" class:active={previewMode === 'streaming'} onclick={() => previewMode = 'streaming'}>Streaming (Claude --append-system-prompt)</button>
						<button class="preview-tab" class:active={previewMode === 'withGuide'} onclick={() => previewMode = 'withGuide'}>With Guide (Interactive)</button>
						<button class="preview-tab" class:active={previewMode === 'compact'} onclick={() => previewMode = 'compact'}>Compact (OpenCode)</button>
					</div>
					<pre class="preview-body">{preview[previewMode] || '(empty)'}</pre>
					<button class="btn btn-sm btn-outline" onclick={loadPreview}>Refresh</button>
				{/if}
			{/if}
		</div>

		<div class="mcp-section card">
			<h2 class="section-title">MCP Registration</h2>
			{#if !mcpLoaded}
				<p class="text-muted">Loading...</p>
			{:else}
				<div class="mcp-providers">
					{#each [
						{ key: 'claude', label: 'Claude Code' },
						{ key: 'opencode', label: 'OpenCode' }
					] as provider}
						{@const status = mcpStatus[provider.key]}
						<div class="mcp-provider">
							<div class="mcp-provider-info">
								<span class="mcp-provider-name">{provider.label}</span>
								{#if status?.registered}
									<span class="badge badge-success">Registered</span>
								{:else}
									<span class="badge badge-muted">Not Registered</span>
								{/if}
							</div>
							<div class="mcp-provider-path">{status?.config_path || ''}</div>
							{#if status?.error}
								<div class="mcp-provider-error">{status.error}</div>
							{/if}
							{#if !status?.registered}
								<button
									class="btn btn-sm"
									onclick={() => registerMCP(provider.key)}
									disabled={mcpRegistering[provider.key]}
								>
									{mcpRegistering[provider.key] ? 'Registering...' : 'Register'}
								</button>
							{/if}
						</div>
					{/each}
				</div>
				{#if mcpError}
					<div class="mcp-error">{mcpError}</div>
				{/if}
			{/if}
		</div>
		<div class="sync-section card">
			<h2 class="section-title">Skill Sync</h2>
			<p class="sync-desc">Sync all enabled skills to each project's <code>.claude/skills/</code> directory so Claude Code and OpenCode can use them natively with frontmatter (effort, maxTurns, etc).</p>
			<div class="sync-actions">
				<button class="btn btn-sm" onclick={syncSkills} disabled={syncing}>
					{syncing ? 'Syncing...' : 'Sync All Skills'}
				</button>
				{#if syncResult}
					<span class="sync-msg" class:error={syncResult.startsWith('Error')}>{syncResult}</span>
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

	.setting-row.column {
		flex-direction: column;
		align-items: stretch;
		gap: 8px;
	}

	.setting-row.column label {
		min-width: 0;
	}

	.setting-row.column .input {
		max-width: none;
	}

	.textarea {
		width: 100%;
		resize: vertical;
		font-family: inherit;
		line-height: 1.5;
		padding: 8px 10px;
	}

	.hint {
		font-size: 12px;
		color: var(--text-secondary);
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

	.preview-section {
		max-width: 900px;
		margin-top: 24px;
	}
	.preview-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 8px;
	}
	.preview-desc {
		font-size: 13px;
		color: var(--text-secondary);
		line-height: 1.5;
		margin-bottom: 12px;
	}
	.preview-tabs {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
		margin-bottom: 8px;
		border-bottom: 1px solid var(--border);
	}
	.preview-tab {
		padding: 6px 12px;
		font-size: 12px;
		font-weight: 500;
		background: transparent;
		color: var(--text-secondary);
		border: none;
		border-bottom: 2px solid transparent;
		cursor: pointer;
		transition: color 0.15s, border-color 0.15s;
	}
	.preview-tab:hover {
		color: var(--text-primary);
	}
	.preview-tab.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
	}
	.preview-body {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: 12px;
		font-family: monospace;
		font-size: 12px;
		line-height: 1.5;
		max-height: 480px;
		overflow: auto;
		white-space: pre-wrap;
		word-break: break-word;
		margin-bottom: 8px;
	}
	.preview-error {
		color: var(--danger);
		font-size: 13px;
	}
	.btn-outline {
		background: transparent;
		color: var(--text-primary);
		border: 1px solid var(--border);
	}
	.btn-outline:hover {
		border-color: var(--accent);
		color: var(--accent);
	}

	.mcp-section {
		max-width: 500px;
		margin-top: 24px;
	}

	.section-title {
		font-size: 16px;
		font-weight: 600;
		margin-bottom: 16px;
	}

	.mcp-providers {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.mcp-provider {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 12px;
		background: var(--bg-secondary);
		border-radius: 8px;
		border: 1px solid var(--border);
	}

	.mcp-provider-info {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.mcp-provider-name {
		font-size: 14px;
		font-weight: 600;
	}

	.mcp-provider-path {
		font-size: 12px;
		color: var(--text-secondary);
		font-family: monospace;
	}

	.mcp-provider-error {
		font-size: 12px;
		color: var(--danger);
	}

	.mcp-error {
		margin-top: 12px;
		font-size: 13px;
		color: var(--danger);
	}

	.badge {
		font-size: 11px;
		font-weight: 600;
		padding: 2px 8px;
		border-radius: 10px;
	}

	.badge-success {
		background: color-mix(in srgb, var(--success) 15%, transparent);
		color: var(--success);
	}

	.badge-muted {
		background: var(--bg-tertiary);
		color: var(--text-secondary);
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
		align-self: flex-start;
		transition: opacity 0.15s;
	}
	.btn-sm:hover { opacity: 0.85; }
	.btn-sm:disabled { opacity: 0.5; cursor: not-allowed; }

	.sync-section {
		max-width: 500px;
		margin-top: 24px;
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

		.mcp-section {
			max-width: none;
		}
	}
</style>
