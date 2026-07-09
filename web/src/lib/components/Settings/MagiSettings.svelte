<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPut } from '$lib/api.js';

	import ProviderEnableToggle from './ProviderEnableToggle.svelte';

	/** @type {{ embedded_active_id: string, custom_prompt_embedded: string }} */
	export let configData;
	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, embedded: boolean, magi: boolean }} */
	export let providerEnabled;
	/** @type {{ claude: Array, opencode: Array, pi: Array, embedded: Array }} */
	export let modelOptions;

	let embeddedEndpoints = [];
	let loaded = false;

	let magiConfig = defaultMagiConfig();
	let magiErrors = [];
	let magiWarnings = [];
	let magiSaving = false;
	let magiSaveMsg = '';

	const PERSONAS = [
		{ value: 'melchior', label: 'melchior (🔬 과학자)' },
		{ value: 'balthasar', label: 'balthasar (🛡 어머니)' },
		{ value: 'casper', label: 'casper (🎭 여성)' },
		{ value: 'custom', label: 'custom' }
	];

	const PROVIDERS = [
		{ value: 'embedded', label: '🟣 Embedded' },
		{ value: 'claude_cli', label: '🟠 Claude CLI' },
		{ value: 'opencode_cli', label: '🟢 OpenCode CLI' }
	];

	function defaultMagiConfig() {
		return {
			enabled: false,
			proposers: [
				{ provider: 'embedded', endpoint_id: '', persona: 'melchior', display_name: '', custom_prompt: '', model_id: '' },
				{ provider: 'embedded', endpoint_id: '', persona: 'balthasar', display_name: '', custom_prompt: '', model_id: '' },
				{ provider: 'embedded', endpoint_id: '', persona: 'casper', display_name: '', custom_prompt: '', model_id: '' }
			],
			aggregator: { type: 'claude_cli', endpoint_id: '', model_id: '' },
			escalation: { confidence_threshold: 7, max_debate_rounds: 1 },
			proposer_timeout_seconds: 120,
			mode: 'advisory'
		};
	}

	async function loadEndpoints() {
		const res = await apiGet('/api/config/embedded');
		if (res?.data) {
			embeddedEndpoints = res.data.endpoints || [];
			loaded = true;
		}
	}

	async function loadMagiConfig() {
		const res = await apiGet('/api/config/magi');
		if (res?.data) {
			magiConfig = res.data.magi || defaultMagiConfig();
			// Backfill provider defaults for backward compat
			for (const p of magiConfig.proposers) {
				if (!p.provider) p.provider = 'embedded';
				if (!p.model_id) p.model_id = '';
			}
			if (!magiConfig.aggregator.model_id) magiConfig.aggregator.model_id = '';
			magiErrors = res.data.errors || [];
			magiWarnings = res.data.warnings || [];
		} else {
			magiConfig = defaultMagiConfig();
		}
	}

	async function saveMagiConfig() {
		if (!magiConfig) return;
		magiSaving = true;
		magiSaveMsg = '';
		try {
			const res = await apiPut('/api/config/magi', magiConfig);
			if (res?.success) {
				magiSaveMsg = '✅ 저장됨';
				await loadMagiConfig();
			} else if (res?.errors) {
				magiErrors = res.errors;
				magiSaveMsg = '❌ 저장 실패';
			} else {
				magiSaveMsg = '❌ ' + (res?.message || '저장 실패');
			}
		} catch (e) {
			magiSaveMsg = '❌ ' + e.message;
		}
		magiSaving = false;
		setTimeout(() => { magiSaveMsg = ''; }, 5000);
	}

	onMount(() => {
		loadEndpoints();
		loadMagiConfig();
	});
</script>

<ProviderEnableToggle {providerEnabled} provider="magi" label="🧠 MAGI" />

<p class="hint">3개의 서로 다른 모델이 블라인드로 답안을 제시하고, 판정자가 종합합니다. Embedded 엔드포인트뿐 아니라 Claude CLI, OpenCode CLI도 심의자로 사용할 수 있습니다.</p>

<div class="magi-section">
	<div class="magi-header">
		<h3>🧠 MAGI 합의</h3>
		<label class="toggle">
			<input type="checkbox" bind:checked={magiConfig.enabled} />
			<span>{magiConfig?.enabled ? '활성' : '비활성'}</span>
		</label>
	</div>

	{#if magiErrors.length > 0}
		<div class="magi-banner magi-banner-error">
			<strong>⚠️ 설정 오류:</strong>
			<ul>{#each magiErrors as e}<li>{e}</li>{/each}</ul>
		</div>
	{/if}
	{#if magiWarnings.length > 0}
		<div class="magi-banner magi-banner-warning">
			<strong>⚠️ 경고:</strong>
			<ul>{#each magiWarnings as w}<li>{w}</li>{/each}</ul>
		</div>
	{/if}

	{#if magiConfig}
		<!-- Proposers -->
		<div class="magi-subsection">
			<h4>심의자 (Proposers)</h4>
			{#each magiConfig.proposers as proposer, i}
				<div class="magi-proposer">
					<span class="magi-proposer-label">심의자 {i + 1}</span>

					<!-- Provider selector -->
					<select class="input magi-input magi-provider-select" bind:value={proposer.provider}>
						{#each PROVIDERS as p}
							<option value={p.value}>{p.label}</option>
						{/each}
					</select>

					<!-- Endpoint/Model selector based on provider -->
					{#if proposer.provider === 'embedded'}
						<select class="input magi-input" bind:value={proposer.endpoint_id}>
							<option value="">— 엔드포인트 선택 —</option>
							{#each embeddedEndpoints as ep}
								{#if ep.enabled}
									<option value={ep.id}>{ep.label || ep.id} ({ep.model})</option>
								{/if}
							{/each}
						</select>
					{:else if proposer.provider === 'claude_cli'}
						<select class="input magi-input" bind:value={proposer.model_id}>
							{#each modelOptions?.claude || [] as m}
								<option value={m.id}>{m.label}</option>
							{/each}
						</select>
					{:else if proposer.provider === 'opencode_cli'}
						<select class="input magi-input" bind:value={proposer.model_id}>
							{#each modelOptions?.opencode || [] as m}
								<option value={m.id}>{m.label}</option>
							{/each}
						</select>
					{/if}

					<!-- Persona -->
					<select class="input magi-input" bind:value={proposer.persona}>
						{#each PERSONAS as p}
							<option value={p.value}>{p.label}</option>
						{/each}
					</select>

					<!-- Display name -->
					<input
						class="input magi-input magi-display-name"
						type="text"
						bind:value={proposer.display_name}
						placeholder="표시 이름 (선택)"
					/>

					{#if proposer.persona === 'custom'}
						<textarea
							class="input textarea magi-custom-prompt"
							bind:value={proposer.custom_prompt}
							rows="2"
							placeholder="커스텀 페르소나 프롬프트"
						></textarea>
					{/if}
				</div>
			{/each}
		</div>

		<!-- Aggregator -->
		<div class="magi-subsection">
			<h4>판정자 (Aggregator)</h4>
			<div class="magi-aggregator">
				<label class="toggle">
					<input type="radio" name="aggregator-type" value="claude_cli" bind:group={magiConfig.aggregator.type} />
					<span>Claude CLI</span>
				</label>
				<label class="toggle">
					<input type="radio" name="aggregator-type" value="opencode_cli" bind:group={magiConfig.aggregator.type} />
					<span>OpenCode CLI</span>
				</label>
				<label class="toggle">
					<input type="radio" name="aggregator-type" value="endpoint" bind:group={magiConfig.aggregator.type} />
					<span>Endpoint</span>
				</label>
				{#if magiConfig.aggregator.type === 'endpoint'}
					<select class="input magi-input" bind:value={magiConfig.aggregator.endpoint_id}>
						<option value="">— 엔드포인트 선택 —</option>
						{#each embeddedEndpoints as ep}
							{#if ep.enabled}
								<option value={ep.id}>{ep.label || ep.id} ({ep.model})</option>
							{/if}
						{/each}
					</select>
				{:else if magiConfig.aggregator.type === 'claude_cli'}
					<select class="input magi-input" bind:value={magiConfig.aggregator.model_id}>
						<option value="">기본 모델</option>
						{#each modelOptions?.claude || [] as m}
							<option value={m.id}>{m.label}</option>
						{/each}
					</select>
				{:else if magiConfig.aggregator.type === 'opencode_cli'}
					<select class="input magi-input" bind:value={magiConfig.aggregator.model_id}>
						<option value="">기본 모델</option>
						{#each modelOptions?.opencode || [] as m}
							<option value={m.id}>{m.label}</option>
						{/each}
					</select>
				{/if}
			</div>
			<p class="hint">판정자는 Embedded 엔드포인트, Claude CLI 또는 OpenCode CLI를 사용할 수 있습니다.</p>
		</div>

		<!-- Escalation -->
		<div class="magi-subsection">
			<h4>에스컬레이션</h4>
			<div class="setting-row">
				<label>신뢰도 임계값</label>
				<input type="range" min="0" max="10" bind:value={magiConfig.escalation.confidence_threshold} />
				<span class="magi-range-value">{magiConfig.escalation.confidence_threshold}/10</span>
			</div>
			<div class="setting-row">
				<label>토론 라운드</label>
				<select class="input magi-input" bind:value={magiConfig.escalation.max_debate_rounds}>
					<option value={0}>0 (토론 없음)</option>
					<option value={1}>1 (기본)</option>
				</select>
			</div>
			<div class="setting-row">
				<label>제안자 타임아웃 (초)</label>
				<input class="input magi-input" type="number" min="30" max="600" bind:value={magiConfig.proposer_timeout_seconds} />
			</div>
		</div>

		<div class="magi-actions">
			<button class="btn btn-primary" onclick={saveMagiConfig} disabled={magiSaving}>
				{magiSaving ? '저장 중...' : 'MAGI 설정 저장'}
			</button>
			{#if magiSaveMsg}<span class="magi-save-msg">{magiSaveMsg}</span>{/if}
		</div>
	{/if}
</div>

<style>
	h3 { font-size: 14px; font-weight: 600; margin: 0 0 4px; }
	p.hint { font-size: 12px; color: var(--text-secondary); margin: 4px 0 12px; }

	.toggle {
		display: flex;
		align-items: center;
		gap: 8px;
		cursor: pointer;
		font-weight: 400;
		min-width: 0;
	}

	input[type="range"] { accent-color: var(--accent); }

	input.input, select.input, textarea.input {
		font-family: inherit;
		font-size: 13px;
		padding: 6px 10px;
		background: var(--bg-input);
		border: 1px solid var(--border);
		border-radius: 6px;
	color: var(--text-primary);
	outline: none;
	width: 100%;
	box-sizing: border-box;
}

select.input { cursor: pointer; }

.textarea {
	width: 100%;
	resize: vertical;
	font-family: inherit;
	line-height: 1.5;
	padding: 8px 10px;
}

.btn-primary {
	padding: 6px 16px;
	font-size: 13px;
	font-weight: 600;
	background: var(--accent);
	color: white;
	border: none;
	border-radius: 6px;
	cursor: pointer;
}
.btn-primary:hover { opacity: 0.85; }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }

.magi-section {
	margin-top: 8px;
}
.magi-header {
	display: flex;
	align-items: center;
	gap: 12px;
	margin-bottom: 8px;
}
.magi-header h3 {
	margin: 0;
	font-size: 14px;
	font-weight: 600;
}
.magi-subsection {
	margin-top: 16px;
}
.magi-subsection h4 {
	font-size: 13px;
	font-weight: 600;
	margin: 0 0 8px;
}
.magi-proposer {
	display: flex;
	flex-wrap: wrap;
	align-items: center;
	gap: 8px;
	margin-bottom: 8px;
	padding-bottom: 8px;
	border-bottom: 1px solid var(--border);
}
.magi-proposer:last-child {
	border-bottom: none;
}
.magi-proposer-label {
	font-size: 12px;
	font-weight: 600;
	min-width: 60px;
}
.magi-input {
	flex: 1 1 160px;
	max-width: 260px;
	min-width: 0;
}
.magi-provider-select {
	flex: 0 1 140px;
	max-width: 160px;
}
.magi-custom-prompt {
	flex: 1 1 100%;
	max-width: none;
	resize: vertical;
}
.magi-display-name {
	flex: 0 1 140px;
	max-width: 160px;
}
.magi-aggregator {
	display: flex;
	align-items: center;
	gap: 16px;
	flex-wrap: wrap;
	margin-bottom: 4px;
}
.magi-range-value {
	font-size: 12px;
	font-weight: 600;
	min-width: 40px;
}
.magi-banner {
	border-radius: 6px;
	padding: 8px 12px;
	margin: 8px 0;
	font-size: 12px;
}
.magi-banner ul {
	margin: 4px 0 0;
	padding-left: 16px;
}
.magi-banner-error {
	background: color-mix(in srgb, var(--danger, #e53e3e) 12%, transparent);
	border: 1px solid color-mix(in srgb, var(--danger, #e53e3e) 30%, transparent);
}
.magi-banner-warning {
	background: color-mix(in srgb, #f0ad4e 12%, transparent);
	border: 1px solid color-mix(in srgb, #f0ad4e 30%, transparent);
}
.magi-actions {
	display: flex;
	align-items: center;
	gap: 12px;
	margin-top: 16px;
}
.magi-save-msg {
	font-size: 12px;
}

.setting-row {
	display: flex;
	flex-wrap: wrap;
	align-items: center;
	column-gap: 16px;
	row-gap: 6px;
	margin-bottom: 8px;
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
.magi-proposer { flex-direction: column; align-items: stretch; }
.magi-input, .magi-provider-select { max-width: none; }
}
</style>
