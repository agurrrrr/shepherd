<script>
	import ProviderEnableToggle from './ProviderEnableToggle.svelte';

	/** @type {{ model_grok: string, custom_prompt_grok: string, concurrency_limits: Record<string, number>, grok_path: string }} */
	export let configData;
	/** @type {Array<{id: string, label: string}>} */
	export let modelOptions;
	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, grok: boolean, embedded: boolean }} */
	export let providerEnabled;

	// If the configured model is not present in the select's options, append it.
	function optionsWithCurrent(opts, current) {
		if (!current) return opts;
		if (opts.some((o) => o.id === current)) return opts;
		return [...opts, { id: current, label: current + ' (not in config)' }];
	}
</script>

<ProviderEnableToggle {providerEnabled} provider="grok" label="⚫ Grok" />

<div class="setting-row">
	<label>Grok Binary Path</label>
	<input class="input" type="text" bind:value={configData.grok_path} placeholder="auto-detect" />
	<span class="hint">grok 또는 래퍼 스크립트 경로 (예: <code>~/.local/bin/grok-safe</code>). 비워두면 자동 탐지.</span>
</div>
<div class="setting-row">
	<label>Grok Model</label>
	<select class="input" bind:value={configData.model_grok}>
		{#each optionsWithCurrent(modelOptions, configData.model_grok) as opt}
			<option value={opt.id}>{opt.label}</option>
		{/each}
	</select>
</div>
<div class="setting-row">
	<label>Per-Group Limit</label>
	<div class="conc-limits">
		<div class="conc-row">
			<span class="conc-label">⚫ Grok{configData.model_grok ? ` (${configData.model_grok})` : ''}</span>
			<input class="input conc-input" type="number" bind:value={configData.concurrency_limits['grok']} min="0" max="50" placeholder="0" />
		</div>
	</div>
	<span class="hint">동시 실행 제한. 0이면 제한 없음.</span>
</div>
<div class="setting-row column">
	<label>Custom Prompt — Grok</label>
	<textarea
		class="input textarea"
		bind:value={configData.custom_prompt_grok}
		rows="6"
		placeholder="Grok 실행 시 추가로 전달할 지시문을 입력하세요."
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
		font-size: 14px;
		font-weight: 500;
		flex: 0 0 140px;
	}

	.setting-row .input {
		flex: 1 1 200px;
		max-width: 240px;
		min-width: 0;
	}

	.setting-row:not(.column) > .hint {
		flex: 0 0 calc(100% - 156px);
		margin-left: 156px;
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
		font-size: 13px;
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
