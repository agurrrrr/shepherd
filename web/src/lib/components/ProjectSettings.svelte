<script>
	import Icon from './Icon.svelte';
	import { onMount } from 'svelte';
	import { apiGet, apiPut, apiPost, apiPatch } from '$lib/api.js';
	import { t, translate } from '$lib/i18n/index.js';

	// sheepName/sheepStatus come from the project page so the rename form can
	// disable saving while the sheep is mid-task (#638 §6: worker refuses renames
	// of working sheep with 409). onSheepRenamed lets the parent refresh its own
	// header/provider state after a successful rename.
	let { projectName, sheepName = '', sheepStatus = 'idle', onSheepRenamed = null } = $props();

	// --- 담당 양 (assigned sheep) section ---
	let newName = $state('');
	let renameSaving = $state(false);
	let renameMsg = $state('');
	let renameError = $state('');
	let suggestions = $state([]);
	let suggesting = $state(false);
	let suggestMsg = $state('');

	let canRename = $derived(
		!!sheepName && sheepStatus !== 'working' && newName.trim() !== '' && newName.trim() !== sheepName && !renameSaving
	);

	async function saveRename() {
		if (!canRename) return;
		renameSaving = true;
		renameMsg = '';
		renameError = '';
		const res = await apiPatch(`/api/sheep/${encodeURIComponent(sheepName)}/name`, { name: newName.trim() });
		if (res?.success) {
			const renamed = res.data?.new_name || newName.trim();
			renameMsg = translate('projectSettings.renamed', { from: sheepName, to: renamed });
			newName = '';
			suggestions = [];
			// Parent owns sheepName state and re-fetches the sheep's provider.
			onSheepRenamed?.(renamed);
		} else {
			// 400: invalid name / manager sheep. 409: name taken or sheep working.
			renameError = res?.message || translate('projectSettings.renameFailed');
		}
		renameSaving = false;
		setTimeout(() => { renameMsg = ''; }, 5000);
	}

	async function loadSuggestions() {
		suggesting = true;
		suggestMsg = '';
		suggestions = [];
		const res = await apiPost('/api/sheep-names/suggest', {
			project_name: projectName,
			description: ''
		});
		if (res?.success && res.data?.names) {
			suggestions = res.data.names;
		} else {
			suggestMsg = res?.message || translate('projectSettings.suggestFailed');
		}
		suggesting = false;
	}

	function useSuggestion(name) {
		newName = name;
		renameError = '';
	}

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
	<!-- 담당 양 (#638): rename through PATCH /api/sheep/<name>/name. LLM candidate
	     chips come from POST /api/sheep-names/suggest. Working sheep cannot be
	     renamed (the server answers 409), so Save stays disabled while working. -->
	<div class="project-settings-section">
		<h4><Icon name="sheep" size={15} /> {$t('projectSettings.sheepTitle')}</h4>
		{#if !sheepName}
			<p class="hint">{$t('projectSettings.sheepNone')}</p>
		{:else}
			<p class="hint">{$t('projectSettings.sheepCurrent')} <strong>{sheepName}</strong>{#if sheepStatus === 'working'}{$t('projectSettings.renameBlockedWhileWorking')}{/if}</p>

			<div class="sheep-rename-row">
				<input
					class="input mono"
					type="text"
					bind:value={newName}
					placeholder={$t('projectSettings.newNamePlaceholder')}
					disabled={sheepStatus === 'working' || renameSaving}
					maxlength="32"
				/>
				<button class="btn btn-primary" onclick={saveRename} disabled={!canRename}>
					{renameSaving ? $t('common.saving') : $t('projectSettings.saveName')}
				</button>
				<button class="btn" onclick={loadSuggestions} disabled={suggesting || sheepStatus === 'working'}>
					{suggesting ? $t('projectSettings.suggesting') : $t('projectSettings.getSuggestions')}
				</button>
			</div>

			{#if suggestions.length > 0}
				<div class="sheep-chips">
					{#each suggestions as n}
						<button class="sheep-chip" onclick={() => useSuggestion(n)} disabled={sheepStatus === 'working'}>{n}</button>
					{/each}
				</div>
			{/if}

			{#if suggestMsg}
				<p class="sheep-suggest-msg">{suggestMsg}</p>
			{/if}
			{#if renameError}
				<p class="sheep-error">{renameError}</p>
			{/if}
			{#if renameMsg}
				<p class="save-msg">{renameMsg}</p>
			{/if}
		{/if}
	</div>

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

	/* #638 담당 양 rename form */
	.sheep-rename-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 8px;
		margin-top: 8px;
	}
	.sheep-rename-row .input {
		flex: 1 1 200px;
		max-width: 320px;
		min-width: 0;
	}
	.sheep-chips {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
		margin-top: 10px;
	}
	.sheep-chip {
		font-family: var(--font-mono);
		font-size: var(--fs-xs);
		padding: 3px 9px;
		border: 0;
		background: var(--well);
		box-shadow: var(--px-ring);
		color: var(--text-primary);
		cursor: pointer;
	}
	.sheep-chip:hover:not(:disabled) {
		color: var(--accent);
	}
	.sheep-suggest-msg {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		margin-top: 8px;
	}
	.sheep-error {
		font-size: var(--fs-sm);
		color: var(--danger-text);
		margin-top: 8px;
	}
</style>
