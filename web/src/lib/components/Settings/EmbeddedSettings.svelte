<script>
	import { onMount } from 'svelte';
import { apiGet, apiPost, apiPut, apiDelete } from '$lib/api.js';

	import ProviderEnableToggle from './ProviderEnableToggle.svelte';
	import MCPServerManagement from './MCPServerManagement.svelte';

	/** @type {{ embedded_active_id: string, custom_prompt_embedded: string }} */
	export let configData;
	/** @type {() => Promise<void>} */
	export let reloadEndpoints;
	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, embedded: boolean }} */
	export let providerEnabled;

	let embeddedEndpoints = [];
	let loaded = false;
	let editing = null; // null = not editing, object = editing/creating
	let testBusy = {};
	let testResult = {};

	async function loadEndpoints() {
		const res = await apiGet('/api/config/embedded');
		if (res?.data) {
			embeddedEndpoints = res.data.endpoints || [];
			loaded = true;
		}
	}

	function openEditor(ep = null) {
		if (ep) {
			editing = { ...ep, _existing: true };
		} else {
			editing = {
				id: '',
				label: '',
				base_url: 'http://127.0.0.1:8080/v1',
				api_key: '',
				model: '',
				enabled: true,
				thinking: false,
				vision: false,
				max_iterations: 40,
				context_tokens: 32768
			};
		}
	}

	function closeEditor() {
		editing = null;
	}

	async function saveEndpoint() {
		if (!editing) return;
		const isEdit = !!editing._existing;
		const body = {
			id: editing.id,
			label: editing.label,
			base_url: editing.base_url,
			api_key: editing.api_key,
			model: editing.model,
			enabled: editing.enabled,
			thinking: editing.thinking,
			vision: editing.vision,
			max_iterations: parseInt(editing.max_iterations) || 40,
			context_tokens: parseInt(editing.context_tokens) || 32768
		};

		let res;
		if (isEdit) {
			res = await apiPut(`/api/config/embedded/${encodeURIComponent(body.id)}`, body);
		} else {
			res = await apiPost('/api/config/embedded', body);
		}

		if (res?.success || res?.message) {
			closeEditor();
			await loadEndpoints();
			await reloadEndpoints();
		} else {
			alert('Error: ' + (res?.message || 'Failed to save'));
		}
	}

	async function deleteEndpoint(id) {
		if (!confirm('Delete this endpoint?')) return;
		const res = await apiDelete(`/api/config/embedded/${encodeURIComponent(id)}`);
		if (res?.success || res?.message) {
			await loadEndpoints();
			await reloadEndpoints();
		} else {
			alert('Error: ' + (res?.message || 'Failed to delete'));
		}
	}

	async function setActiveEndpoint(id) {
		const res = await apiPost(`/api/config/embedded/${encodeURIComponent(id)}/set-active`, {});
		if (res?.success || res?.message) {
			configData.embedded_active_id = id;
			await loadEndpoints();
		} else {
			alert('Error: ' + (res?.message || 'Failed to set active'));
		}
	}

	async function testEndpoint(ep) {
		testBusy[ep.id] = true;
		testResult[ep.id] = '';
		try {
			const res = await apiPost('/api/config/embedded/test', {
				base_url: ep.base_url,
				api_key: ep.api_key,
				model: ep.model
			});
			if (res?.success) {
				testResult[ep.id] = '✅ Connected';
			} else {
				testResult[ep.id] = '❌ ' + (res?.message || 'Failed');
			}
		} catch (e) {
			testResult[ep.id] = '❌ ' + e.message;
		}
		testBusy[ep.id] = false;
		setTimeout(() => { testResult[ep.id] = ''; }, 5000);
	}

	// Load endpoints on mount
	onMount(loadEndpoints);

	// ── MAGI consensus config ───────────────────────────────────────
	let magiConfig = null;
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

	async function loadMagiConfig() {
		const res = await apiGet('/api/config/magi');
		if (res?.data) {
			magiConfig = res.data.magi || defaultMagiConfig();
			magiErrors = res.data.errors || [];
			magiWarnings = res.data.warnings || [];
		} else {
			magiConfig = defaultMagiConfig();
		}
	}

	function defaultMagiConfig() {
		return {
			enabled: false,
			proposers: [
				{ endpoint_id: '', persona: 'melchior', custom_prompt: '' },
				{ endpoint_id: '', persona: 'balthasar', custom_prompt: '' },
				{ endpoint_id: '', persona: 'casper', custom_prompt: '' }
			],
			aggregator: { type: 'claude_cli', endpoint_id: '' },
			escalation: { confidence_threshold: 7, max_debate_rounds: 1 },
			proposer_timeout_seconds: 120,
			mode: 'advisory'
		};
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

	// Load magi config on mount alongside endpoints
	onMount(loadMagiConfig);
</script>

<ProviderEnableToggle {providerEnabled} provider="embedded" label="🟣 Embedded" />

<p class="hint">OpenAI 호환 API 서버(llama.cpp, vLLM, Ollama 등)를 직접 연결합니다. 서브프로세스 없이 in-process 에이전트 루프가 실행됩니다.</p>

<div class="setting-row">
	<label>Active Endpoint</label>
	<select class="input" bind:value={configData.embedded_active_id}>
		<option value="">— 선택 —</option>
		{#each embeddedEndpoints as ep}
			<option value={ep.id}>{ep.label || ep.id} ({ep.model})</option>
		{/each}
	</select>
</div>

<div class="setting-row column">
	<label>Custom Prompt — Embedded</label>
	<textarea
		class="input textarea"
		bind:value={configData.custom_prompt_embedded}
		rows="4"
		placeholder="임베디드 프로바이더 실행 시 추가로 전달할 지시문을 입력하세요."
	></textarea>
</div>

<div class="embedded-endpoints">
	<div class="embedded-header">
		<h3>Endpoints</h3>
		<button class="btn btn-sm btn-outline" onclick={() => openEditor(null)}>+ Add</button>
	</div>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else if embeddedEndpoints.length === 0}
		<p class="text-muted">No endpoints configured yet.</p>
	{:else}
		{#each embeddedEndpoints as ep}
			<div class="embedded-card" class:active={ep.is_active}>
				<div class="embedded-card-header">
					<strong>{ep.label || ep.id}</strong>
					{#if ep.is_active}<span class="badge badge-success">Active</span>{/if}
					{#if !ep.enabled}<span class="badge badge-muted">Disabled</span>{/if}
					<div class="embedded-actions">
						<button class="btn btn-sm btn-outline" onclick={() => testEndpoint(ep)} disabled={testBusy[ep.id]}>
							{testBusy[ep.id] ? '...' : 'Test'}
						</button>
						<button class="btn btn-sm btn-outline" onclick={() => setActiveEndpoint(ep.id)} disabled={ep.is_active}>Set Active</button>
						<button class="btn btn-sm btn-outline" onclick={() => openEditor(ep)}>Edit</button>
						<button class="btn btn-sm btn-outline" onclick={() => deleteEndpoint(ep.id)} disabled={ep.is_active}>Delete</button>
						<span class="embedded-test-result">{testResult[ep.id] || ''}</span>
					</div>
				</div>
				<div class="embedded-card-body">
					<div class="embedded-detail"><span class="embedded-label">Model:</span> {ep.model}</div>
					<div class="embedded-detail"><span class="embedded-label">URL:</span> <code>{ep.base_url}</code></div>
					<div class="embedded-detail"><span class="embedded-label">Context:</span> {ep.context_tokens?.toLocaleString()} tokens</div>
					<div class="embedded-detail"><span class="embedded-label">Max Iterations:</span> {ep.max_iterations}</div>
					<div class="embedded-detail"><span class="embedded-label">Thinking:</span> {ep.thinking ? 'On' : 'Off'}</div>
					<div class="embedded-detail"><span class="embedded-label">Vision:</span> {ep.vision ? 'On' : 'Off'}</div>
				</div>
			</div>
		{/each}
	{/if}
</div>

<!-- Embedded endpoint editor modal -->
{#if editing}
	<div class="modal-overlay" onclick={closeEditor}>
		<div class="modal-card" onclick={(e) => e.stopPropagation()}>
			<h3>{editing._existing ? 'Edit Endpoint' : 'Add Endpoint'}</h3>
			<div class="setting-row">
				<label>ID</label>
				<input class="input" type="text" bind:value={editing.id} placeholder="local-qwen" disabled={!!editing._existing} />
			</div>
			<div class="setting-row">
				<label>Label</label>
				<input class="input" type="text" bind:value={editing.label} placeholder="Qwen3 27B" />
			</div>
			<div class="setting-row">
				<label>Base URL</label>
				<input class="input" type="text" bind:value={editing.base_url} placeholder="http://127.0.0.1:8080/v1" />
			</div>
			<div class="setting-row">
				<label>API Key</label>
				<input class="input" type="password" bind:value={editing.api_key} placeholder="Leave empty for local servers" />
			</div>
			<div class="setting-row">
				<label>Model</label>
				<input class="input" type="text" bind:value={editing.model} placeholder="qwen3-27b" />
			</div>
			<div class="setting-row">
				<label>Max Iterations</label>
				<input class="input" type="number" bind:value={editing.max_iterations} min="1" max="200" />
			</div>
			<div class="setting-row">
				<label>Context Tokens</label>
				<input class="input" type="number" bind:value={editing.context_tokens} min="1024" max="131072" step="1024" />
			</div>
			<div class="setting-row">
				<label>Thinking</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={editing.thinking} />
					<span>{editing.thinking ? 'On' : 'Off'}</span>
				</label>
			</div>
			<div class="setting-row">
				<label>Vision</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={editing.vision} />
					<span>{editing.vision ? 'On' : 'Off'}</span>
				</label>
			</div>
			<p class="hint">모델이 이미지를 볼 수 있으면 켜세요. 켜면 작업 중 생성한 스크린샷도 read_file로 직접 봅니다.</p>
			<div class="setting-row">
				<label>Enabled</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={editing.enabled} />
					<span>{editing.enabled ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>
			<div class="embedded-editor-actions">
				<button class="btn btn-primary" onclick={saveEndpoint}>Save</button>
				<button class="btn btn-outline" onclick={closeEditor}>Cancel</button>
			</div>
		</div>
	</div>
{/if}

<!-- MAGI consensus section -->
<div class="magi-section">
	<div class="magi-header">
		<h3>🧠 MAGI 합의</h3>
		<label class="toggle">
			<input type="checkbox" bind:checked={magiConfig.enabled} />
			<span>{magiConfig?.enabled ? '활성' : '비활성'}</span>
		</label>
	</div>
	<p class="hint">3개의 서로 다른 모델이 블라인드로 답안을 제시하고, 판정자가 종합합니다. Phase 1은 자문형(도구 없음)입니다.</p>

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
					<select class="input magi-input" bind:value={proposer.endpoint_id}>
						<option value="">— 엔드포인트 선택 —</option>
						{#each embeddedEndpoints as ep}
							{#if ep.enabled}
								<option value={ep.id}>{ep.label || ep.id} ({ep.model})</option>
							{/if}
						{/each}
					</select>
					<select class="input magi-input" bind:value={proposer.persona}>
						{#each PERSONAS as p}
							<option value={p.value}>{p.label}</option>
						{/each}
					</select>
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
				{/if}
			</div>
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

<MCPServerManagement />

<style>
	.text-muted { color: var(--text-secondary); }

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

	.toggle {
		display: flex;
		align-items: center;
		gap: 8px;
		cursor: pointer;
		font-weight: 400;
		min-width: 0;
	}

	.embedded-endpoints {
		margin-top: 16px;
	}
	.embedded-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 12px;
	}
	.embedded-header h3 {
		margin: 0;
		font-size: 14px;
		font-weight: 600;
	}
	.embedded-card {
		border: 1px solid var(--border);
		border-radius: 8px;
		padding: 12px 16px;
		margin-bottom: 8px;
		background: var(--bg-card);
		transition: border-color 0.2s;
	}
	.embedded-card.active {
		border-color: var(--accent);
		box-shadow: 0 0 0 1px var(--accent);
	}
	.embedded-card-header {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 8px;
		flex-wrap: wrap;
	}
	.embedded-card-header strong {
		font-size: 14px;
	}
	.embedded-actions {
		display: flex;
		gap: 6px;
		margin-left: auto;
		align-items: center;
	}
	.embedded-card-body {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
		gap: 4px 16px;
	}
	.embedded-detail {
		font-size: 12px;
		color: var(--text-secondary);
	}
	.embedded-detail code {
		font-size: 11px;
		background: var(--bg-input);
		padding: 1px 4px;
		border-radius: 3px;
	}
	.embedded-label {
		color: var(--text-muted);
		margin-right: 4px;
	}
	.embedded-test-result {
		font-size: 12px;
		min-width: 80px;
	}
	.embedded-editor-actions {
		display: flex;
		gap: 8px;
		margin-top: 16px;
	}

	.badge {
		font-size: 11px;
		font-weight: 600;
		padding: 2px 8px;
		border-radius: 10px;
	}
	.badge-success {
		background: color-mix(in srgb, var(--success) 15%, transparent);
		color: var(--success);
	}
	.badge-muted {
		background: var(--bg-tertiary);
		color: var(--text-secondary);
	}

	.btn-sm {
		padding: 4px 12px;
		font-size: 12px;
		font-weight: 600;
		background: var(--accent);
		color: white;
		border: none;
		border-radius: 6px;
		cursor: pointer;
		transition: opacity 0.15s;
	}
	.btn-sm:hover { opacity: 0.85; }
	.btn-sm:disabled { opacity: 0.5; cursor: not-allowed; }

	.btn-outline {
		background: transparent;
		color: var(--text-primary);
		border: 1px solid var(--border);
	}
	.btn-outline:hover {
		border-color: var(--accent);
		color: var(--accent);
	}

	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 1000;
	}
	.modal-card {
		background: var(--bg-2);
		border: 1px solid var(--border);
		border-radius: 12px;
		padding: 24px;
		width: 90%;
		max-width: 500px;
		max-height: 80vh;
		overflow-y: auto;
		box-shadow: var(--shadow-3);
	}
	.modal-card h3 {
		margin: 0 0 16px;
		font-size: 16px;
	}
	.modal-card .setting-row > label:not(.toggle) {
		flex: 0 0 120px;
	}
	.modal-card .setting-row .input {
		flex: 1 1 auto;
		max-width: none;
		min-width: 0;
	}
	.modal-card .setting-row:not(.column) > .hint {
		flex: 0 0 calc(100% - 136px);
		margin-left: 136px;
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
	}

	/* ── MAGI section ── */
	.magi-section {
		margin-top: 24px;
		border-top: 1px solid var(--border);
		padding-top: 16px;
	}
	.magi-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 4px;
	}
	.magi-header h3 {
		margin: 0;
		font-size: 14px;
		font-weight: 600;
	}
	.magi-subsection {
		margin-top: 12px;
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
	.magi-custom-prompt {
		flex: 1 1 100%;
		max-width: none;
		resize: vertical;
	}
	.magi-aggregator {
		display: flex;
		align-items: center;
		gap: 16px;
		flex-wrap: wrap;
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
		margin-top: 12px;
	}
	.magi-save-msg {
		font-size: 12px;
	}
</style>
