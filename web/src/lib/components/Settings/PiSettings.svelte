<script>
	import ProviderEnableToggle from './ProviderEnableToggle.svelte';

	/** @type {{ model_pi: string, custom_prompt_pi: string, concurrency_limits: Record<string, number> }} */
	export let configData;
	/** @type {Array<{id: string, label: string}>} */
	export let modelOptions;
	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, embedded: boolean }} */
	export let providerEnabled;

	function optionsWithCurrent(opts, current) {
		if (!current) return opts;
		if (opts.some((o) => o.id === current)) return opts;
		return [...opts, { id: current, label: current + ' (not in config)' }];
	}
</script>

<ProviderEnableToggle {providerEnabled} provider="pi" label="🔵 Pi" />

<div class="setting-row">
	<label>Pi Model</label>
	<select class="input" bind:value={configData.model_pi}>
		{#each optionsWithCurrent(modelOptions, configData.model_pi) as opt}
			<option value={opt.id}>{opt.label}</option>
		{/each}
	</select>
</div>
<div class="setting-row">
	<label>Per-Group Limits</label>
	<div class="conc-limits">
		{#each modelOptions as opt}
			{@const key = opt.id ? `pi/${opt.id}` : 'pi'}
			<div class="conc-row">
				<span class="conc-label" title={opt.id ? opt.id : 'Pi 모델 미지정 작업의 기본 그룹'}>🔵 {opt.id ? opt.label : 'Pi (모델 미지정 / 기본)'}</span>
				<input class="input conc-input" type="number" bind:value={configData.concurrency_limits[key]} min="0" max="50" placeholder="0" />
			</div>
		{/each}
	</div>
	<span class="hint">provider+model 그룹별 동시 실행 제한. 0이면 그 그룹은 제한 없음.</span>
</div>
<div class="setting-row column">
	<label>Custom Prompt — Pi</label>
	<textarea
		class="input textarea"
		bind:value={configData.custom_prompt_pi}
		rows="6"
		placeholder="Pi 실행 시 추가로 전달할 지시문을 입력하세요."
	></textarea>
	<span class="hint">Injected only when the task runs on Pi.</span>
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
