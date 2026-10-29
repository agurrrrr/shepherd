<script>
	import ProviderEnableToggle from './ProviderEnableToggle.svelte';
	import { t, translate } from '$lib/i18n/index.js';

	/** @type {{ model_claude: string, custom_prompt_claude: string, concurrency_limits: Record<string, number>, limit_handoff_provider?: string, limit_handoff_model?: string }} */
	export let configData;
	/** @type {Array<{id: string, label: string}>} */
	export let modelOptions;
	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, embedded: boolean }} */
	export let providerEnabled;
	/** Full /api/config/model-options payload. The handoff model select follows the
	    chosen handoff provider, so it needs every provider's list — including the
	    `embedded` key that lives beside claude/opencode/pi/grok (#676). */
	/** @type {{ claude: Array, opencode: Array, pi: Array, grok: Array, embedded: Array }} */
	export let handoffModelOptions = { claude: [], opencode: [], pi: [], grok: [], embedded: [] };

	// If the configured model is not present in the select's options, append it.
	function optionsWithCurrent(opts, current) {
		if (!current) return opts;
		if (opts.some((o) => o.id === current)) return opts;
		return [...opts, { id: current, label: current + ' (not in config)' }];
	}

	// Rate-limit handoff (#676). The empty value turns the feature off; the six
	// names match config.NormalizeHandoffProvider on the daemon side.
	const handoffProviders = ['claude', 'opencode', 'pi', 'grok', 'embedded', 'magi'];
	const handoffProviderNames = { claude: 'Claude', opencode: 'OpenCode', pi: 'Pi', grok: 'Grok', embedded: null, magi: 'MAGI' };

	// A hand-edited config can hold a value outside the list (for example the
	// legacy alias "auto"). Keep such a value selectable with a "(not in config)"
	// suffix so the select never shows blank and a save never rewrites it silently.
	function handoffProviderChoices() {
		const current = configData.limit_handoff_provider;
		if (current && !handoffProviders.includes(current)) return [...handoffProviders, current];
		return handoffProviders;
	}

	function handoffProviderLabel(p) {
		if (p === '') return $t('claudeSettings.handoffNone');
		let label;
		if (p === 'embedded') label = translate('shepherdSettings.provider.embedded');
		else label = handoffProviderNames[p] || p;
		if (!handoffProviders.includes(p)) label = label + ' (not in config)';
		else if (providerEnabled[p] === false) label += translate('shepherdSettings.providerDisabledSuffix');
		return label;
	}

	function handoffModelChoices(provider) {
		return handoffModelOptions?.[provider] || [];
	}

	// Switching the handoff provider resets the model choice: a model id from the
	// previous provider is meaningless for the new one, and MAGI has no model at
	// all. A value loaded from config (not touched by the user) stays selectable
	// through optionsWithCurrent instead.
	function onHandoffProviderChange() {
		configData.limit_handoff_model = '';
	}
</script>

<ProviderEnableToggle {providerEnabled} provider="claude" label="🟠 Claude" />

<div class="setting-row">
	<label>Claude Model</label>
	<select class="input" bind:value={configData.model_claude}>
		{#each optionsWithCurrent(modelOptions, configData.model_claude) as opt}
			<option value={opt.id}>{opt.label}</option>
		{/each}
	</select>
</div>
<div class="setting-row">
	<label>Per-Group Limit</label>
	<div class="conc-limits">
		<div class="conc-row">
			<span class="conc-label">🟠 Claude{configData.model_claude ? ` (${configData.model_claude})` : ''}</span>
			<input class="input conc-input" type="number" bind:value={configData.concurrency_limits['claude']} min="0" max="50" placeholder="0" />
		</div>
	</div>
	<span class="hint">{$t('claudeSettings.concurrencyHint')}</span>
</div>
<div class="setting-row column">
	<label>Custom Prompt — Claude</label>
	<textarea
		class="input textarea"
		bind:value={configData.custom_prompt_claude}
		rows="6"
		placeholder={$t('claudeSettings.promptPlaceholder')}
	></textarea>
	<span class="hint">Injected only when the task runs on Claude Code.</span>
</div>

<hr class="setting-divider" />
<div class="setting-section-title">{$t('claudeSettings.handoffTitle')}</div>
<p class="hint intro">{$t('claudeSettings.handoffHint')}</p>

<div class="setting-row">
	<label>{$t('claudeSettings.handoffProvider')}</label>
	<select class="input" bind:value={configData.limit_handoff_provider} onchange={onHandoffProviderChange}>
		<option value="">{$t('claudeSettings.handoffNone')}</option>
		{#each handoffProviderChoices() as p}
			<option value={p}>{handoffProviderLabel(p)}</option>
		{/each}
	</select>
</div>

{#if configData.limit_handoff_provider && configData.limit_handoff_provider !== 'magi'}
	<div class="setting-row">
		<label>{$t('claudeSettings.handoffModel')}</label>
		<select class="input" bind:value={configData.limit_handoff_model}>
			{#each optionsWithCurrent(handoffModelChoices(configData.limit_handoff_provider), configData.limit_handoff_model) as opt}
				<option value={opt.id}>{opt.label}</option>
			{/each}
		</select>
	</div>
{:else if configData.limit_handoff_provider === 'magi'}
	<div class="setting-row">
		<label>{$t('claudeSettings.handoffModel')}</label>
		<span class="hint magi-note">{$t('claudeSettings.handoffModelMagiHint')}</span>
	</div>
{/if}

<style>
	.setting-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		column-gap: 16px;
		row-gap: 6px;
	}

	.setting-row > label:not(.toggle) {
		font-size: var(--fs-sm);
		font-weight: 500;
		flex: 0 0 180px;
		color: var(--text-primary);
	}

	.setting-row .input {
		flex: 1 1 200px;
		max-width: 340px;
		min-width: 0;
	}

	.setting-row:not(.column) > .hint {
		flex: 0 0 calc(100% - 196px);
		margin-left: 196px;
		min-width: 0;
	}

	.conc-limits {
		flex: 1 1 200px;
		display: flex;
		flex-direction: column;
		gap: 8px;
		max-width: 320px;
		min-width: 0;
	}

	.conc-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}

	.conc-label {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
	}

	.setting-row .conc-input {
		flex: 0 0 90px;
		max-width: 90px;
	}

	.setting-row.column {
		flex-direction: column;
		align-items: stretch;
		gap: 8px;
	}

	.setting-row.column > label:not(.toggle) {
		flex: none;
	}

	/* Column rows stack vertically, so the row's 200px flex-basis would become
	   the field's height; let the field size itself instead. */
	.setting-row.column .input {
		flex: none;
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
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		line-height: 1.55;
	}

	.setting-section-title {
		font-size: var(--fs-base);
		font-weight: 600;
		color: var(--text-primary);
		padding-top: 12px;
		margin-bottom: 4px;
	}

	.setting-divider {
		border: none;
		border-top: 1px solid var(--border);
		margin: 8px 0;
	}

	.hint.intro {
		flex: 1 1 100%;
		margin-bottom: 4px;
	}

	.magi-note {
		flex: 1 1 200px;
		max-width: 340px;
	}

	@media (max-width: 768px) {
		.setting-row {
			flex-direction: column;
			align-items: stretch;
			gap: 6px;
		}
		.setting-row > label:not(.toggle) {
			flex: none;
		}
		.setting-row .input {
			flex: none;
			max-width: none;
		}
		.setting-row:not(.column) > .hint {
			flex: 0 0 100%;
			margin-left: 0;
		}
	}
</style>
