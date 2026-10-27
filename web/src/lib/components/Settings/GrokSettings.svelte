<script>
	import ProviderEnableToggle from './ProviderEnableToggle.svelte';
	import { apiPost } from '$lib/api.js';
	import { translate, t } from '$lib/i18n/index.js';

	/** @type {{ model_grok: string, custom_prompt_grok: string, concurrency_limits: Record<string, number>, grok_path: string, grok_background_wait: string }} */
	export let configData;
	/** @type {Array<{id: string, label: string, isDefault?: boolean}>} */
	export let modelOptions;
	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, grok: boolean, embedded: boolean }} */
	export let providerEnabled;
	/** @type {{ source: string, error: string, fetched_at: string }|null} */
	export let grokModelInfo = null;

	let refreshing = false;
	let refreshMsg = '';
	// Options read straight from the grok CLI by the refresh button. Null means
	// "use the list the page loaded at startup".
	let liveOptions = null;

	// If the configured model is not present in the select's options, append it.
	function optionsWithCurrent(opts, current) {
		if (!current) return opts;
		if (opts.some((o) => o.id === current)) return opts;
		return [...opts, { id: current, label: current + ' (not in config)' }];
	}

	$: currentOptions = liveOptions || modelOptions || [];
	// grokModelInfo.source: "cli" (daemon read `grok models`), "fallback" (CLI
	// failed), "pending" (not read yet).
	$: grokSourceLabel =
		liveOptions
			? $t('grokSettings.sourceLive')
			: grokModelInfo?.source === 'cli'
				? $t('grokSettings.sourceCli')
				: grokModelInfo?.source === 'fallback'
					? $t('grokSettings.sourceFallback', { error: grokModelInfo.error || $t('grokSettings.sourceNone') })
					: $t('grokSettings.sourcePending');
	// `grok models` lists the account default first, so it is also the newest
	// model xAI offers for this account.
	$: latestModel = currentOptions.find((o) => o.id) || null;

	// Re-reads `grok models` on the daemon and replaces the option list in place.
	async function refreshModels() {
		refreshing = true;
		refreshMsg = '';
		try {
			const res = await apiPost('/api/config/grok-models/refresh', {});
			const data = res?.data;
			if (!data) {
				refreshMsg = translate('grokSettings.noResponse');
			} else if (data.source === 'cli' && data.grok?.length) {
				liveOptions = data.grok;
				refreshMsg = translate('grokSettings.refreshed', { count: data.grok.length });
			} else {
				refreshMsg = translate('grokSettings.cliFailed', { error: data.error || translate('grokSettings.sourceNone') });
			}
		} catch (err) {
			refreshMsg = translate('grokSettings.refreshError', { error: err?.message || err });
		} finally {
			refreshing = false;
		}
	}

	function applyLatest() {
		if (!latestModel) return;
		configData.model_grok = latestModel.id;
		refreshMsg = translate('grokSettings.appliedLatest', { model: latestModel.id });
	}
</script>

<ProviderEnableToggle {providerEnabled} provider="grok" label="⚫ Grok" />

<div class="setting-row">
	<label>Grok Binary Path</label>
	<input class="input" type="text" bind:value={configData.grok_path} placeholder="auto-detect" />
	<span class="hint">{$t('grokSettings.binaryHint')}</span>
</div>
<div class="setting-row">
	<label>Grok Background Wait</label>
	<input class="input" type="text" bind:value={configData.grok_background_wait} placeholder={$t('grokSettings.backgroundWaitPlaceholder')} />
	<span class="hint">
		{$t('grokSettings.backgroundWaitHint')}
	</span>
</div>
<div class="setting-row">
	<label>Grok Model</label>
	<select class="input" bind:value={configData.model_grok}>
		{#each optionsWithCurrent(currentOptions, configData.model_grok) as opt}
			<option value={opt.id}>{opt.label}</option>
		{/each}
	</select>
	<button class="btn btn-sm btn-outline" onclick={refreshModels} disabled={refreshing}>
		{refreshing ? $t('grokSettings.reading') : $t('grokSettings.refreshButton')}
	</button>
	<button class="btn btn-sm btn-outline" onclick={applyLatest} disabled={!latestModel}>
		{$t('grokSettings.applyLatest')}{latestModel ? ` (${latestModel.id})` : ''}
	</button>
	<span class="hint">
		{$t('grokSettings.modelHint', { source: grokSourceLabel })}
		{#if refreshMsg}<span class="refresh-msg">{refreshMsg}</span>{/if}
	</span>
</div>
<div class="setting-row">
	<label>Per-Group Limit</label>
	<div class="conc-limits">
		<div class="conc-row">
			<span class="conc-label">⚫ Grok{configData.model_grok ? ` (${configData.model_grok})` : ''}</span>
			<input class="input conc-input" type="number" bind:value={configData.concurrency_limits['grok']} min="0" max="50" placeholder="0" />
		</div>
	</div>
	<span class="hint">{$t('grokSettings.concurrencyHint')}</span>
</div>
<div class="setting-row column">
	<label>Custom Prompt — Grok</label>
	<textarea
		class="input textarea"
		bind:value={configData.custom_prompt_grok}
		rows="6"
		placeholder={$t('grokSettings.promptPlaceholder')}
	></textarea>
	<span class="hint">Injected only when the task runs on Grok.</span>
</div>

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

	.btn-outline {
		background: transparent;
		color: var(--text-primary);
		border: 1px solid var(--border);
	}

	.btn-outline:hover:not(:disabled) {
		border-color: var(--accent);
		color: var(--accent);
	}

	.refresh-msg {
		display: block;
		margin-top: 4px;
		color: var(--text-secondary);
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
