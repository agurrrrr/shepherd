<script>
	import { apiGet, apiPatch, apiPost } from '$lib/api.js';

	import CommonSettings from '$lib/components/Settings/CommonSettings.svelte';
	import ClaudeSettings from '$lib/components/Settings/ClaudeSettings.svelte';
	import OpenCodeSettings from '$lib/components/Settings/OpenCodeSettings.svelte';
	import PiSettings from '$lib/components/Settings/PiSettings.svelte';
	import EmbeddedSettings from '$lib/components/Settings/EmbeddedSettings.svelte';
	import PreviewSection from '$lib/components/Settings/PreviewSection.svelte';
	import MCPSection from '$lib/components/Settings/MCPSection.svelte';
	import SkillSyncSection from '$lib/components/Settings/SkillSyncSection.svelte';
	import DataManagement from '$lib/components/Settings/DataManagement.svelte';

	let configData = {};
	let loaded = false;
	let saving = false;
	let saveMsg = '';
	let restarting = false;
	let loadError = '';

	let mcpStatus = { claude: null, opencode: null, pi: null };
	let mcpLoaded = false;

	let projectsList = [];
	let modelOptions = { claude: [], opencode: [], pi: [] };

	// Provider enabled flags (shared mutable state)
	let providerEnabled = { claude: true, opencode: true, pi: true, embedded: true };

	// Tab state
	let activeTab = 'common';
	const settingsTabs = [
		{ value: 'common', label: '공통' },
		{ value: 'claude', label: 'Claude' },
		{ value: 'opencode', label: 'OpenCode' },
		{ value: 'pi', label: 'Pi' },
		{ value: 'embedded', label: 'Embedded' }
	];

	let visibleTabs = $derived(
		settingsTabs.filter(t => t.value === 'common' || providerEnabled[t.value] || activeTab === t.value)
	);

	// Embedded endpoint sync
	async function reloadEmbeddedEndpoints() {
		// The EmbeddedSettings component manages its own state,
		// this is a hook for future cross-component sync if needed.
	}

	// MCP status update listener
	function handleMcpStatusChanged(e) {
		if (e.detail) mcpStatus = e.detail;
	}

	// Load all settings data with error handling (runs after mount in Svelte 5 runes mode)
	$effect(async () => {
		try {
			const [configRes, mcpRes, modelRes, projRes] = await Promise.all([
				apiGet('/api/config'),
				apiGet('/api/mcp/status'),
				apiGet('/api/config/model-options'),
				apiGet('/api/projects')
			]);

			if (configRes?.data) {
				configData = configRes.data;
				if (!configData.concurrency_limits) configData.concurrency_limits = {};
				providerEnabled = {
					claude: configRes.data.provider_enabled_claude !== false,
					opencode: configRes.data.provider_enabled_opencode !== false,
					pi: configRes.data.provider_enabled_pi !== false,
					embedded: configRes.data.provider_enabled_embedded !== false
				};
			}
			if (mcpRes?.data) mcpStatus = mcpRes.data;
			if (modelRes?.data) modelOptions = modelRes.data;
			if (projRes?.data) projectsList = projRes.data;

			mcpLoaded = true;
			loaded = true;
		} catch (err) {
			console.error('Failed to load settings:', err);
			loadError = 'Settings를 불러오는데 실패했습니다: ' + (err?.message || 'Unknown error');
			loaded = true; // Show page even with error so user can see the message
		}

		window.addEventListener('mcp-status-changed', handleMcpStatusChanged);
		return () => {
			window.removeEventListener('mcp-status-changed', handleMcpStatusChanged);
		};
	});

	// Build concurrency limits from per-provider inputs
	function buildConcurrencyLimits() {
		const src = configData.concurrency_limits || {};
		const out = {};
		for (const key of Object.keys(src)) {
			const n = parseInt(src[key]);
			if (Number.isFinite(n) && n > 0) out[key] = n;
		}
		return out;
	}

	async function save() {
		saving = true;
		saveMsg = '';
		const res = await apiPatch('/api/config', {
			language: configData.language,
			default_provider: configData.default_provider,
			provider_enabled_claude: providerEnabled.claude,
			provider_enabled_opencode: providerEnabled.opencode,
			provider_enabled_pi: providerEnabled.pi,
			provider_enabled_embedded: providerEnabled.embedded,
			max_sheep: parseInt(configData.max_sheep) || 12,
			max_concurrent_tasks: parseInt(configData.max_concurrent_tasks) || 0,
			concurrency_limits: buildConcurrencyLimits(),
			auto_approve: configData.auto_approve,
			session_reuse: configData.session_reuse,
			include_task_history: configData.include_task_history,
			include_mcp_guide: configData.include_mcp_guide,
			include_sheep_memory: configData.include_sheep_memory,
			sheep_memory_prompt: configData.sheep_memory_prompt || '',
			enable_file_browser: configData.enable_file_browser,
			custom_prompt_claude: configData.custom_prompt_claude || '',
			custom_prompt_opencode: configData.custom_prompt_opencode || '',
			custom_prompt_pi: configData.custom_prompt_pi || '',
			opencode_compact_prompt: configData.opencode_compact_prompt,
			opencode_thinking_default: configData.opencode_thinking_default,
			opencode_thinking_proxy_enabled: configData.opencode_thinking_proxy_enabled,
			opencode_thinking_proxy_port: parseInt(configData.opencode_thinking_proxy_port) || 8686,
			opencode_thinking_proxy_target: configData.opencode_thinking_proxy_target || '',
			opencode_thinking_model: configData.opencode_thinking_model || '',
			model_claude: configData.model_claude || '',
			model_opencode: configData.model_opencode || '',
			model_pi: configData.model_pi || '',
			task_timeout: (configData.task_timeout || '').trim() || '4h',
			wiki_enabled: configData.wiki_enabled,
			wiki_auto_ingest: configData.wiki_auto_ingest,
			wiki_max_context_pages: parseInt(configData.wiki_max_context_pages) || 2,
			wiki_max_page_content_chars: parseInt(configData.wiki_max_page_content_chars) || 2000,
			discord_notifications_enabled: configData.discord_notifications_enabled,
			discord_webhook_url: configData.discord_webhook_url || '',
			discord_notify_on_complete: configData.discord_notify_on_complete,
			discord_notify_on_fail: configData.discord_notify_on_fail,
			embedded_active_id: configData.embedded_active_id || '',
			custom_prompt_embedded: configData.custom_prompt_embedded || ''
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
		setTimeout(() => {
			window.location.reload();
		}, 3000);
	}
</script>

<div class="page">
	<h1 class="page-title">Settings</h1>

	{#if loadError}
		<div class="error-banner">{loadError}</div>
	{/if}

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else}
		<!-- Tab Navigation -->
		<div class="settings-tabs">
			{#each visibleTabs as tab}
				<button
					class="settings-tab"
					class:active={activeTab === tab.value}
					onclick={() => activeTab = tab.value}
				>
					{tab.label}
					{#if tab.value !== 'common'}
						{#if !providerEnabled[tab.value]}
							<span class="tab-badge tab-badge-off">OFF</span>
						{:else}
							<span class="tab-badge tab-badge-on">ON</span>
						{/if}
					{/if}
				</button>
			{/each}
		</div>

		<div class="settings-form card">
			{#if activeTab === 'common'}
				<CommonSettings {configData} {providerEnabled} {modelOptions} />
			{/if}
			{#if activeTab === 'claude'}
				<ClaudeSettings {configData} modelOptions={modelOptions.claude} />
			{/if}
			{#if activeTab === 'opencode'}
				<OpenCodeSettings {configData} modelOptions={modelOptions.opencode} />
			{/if}
			{#if activeTab === 'pi'}
				<PiSettings {configData} modelOptions={modelOptions.pi} />
			{/if}
			{#if activeTab === 'embedded'}
				<EmbeddedSettings {configData} reloadEndpoints={reloadEmbeddedEndpoints} />
			{/if}

			<!-- Save/Restart actions -->
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

		<!-- Extra sections below the form -->
		<div style="margin-top:24px">
			<PreviewSection />
		</div>
		<div style="margin-top:24px">
			<MCPSection {mcpStatus} {mcpLoaded} />
		</div>
		<div style="margin-top:24px">
			<SkillSyncSection />
		</div>
		<div style="margin-top:24px">
			<DataManagement {projectsList} />
		</div>
	{/if}
</div>

<style>
	.page-title { font-size: 20px; font-weight: 600; margin-bottom: 20px; }
	.text-muted { color: var(--text-secondary); }

	.settings-form {
		max-width: 640px;
		display: flex;
		flex-direction: column;
		gap: 16px;
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

	.error-banner {
		background: color-mix(in srgb, var(--danger) 15%, transparent);
		color: var(--danger);
		padding: 10px 16px;
		border-radius: 8px;
		margin-bottom: 16px;
		font-size: 14px;
	}

	/* — Tab bar — */
	.settings-tabs {
		display: flex;
		gap: 4px;
		margin-bottom: 16px;
		border-bottom: 2px solid var(--border);
		padding-bottom: 0;
	}
	.settings-tab {
		padding: 8px 16px;
		font-size: 14px;
		font-weight: 500;
		background: transparent;
		color: var(--text-secondary);
		border: none;
		border-bottom: 2px solid transparent;
		margin-bottom: -2px;
		cursor: pointer;
		transition: color 0.15s, border-color 0.15s;
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.settings-tab:hover {
		color: var(--text-primary);
	}
	.settings-tab.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
		font-weight: 600;
	}
	.tab-badge {
		font-size: 10px;
		font-weight: 700;
		padding: 1px 6px;
		border-radius: 8px;
		letter-spacing: 0.5px;
	}
	.tab-badge-on {
		background: color-mix(in srgb, var(--success) 20%, transparent);
		color: var(--success);
	}
	.tab-badge-off {
		background: color-mix(in srgb, var(--danger) 20%, transparent);
		color: var(--danger);
	}

	@media (max-width: 768px) {
		.settings-tabs {
			overflow-x: auto;
			-webkit-overflow-scrolling: touch;
			padding-bottom: 4px;
		}
		.settings-tab {
			white-space: nowrap;
			padding: 6px 10px;
			font-size: 13px;
		}
	}
</style>
