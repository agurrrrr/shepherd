<script>
	/** @type {{ model_pi: string, custom_prompt_pi: string }} */
	export let configData;
	/** @type {Array<{id: string, label: string}>} */
	export let modelOptions;

	function optionsWithCurrent(opts, current) {
		if (!current) return opts;
		if (opts.some((o) => o.id === current)) return opts;
		return [...opts, { id: current, label: current + ' (not in config)' }];
	}
</script>

<div class="setting-row">
	<label>Pi Model</label>
	<select class="input" bind:value={configData.model_pi}>
		{#each optionsWithCurrent(modelOptions, configData.model_pi) as opt}
			<option value={opt.id}>{opt.label}</option>
		{/each}
	</select>
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
			max-width: none;
		}
	}
</style>
