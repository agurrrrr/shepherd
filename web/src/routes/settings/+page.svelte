<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPatch, apiPost } from '$lib/api.js';

	import CommonSettings from '$lib/components/Settings/CommonSettings.svelte';
	import ShepherdSettings from '$lib/components/Settings/ShepherdSettings.svelte';
	import ClaudeSettings from '$lib/components/Settings/ClaudeSettings.svelte';
	import OpenCodeSettings from '$lib/components/Settings/OpenCodeSettings.svelte';
	import PiSettings from '$lib/components/Settings/PiSettings.svelte';
	import GrokSettings from '$lib/components/Settings/GrokSettings.svelte';
	import EmbeddedSettings from '$lib/components/Settings/EmbeddedSettings.svelte';
	import MagiSettings from '$lib/components/Settings/MagiSettings.svelte';
	import PreviewSection from '$lib/components/Settings/PreviewSection.svelte';
	import MCPSection from '$lib/components/Settings/MCPSection.svelte';
	import SkillSyncSection from '$lib/components/Settings/SkillSyncSection.svelte';
	import DataManagement from '$lib/components/Settings/DataManagement.svelte';
	import AccountSettings from '$lib/components/Settings/AccountSettings.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import Icon from '$lib/components/Icon.svelte';

	// NOTE: 이 컴포넌트는 아래 $derived 사용으로 Svelte 5 runes 모드다.
	// runes 모드에서는 평범한 let 변수가 반응형이 아니므로 반드시 $state로 선언해야 한다.
	// (이게 빠지면 loaded=true로 바꿔도 재렌더되지 않아 "Loading..."이 영원히 남는다)
	let configData = $state({});
	let loaded = $state(false);
	let saving = $state(false);
	let saveMsg = $state('');
	let restarting = $state(false);
	let loadError = $state('');

	let mcpStatus = $state({ claude: null, opencode: null, pi: null });
	let mcpLoaded = $state(false);

	let projectsList = $state([]);
	let modelOptions = $state({ claude: [], opencode: [], pi: [], grok: [] });
	let grokModelInfo = $state(null);

	// The manager sheep's saved provider — ShepherdSettings reports it so the
	// 목자 tab's prompt preview renders the provider a shepherd task actually
	// runs on (#595).
	let shepherdProvider = $state('claude');

	// Provider enabled flags (shared mutable state)
	let providerEnabled = $state({ claude: true, opencode: true, pi: true, grok: true, embedded: true, magi: true });

	// Tab state — the shepherd tab comes first: setting the workspace is the first
	// thing a new daemon needs, and its own Save button posts to a different
	// endpoint than the shared form below (#593).
	let activeTab = $state('shepherd');
	const settingsTabs = [
		{ value: 'shepherd', label: '목자', hint: 'Workspace, orchestrator' },
		{ value: 'common', label: '공통', hint: 'Daemon, prompts, wiki, alerts' },
		{ value: 'claude', label: 'Claude', hint: 'Claude Code CLI' },
		{ value: 'opencode', label: 'OpenCode', hint: 'OpenCode CLI' },
		{ value: 'pi', label: 'Pi', hint: 'Pi coding agent' },
		{ value: 'grok', label: 'Grok', hint: 'Grok CLI' },
		{ value: 'embedded', label: 'Embedded', hint: 'Local & hosted endpoints' },
		{ value: 'magi', label: 'MAGI', hint: 'Three-model deliberation' }
	];

	// Only provider tabs get the On/Off badge — the 목자 and 공통 tabs are always on.
	const providerTabs = ['claude', 'opencode', 'pi', 'grok', 'embedded', 'magi'];

	// 모든 탭을 항상 노출한다. 사용유무 토글이 각 provider 탭 안으로 이동했으므로,
	// 탭을 숨기면 한 번 끈 provider를 다시 켤 방법이 사라진다. 상태는 ON/OFF 뱃지로 구분.
	let visibleTabs = $derived(settingsTabs);

	// Embedded endpoint sync
	async function reloadEmbeddedEndpoints() {
		// The EmbeddedSettings component manages its own state,
		// this is a hook for future cross-component sync if needed.
	}

	// MCP status update listener
	function handleMcpStatusChanged(e) {
		if (e.detail) mcpStatus = e.detail;
	}

	// Load all settings data with error handling (runs after mount)
	onMount(async () => {
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
				if (configData.webpush_enabled == null) configData.webpush_enabled = true;
				if (configData.webpush_notify_on_complete == null) configData.webpush_notify_on_complete = true;
				if (configData.webpush_notify_on_fail == null) configData.webpush_notify_on_fail = true;
				providerEnabled = {
					claude: configRes.data.provider_enabled_claude !== false,
					opencode: configRes.data.provider_enabled_opencode !== false,
					pi: configRes.data.provider_enabled_pi !== false,
					grok: configRes.data.provider_enabled_grok !== false,
					embedded: configRes.data.provider_enabled_embedded !== false,
					magi: configRes.data.provider_enabled_magi !== false
				};
			}
			if (mcpRes?.data) mcpStatus = mcpRes.data;
			if (modelRes?.data) {
				modelOptions = modelRes.data;
				grokModelInfo = modelRes.data.grok_info || null;
			}
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
			provider_enabled_grok: providerEnabled.grok,
			provider_enabled_embedded: providerEnabled.embedded,
			provider_enabled_magi: providerEnabled.magi,
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
			custom_prompt_grok: configData.custom_prompt_grok || '',
			opencode_compact_prompt: configData.opencode_compact_prompt,
			opencode_thinking_default: configData.opencode_thinking_default,
			opencode_thinking_proxy_enabled: configData.opencode_thinking_proxy_enabled,
			opencode_thinking_proxy_port: parseInt(configData.opencode_thinking_proxy_port) || 8686,
			opencode_thinking_proxy_target: configData.opencode_thinking_proxy_target || '',
			opencode_thinking_model: configData.opencode_thinking_model || '',
			model_claude: configData.model_claude || '',
			model_opencode: configData.model_opencode || '',
			model_pi: configData.model_pi || '',
			model_grok: configData.model_grok || '',
			task_timeout: (configData.task_timeout || '').trim() || '4h',
			wiki_enabled: configData.wiki_enabled,
			wiki_auto_ingest: configData.wiki_auto_ingest,
			task_checkpoint_enabled: configData.task_checkpoint_enabled,
			task_checkpoint_retention_days: Number.isNaN(parseInt(configData.task_checkpoint_retention_days)) ? 30 : parseInt(configData.task_checkpoint_retention_days),
			wiki_max_context_pages: parseInt(configData.wiki_max_context_pages) || 2,
			wiki_max_page_content_chars: parseInt(configData.wiki_max_page_content_chars) || 2000,
			discord_notifications_enabled: configData.discord_notifications_enabled,
			discord_webhook_url: configData.discord_webhook_url || '',
			discord_notify_on_complete: configData.discord_notify_on_complete,
			discord_notify_on_fail: configData.discord_notify_on_fail,
			webpush_enabled: configData.webpush_enabled !== false,
			webpush_notify_on_complete: configData.webpush_notify_on_complete !== false,
			webpush_notify_on_fail: configData.webpush_notify_on_fail !== false,
			embedded_active_id: configData.embedded_active_id || '',
			custom_prompt_embedded: configData.custom_prompt_embedded || '',
			shell: configData.shell || ''
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
	<PageHeader
		eyebrow="Configure · Daemon"
		title="Settings"
		subtitle="Providers, prompts, notifications and housekeeping for this Shepherd daemon. Changes apply after Save; some need a restart."
	/>

	{#if loadError}
		<div class="error-banner" role="alert"><Icon name="alert-triangle" size={15} /> {loadError}</div>
	{/if}

	{#if !loaded}
		<div class="settings-layout" aria-busy="true">
			<div class="settings-nav">
				{#each Array(settingsTabs.length) as _}<div class="skeleton" style="height: 38px; margin-bottom: 4px"></div>{/each}
			</div>
			<div class="card"><div class="skeleton" style="height: 360px"></div></div>
		</div>
	{:else}
		<div class="settings-layout">
			<!-- Section navigation -->
			<nav class="settings-nav" aria-label="Settings sections">
				<span class="nav-caption">General</span>
				{#each visibleTabs as tab, i}
					{#if i > 0 && providerTabs.includes(tab.value) && !providerTabs.includes(visibleTabs[i - 1]?.value)}
						<span class="nav-caption">Providers</span>
					{/if}
					<button
						class="settings-tab"
						class:active={activeTab === tab.value}
						aria-current={activeTab === tab.value ? 'page' : undefined}
						onclick={() => activeTab = tab.value}
					>
						<span class="tab-text">
							<span class="tab-label">{tab.label}</span>
							<span class="tab-hint">{tab.hint}</span>
						</span>
						{#if providerTabs.includes(tab.value)}
							<span
								class="tab-state"
								class:on={providerEnabled[tab.value]}
								title={providerEnabled[tab.value] ? 'Enabled' : 'Disabled'}
							>{providerEnabled[tab.value] ? 'On' : 'Off'}</span>
						{/if}
					</button>
				{/each}
			</nav>

			<div class="settings-main">
				{#key activeTab}
					<div class="settings-form card">
						<header class="form-head">
							<h2>{settingsTabs.find((t) => t.value === activeTab)?.label}</h2>
							<span class="form-hint">{settingsTabs.find((t) => t.value === activeTab)?.hint}</span>
						</header>

						{#if activeTab === 'shepherd'}
							<ShepherdSettings {configData} {providerEnabled} {modelOptions} onProvider={(p) => (shepherdProvider = p)} />
						{/if}
						{#if activeTab === 'common'}
							<CommonSettings {configData} />
						{/if}
						{#if activeTab === 'claude'}
							<ClaudeSettings {configData} {providerEnabled} modelOptions={modelOptions.claude} />
						{/if}
						{#if activeTab === 'opencode'}
							<OpenCodeSettings {configData} {providerEnabled} modelOptions={modelOptions.opencode} />
						{/if}
						{#if activeTab === 'pi'}
							<PiSettings {configData} {providerEnabled} modelOptions={modelOptions.pi} />
						{/if}
						{#if activeTab === 'grok'}
							<GrokSettings {configData} {providerEnabled} modelOptions={modelOptions.grok} {grokModelInfo} />
						{/if}
						{#if activeTab === 'embedded'}
							<EmbeddedSettings {configData} {providerEnabled} reloadEndpoints={reloadEmbeddedEndpoints} />
						{/if}
						{#if activeTab === 'magi'}
							<MagiSettings {configData} {providerEnabled} {modelOptions} />
						{/if}

						<!-- Save/Restart actions — the 목자 tab saves through its own
						     button (PATCH /api/shepherd/config), so the shared form's
						     buttons stay hidden there. -->
						{#if activeTab !== 'shepherd'}
						<div class="setting-actions">
							{#if saveMsg}
								<span class="save-msg" class:error={saveMsg.startsWith('Error')} role="status">
									<Icon name={saveMsg.startsWith('Error') ? 'alert-triangle' : 'check'} size={14} />
									{saveMsg}
								</span>
							{/if}
							<button class="btn btn-ghost" onclick={restart} disabled={restarting}>
								<Icon name="refresh-cw" size={13} />
								{restarting ? 'Restarting…' : 'Restart server'}
							</button>
							<button class="btn btn-primary" onclick={save} disabled={saving}>
								{saving ? 'Saving…' : 'Save settings'}
							</button>
						</div>
						{/if}
					</div>
				{/key}

				<!-- System Prompt Preview — provider 탭별로 표시 -->
				{#if ['claude', 'opencode', 'pi', 'grok'].includes(activeTab)}
					<div class="extra">
						<PreviewSection provider={activeTab} />
					</div>
				{/if}

				<!-- 목자 탭: 현재 목자 프로바이더 기준으로 목자 프롬프트 미리보기 (#595).
				     sheep=shepherd를 붙이면 서버가 운영 지침·워크스페이스·관리 범위 표를
				     포함한 목자 컨텍스트를 렌더링한다. -->
				{#if activeTab === 'shepherd'}
					<div class="extra">
						<PreviewSection provider={shepherdProvider} sheep="shepherd" title="목자" />
					</div>
				{/if}

				<!-- 공통 관리 섹션 — 공통 탭에만 표시 -->
				{#if activeTab === 'common'}
					<div class="extra"><AccountSettings /></div>
					<div class="extra"><MCPSection {mcpStatus} {mcpLoaded} /></div>
					<div class="extra"><SkillSyncSection /></div>
					<div class="extra"><DataManagement {projectsList} /></div>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.page { max-width: var(--content-max); }

	.settings-layout {
		display: grid;
		grid-template-columns: 220px minmax(0, 820px);
		gap: 28px;
		align-items: start;
	}

	/* — Section nav — */
	.settings-nav {
		position: sticky;
		top: 24px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.nav-caption {
		padding: 14px 12px 6px;
		font-family: var(--font-mono);
		font-size: 10px;
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.nav-caption:first-child {
		padding-top: 0;
	}

	.settings-tab {
		position: relative;
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
		width: 100%;
		padding: 8px 12px;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		color: var(--text-secondary);
		text-align: left;
		cursor: pointer;
		transition: background var(--dur-1) ease, color var(--dur-1) ease;
	}

	.settings-tab:hover {
		background: var(--bg-hover);
		color: var(--text-primary);
	}

	.settings-tab.active {
		background:
			linear-gradient(90deg, rgba(244, 184, 96, 0.1), rgba(244, 184, 96, 0.02)),
			rgba(237, 230, 218, 0.03);
		box-shadow: inset 0 0 0 1px rgba(244, 184, 96, 0.12);
		color: var(--text-primary);
	}

	.settings-tab.active::before {
		content: '';
		position: absolute;
		left: -10px;
		top: 10px;
		bottom: 10px;
		width: 2px;
		border-radius: 2px;
		background: var(--accent);
		box-shadow: 0 0 10px var(--accent-glow);
	}

	.tab-text {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}

	.tab-label {
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
	}

	.tab-hint {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.tab-state {
		flex-shrink: 0;
		display: inline-flex;
		align-items: center;
		gap: 5px;
		font-family: var(--font-mono);
		font-size: 10px;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.tab-state::before {
		content: '';
		width: 6px;
		height: 6px;
		border-radius: 50%;
		box-shadow: inset 0 0 0 1.5px var(--text-tertiary);
	}

	.tab-state.on {
		color: var(--success);
	}

	.tab-state.on::before {
		background: var(--success);
		box-shadow: 0 0 8px rgba(159, 214, 133, 0.5);
	}

	/* — Form — */
	.settings-main {
		min-width: 0;
	}

	.settings-form {
		display: flex;
		flex-direction: column;
		gap: 16px;
		padding: 24px 26px 0;
		animation: rise-in var(--dur-3) var(--ease-out) backwards;
	}

	.form-head {
		display: flex;
		align-items: baseline;
		gap: 14px;
		padding-bottom: 16px;
		margin-bottom: 4px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.form-head h2 {
		font-family: var(--font-display);
		font-size: 32px;
		font-weight: 400;
		letter-spacing: -0.015em;
	}

	.form-hint {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
	}

	/* Action bar sticks to the bottom of the viewport while the form scrolls */
	.setting-actions {
		position: sticky;
		bottom: 0;
		z-index: 2;
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 8px;
		margin: 8px -26px 0;
		padding: 14px 26px;
		border-top: 1px solid var(--border);
		border-radius: 0 0 var(--radius-md) var(--radius-md);
		background: rgba(20, 20, 22, 0.88);
		backdrop-filter: blur(12px);
		-webkit-backdrop-filter: blur(12px);
	}

	.save-msg {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-right: auto;
		font-size: var(--fs-sm);
		color: var(--success);
		animation: rise-in var(--dur-2) var(--ease-out) backwards;
	}

	.save-msg.error {
		color: var(--danger);
	}

	.extra {
		margin-top: 20px;
	}

	.error-banner {
		display: flex;
		align-items: center;
		gap: 8px;
		background: var(--danger-soft);
		box-shadow: inset 0 0 0 1px var(--danger-line);
		color: var(--danger);
		padding: 10px 16px;
		border-radius: var(--radius);
		margin-bottom: 16px;
		font-size: var(--fs-sm);
	}

	@media (max-width: 960px) {
		.settings-layout {
			grid-template-columns: minmax(0, 1fr);
			gap: 16px;
		}

		.settings-nav {
			position: static;
			flex-direction: row;
			overflow-x: auto;
			scrollbar-width: none;
			gap: 4px;
			padding-bottom: 2px;
		}

		.settings-nav::-webkit-scrollbar {
			display: none;
		}

		.nav-caption,
		.tab-hint {
			display: none;
		}

		.settings-tab {
			width: auto;
			flex-shrink: 0;
			white-space: nowrap;
		}

		.settings-tab.active::before {
			display: none;
		}
	}

	@media (max-width: 768px) {
		.settings-form {
			padding: 18px 16px 0;
		}

		.setting-actions {
			margin: 8px -16px 0;
			padding: 12px 16px;
		}
	}
</style>
