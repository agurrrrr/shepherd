<script>
	import ProviderEnableToggle from './ProviderEnableToggle.svelte';

	/** @type {{ model_opencode: string, custom_prompt_opencode: string, opencode_compact_prompt: boolean, opencode_thinking_default: boolean, opencode_thinking_proxy_enabled: boolean, opencode_thinking_proxy_port: number, opencode_thinking_proxy_target: string, opencode_thinking_model: string, concurrency_limits: Record<string, number> }} */
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

<ProviderEnableToggle {providerEnabled} provider="opencode" label="🟢 OpenCode" />

<div class="setting-row">
	<label>OpenCode Model</label>
	<select class="input" bind:value={configData.model_opencode}>
		{#each optionsWithCurrent(modelOptions, configData.model_opencode) as opt}
			<option value={opt.id}>{opt.label}</option>
		{/each}
	</select>
</div>
<div class="setting-row">
	<label>Per-Group Limits</label>
	<div class="conc-limits">
		{#each modelOptions as opt}
			{@const key = opt.id ? `opencode/${opt.id}` : 'opencode'}
			<div class="conc-row">
				<span class="conc-label" title={opt.id ? opt.id : 'OpenCode 모델 미지정 작업의 기본 그룹'}>🟢 {opt.id ? opt.label : 'OpenCode (모델 미지정 / 기본)'}</span>
				<input class="input conc-input" type="number" bind:value={configData.concurrency_limits[key]} min="0" max="50" placeholder="0" />
			</div>
		{/each}
	</div>
	<span class="hint">provider+model 그룹별 동시 실행 제한. 0이면 그 그룹은 제한 없음.</span>
</div>
<div class="setting-row">
	<label>Compact Prompt</label>
	<label class="toggle">
		<input type="checkbox" bind:checked={configData.opencode_compact_prompt} />
		<span>{configData.opencode_compact_prompt ? 'Compact' : 'Full (same as Claude)'}</span>
	</label>
</div>
<div class="setting-row">
	<label>Thinking (default)</label>
	<label class="toggle">
		<input type="checkbox" bind:checked={configData.opencode_thinking_default} />
		<span>{configData.opencode_thinking_default ? 'On' : 'Off'}</span>
	</label>
	<span class="hint">Default reasoning mode. Per-project toggle on the project page overrides this.</span>
</div>

<div class="setting-section">Thinking Proxy</div>
<p class="hint">Injects <code>chat_template_kwargs.enable_thinking</code> into completions before forwarding. Required because opencode strips that field. Restart after toggling.</p>
<div class="setting-row">
	<label>Enabled</label>
	<label class="toggle">
		<input type="checkbox" bind:checked={configData.opencode_thinking_proxy_enabled} />
		<span>{configData.opencode_thinking_proxy_enabled ? 'Enabled' : 'Disabled'}</span>
	</label>
</div>
<div class="setting-row">
	<label>Proxy Port</label>
	<input class="input" type="number" min="1024" max="65535" bind:value={configData.opencode_thinking_proxy_port} />
	<span class="hint">127.0.0.1:&lt;port&gt;. Use this as <code>baseURL</code> in opencode config.</span>
</div>
<div class="setting-row">
	<label>Proxy Target</label>
	<input class="input" type="text" placeholder="http://127.0.0.1:8083/v1" bind:value={configData.opencode_thinking_proxy_target} />
	<span class="hint">Real OpenAI-compatible endpoint (llama-server, etc.).</span>
</div>
<div class="setting-row">
	<label>Thinking Model</label>
	<input class="input" type="text" placeholder="qwen3.6-thinking/qwen3.6-27b" bind:value={configData.opencode_thinking_model} />
	<span class="hint"><code>provider/model</code> id for the Thinking toggle.</span>
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
