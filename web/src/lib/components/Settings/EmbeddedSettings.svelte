<script>
	import { onMount } from 'svelte';
import { apiGet, apiPost, apiPut, apiDelete } from '$lib/api.js';

	import ProviderEnableToggle from './ProviderEnableToggle.svelte';
	import MCPServerManagement from './MCPServerManagement.svelte';

	/** @type {{ embedded_active_id: string, custom_prompt_embedded: string, shell: string }} */
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

	let editorModels = [];
	let editorModelsBusy = false;
	let editorApiBase = '';

	// Copilot device-flow modal state.
	// step: starting | authorize | models | error
	let copilot = null;

	async function loadEndpoints() {
		const res = await apiGet('/api/config/embedded');
		if (res?.data) {
			embeddedEndpoints = res.data.endpoints || [];
			loaded = true;
		}
	}

	function openEditor(ep = null) {
		editorModels = [];
		editorModelsBusy = false;
		if (ep) {
			// Older API responses may omit subagent; default true (matches server BC migration).
			editing = {
				...ep,
				subagent: ep.subagent ?? true,
				auth: ep.auth || '',
				github_token: ep.github_token || '',
				_existing: true
			};
		} else {
			editing = {
				id: '',
				label: '',
				base_url: 'http://127.0.0.1:8080/v1/chat/completions',
				api_key: '',
				model: '',
				enabled: true,
				thinking: false,
				vision: false,
				subagent: true,
				max_iterations: 40,
				context_tokens: 32768,
				max_concurrent: 0,
				reasoning_budget_tokens: 0,
				handoff_no_thinking: false,
				auth: '',
				github_token: ''
			};
		}
	}

	function closeEditor() {
		editing = null;
		editorModels = [];
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
			subagent: !!editing.subagent,
			max_iterations: parseInt(editing.max_iterations) || 40,
			context_tokens: parseInt(editing.context_tokens) || 32768,
			max_concurrent: parseInt(editing.max_concurrent) || 0,
			reasoning_budget_tokens: Math.max(0, parseInt(editing.reasoning_budget_tokens) || 0),
			handoff_no_thinking: !!editing.handoff_no_thinking,
			auth: editing.auth || '',
			github_token: editing.github_token || ''
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
				id: ep.id,
				base_url: ep.base_url,
				api_key: ep.api_key,
				model: ep.model,
				auth: ep.auth || '',
				github_token: ep.github_token || ''
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

	// ── Copilot device-flow auth + model picker ──────────────────────────────

	function openCopilot() {
		copilot = {
			open: true,
			step: 'starting',
			device: null,
			token: '',
			models: [],
			selected: null,
			apiBase: 'https://api.githubcopilot.com',
			error: '',
			busy: false,
			interval: 5,
			pollTimer: null,
			manualToken: '',
			id: '',
			label: '',
			contextTokens: 0
		};
		startCopilotAuth();
	}

	function closeCopilot() {
		if (copilot?.pollTimer) clearTimeout(copilot.pollTimer);
		copilot = null;
	}

	async function startCopilotAuth() {
		if (copilot.pollTimer) clearTimeout(copilot.pollTimer);
		copilot.pollTimer = null;
		copilot.step = 'starting';
		copilot.error = '';
		const res = await apiPost('/api/config/copilot/device/start', {});
		if (!copilot) return;
		if (res?.success && res.data) {
			copilot.device = res.data;
			copilot.interval = Math.max(5, res.data.interval || 5);
			copilot.step = 'authorize';
			scheduleCopilotPoll();
		} else {
			copilot.step = 'authorize';
			copilot.error = res?.message || 'GitHub 인증을 시작하지 못했습니다. 토큰을 직접 입력해 보세요.';
		}
	}

	function scheduleCopilotPoll(interval) {
		if (!copilot) return;
		if (copilot.pollTimer) clearTimeout(copilot.pollTimer);
		const seconds = Math.max(5, interval || copilot.interval || 5);
		copilot.pollTimer = setTimeout(pollCopilot, seconds * 1000);
	}

	async function pollCopilot() {
		if (!copilot || !copilot.device) return;
		const res = await apiPost('/api/config/copilot/device/poll', {
			device_code: copilot.device.device_code
		});
		if (!copilot) return;
		if (!res?.success) {
			copilot.error = res?.message || '인증 상태를 확인하지 못했습니다.';
			scheduleCopilotPoll();
			return;
		}
		const d = res.data || {};
		if (d.status === 'authorized') {
			copilot.token = d.github_token;
			await loadCopilotModels(d.github_token);
			return;
		}
		if (d.status === 'expired' || d.status === 'denied' || d.status === 'error') {
			copilot.step = 'error';
			copilot.error = d.message || d.status;
			return;
		}
		if (d.status === 'slow_down') copilot.interval += 5;
		scheduleCopilotPoll();
	}

	async function useManualToken() {
		const token = (copilot?.manualToken || '').trim();
		if (!token) {
			copilot.error = 'GitHub 토큰을 입력하세요.';
			return;
		}
		if (copilot.pollTimer) clearTimeout(copilot.pollTimer);
		copilot.pollTimer = null;
		copilot.token = token;
		await loadCopilotModels(token);
	}

	async function loadCopilotModels(token) {
		if (!copilot) return;
		copilot.step = 'models';
		copilot.busy = true;
		copilot.error = '';
		const res = await apiPost('/api/config/copilot/models', { github_token: token });
		if (!copilot) return;
		copilot.busy = false;
		if (!res?.success || !res.data) {
			copilot.step = 'error';
			copilot.error = res?.message || '모델 목록을 가져오지 못했습니다.';
			return;
		}
		const models = (res.data.models || []).slice().sort((a, b) => {
			if (!!b.supports_tools !== !!a.supports_tools) return b.supports_tools ? 1 : -1;
			return (a.name || a.id).localeCompare(b.name || b.id);
		});
		copilot.models = models;
		copilot.apiBase = res.data.api_base || 'https://api.githubcopilot.com';
		if (!models.length) {
			copilot.step = 'error';
			copilot.error = '사용 가능한 Chat Completions 모델이 없습니다. Copilot 구독 상태를 확인하세요.';
			return;
		}
		selectCopilotModel(models.find((m) => m.supports_tools) || models[0]);
	}

	function selectCopilotModel(m) {
		if (!copilot || !m) return;
		copilot.selected = m;
		copilot.contextTokens = m.context_tokens || m.context_window_tokens || 0;
		const slug = (m.id || '').replace(/[^a-zA-Z0-9._-]+/g, '-').toLowerCase();
		copilot.id = 'copilot-' + slug;
		copilot.label = m.name || m.id;
	}

	async function createCopilotEndpoint() {
		if (!copilot || !copilot.selected) return;
		if (!copilot.id.trim()) {
			copilot.error = '엔드포인트 ID를 입력하세요.';
			return;
		}
		copilot.busy = true;
		copilot.error = '';
		const apiPath = copilot.selected.api === 'responses' ? '/responses' : '/chat/completions';
		const body = {
			id: copilot.id.trim(),
			label: copilot.label.trim() || copilot.selected.name,
			base_url: (copilot.apiBase || 'https://api.githubcopilot.com') + apiPath,
			api_key: '',
			model: copilot.selected.id,
			enabled: true,
			thinking: false,
			vision: !!copilot.selected.supports_vision,
			subagent: true,
			max_iterations: 40,
			context_tokens: parseInt(copilot.contextTokens) || copilot.selected.context_tokens || 128000,
			max_concurrent: 0,
			reasoning_budget_tokens: 0,
			handoff_no_thinking: false,
			auth: 'copilot',
			github_token: copilot.token
		};
		const res = await apiPost('/api/config/embedded', body);
		if (!copilot) return;
		copilot.busy = false;
		if (res?.success) {
			closeCopilot();
			await loadEndpoints();
			await reloadEndpoints();
		} else {
			copilot.error = res?.message || '엔드포인트를 저장하지 못했습니다.';
		}
	}

	async function copyUserCode() {
		if (!copilot?.device?.user_code) return;
		try {
			await navigator.clipboard.writeText(copilot.device.user_code);
		} catch {
			/* clipboard may be unavailable; the code is visible for manual copy */
		}
	}

	async function refreshEditorModels() {
		if (!editing) return;
		editorModelsBusy = true;
		// The GET response masks tokens. For an existing endpoint send the id so
		// the server can use the stored token; otherwise use the typed token.
		const token = (editing.github_token || '').trim();
		const useStored = editing._existing && (!token || token.includes('****'));
		const payload = useStored ? { id: editing.id } : { github_token: token };
		const res = await apiPost('/api/config/copilot/models', payload);
		editorModelsBusy = false;
		if (res?.success && res.data?.models) {
			editorModels = res.data.models.slice().sort((a, b) => {
				if (!!b.supports_tools !== !!a.supports_tools) return b.supports_tools ? 1 : -1;
				return (a.name || a.id).localeCompare(b.name || b.id);
			});
			editorApiBase = res.data.api_base || '';
			applyEditorModel();
			if (!editing.base_url) {
				editing.base_url = (res.data.api_base || 'https://api.githubcopilot.com') + '/chat/completions';
			}
		} else {
			alert('Error: ' + (res?.message || 'Failed to load models'));
		}
	}

	function applyEditorModel() {
		if (!editing) return;
		const m = editorModels.find((x) => x.id === editing.model);
		if (!m) return;
		if (m.context_tokens) editing.context_tokens = m.context_tokens;
		editing.vision = !!m.supports_vision;
		// Responses-only models (GPT-6.1 Sol) must call /responses, not
		// /chat/completions — the latter is rejected for those models.
		if (editorApiBase && editing.auth === 'copilot') {
			editing.base_url = editorApiBase + (m.api === 'responses' ? '/responses' : '/chat/completions');
		}
	}

	// Load endpoints on mount
	onMount(loadEndpoints);
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
		rows="6"
		placeholder="임베디드 프로바이더 실행 시 추가로 전달할 지시문을 입력하세요."
	></textarea>
</div>

<div class="setting-row column">
	<label>Shell</label>
	<input
		class="input"
		type="text"
		bind:value={configData.shell}
		placeholder="예: C:\Program Files\Git\bin\bash.exe, pwsh, /bin/zsh (비우면 자동 탐지)"
	/>
	<span class="hint">임베디드 프로바이더의 bash 도구가 명령을 실행할 셸을 명시합니다. 설정하면 시스템 자동 탐지를 건너뛰고 이 셸을 바로 사용합니다. 값은 실행 파일 경로/이름 하나만 — 인자(&quot;-c&quot; 등)는 셸 종류를 보고 자동으로 붙습니다.</span>
</div>

<div class="embedded-endpoints">
	<div class="embedded-header">
		<h3>Endpoints</h3>
		<div class="embedded-header-actions">
			<button class="btn btn-sm btn-outline" onclick={openCopilot}>+ Copilot</button>
			<button class="btn btn-sm btn-outline" onclick={() => openEditor(null)}>+ Add</button>
		</div>
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
					{#if ep.enabled && ep.subagent}<span class="badge badge-subagent">Subagent</span>{/if}
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
					<div class="embedded-detail"><span class="embedded-label">Auth:</span> {ep.auth === 'copilot' ? 'GitHub Copilot' : 'API Key'}</div>
					<div class="embedded-detail"><span class="embedded-label">Context:</span> {ep.context_tokens?.toLocaleString()} tokens</div>
					<div class="embedded-detail"><span class="embedded-label">Max Iterations:</span> {ep.max_iterations}</div>
				<div class="embedded-detail"><span class="embedded-label">Max Concurrent:</span> {ep.max_concurrent || '∞'}</div>
					<div class="embedded-detail"><span class="embedded-label">Thinking:</span> {ep.thinking ? 'On' : 'Off'}</div>
					<div class="embedded-detail"><span class="embedded-label">Vision:</span> {ep.vision ? 'On' : 'Off'}</div>
					<div class="embedded-detail"><span class="embedded-label">Subagent:</span> {ep.subagent ? 'On' : 'Off'}</div>
					<div class="embedded-detail"><span class="embedded-label">Reasoning Budget:</span> {ep.reasoning_budget_tokens > 0 ? `${ep.reasoning_budget_tokens.toLocaleString()} tokens` : 'Off'}</div>
					<div class="embedded-detail"><span class="embedded-label">Handoff No-Thinking:</span> {ep.handoff_no_thinking ? 'On' : 'Off'}</div>
				</div>
			</div>
		{/each}
	{/if}
</div>

<!-- Embedded endpoint editor modal — only Cancel closes (no outside-click dismiss) -->
{#if editing}
	<div class="modal-overlay">
		<div class="modal-card">
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
				<label>Endpoint URL</label>
				<input class="input" type="text" bind:value={editing.base_url} placeholder="http://127.0.0.1:8080/v1/chat/completions" />
			</div>
			<p class="hint">호출할 전체 URL을 그대로 적으세요. 뒤에 경로를 덧붙이지 않으므로 OpenAI 형식이 아닌 게이트웨이도 쓸 수 있습니다. 기존 설정처럼 <code>/v1</code>로 끝나면 <code>/chat/completions</code>를 붙여 호환 처리합니다.</p>
			<div class="setting-row">
				<label>Auth</label>
				<select class="input" bind:value={editing.auth}>
					<option value="">API Key / OpenAI-compatible</option>
					<option value="copilot">GitHub Copilot</option>
				</select>
			</div>
			{#if editing.auth === 'copilot'}
				<div class="setting-row">
					<label>GitHub Token</label>
					<input class="input" type="password" bind:value={editing.github_token} placeholder="ghu_... (비우면 저장된 토큰 유지)" />
				</div>
				<p class="hint">Copilot은 GitHub 토큰을 단기 API 토큰으로 교환해 사용합니다. 저장된 토큰은 GET 응답에서 마스킹되므로, 그대로 두면 기존 토큰이 유지됩니다.</p>
				<div class="setting-row">
					<label>Model</label>
					<input class="input" type="text" bind:value={editing.model} placeholder="gpt-4o" />
					<button class="btn btn-sm btn-outline" onclick={refreshEditorModels} disabled={editorModelsBusy}>
						{editorModelsBusy ? '...' : '모델 불러오기'}
					</button>
				</div>
				{#if editorModels.length > 0}
					<div class="setting-row">
						<label>Model List</label>
						<select class="input" bind:value={editing.model} onchange={applyEditorModel}>
							{#each editorModels as m}
								<option value={m.id}>
									{m.name} — context {(m.context_tokens || 0).toLocaleString()}{m.supports_tools ? '' : ' (도구 미지원)'}
								</option>
							{/each}
						</select>
					</div>
				{/if}
				<p class="hint">모델을 고르면 Context Tokens가 모델의 최대 입력 컨텍스트로 채워집니다. 핸드오프는 이 값의 75%에서 발동합니다.</p>
			{:else}
				<div class="setting-row">
					<label>API Key</label>
					<input class="input" type="password" bind:value={editing.api_key} placeholder="Leave empty for local servers" />
				</div>
				<div class="setting-row">
					<label>Model</label>
					<input class="input" type="text" bind:value={editing.model} placeholder="qwen3-27b" />
				</div>
			{/if}
			<div class="setting-row">
				<label>Max Iterations</label>
				<input class="input" type="number" bind:value={editing.max_iterations} min="1" max="200" />
			</div>
			<div class="setting-row">
				<label>Context Tokens</label>
				<input class="input" type="number" bind:value={editing.context_tokens} min="1024" max="2000000" step="1024" />
			</div>
			<div class="setting-row">
				<label>Max Concurrent</label>
				<input class="input" type="number" bind:value={editing.max_concurrent} min="0" placeholder="0" />
			</div>
			<p class="hint">llama.cpp <code>--parallel</code> 값과 일치시키세요. 0이면 제한 없음.</p>
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
				<label>Reasoning Budget</label>
				<input class="input" type="number" bind:value={editing.reasoning_budget_tokens} min="0" step="1024" placeholder="0" />
			</div>
			<p class="hint">0보다 크면 요청마다 <code>reasoning_budget_tokens</code>를 보냅니다(최대 max_tokens의 절반). 추론만 하다 출력 한도에서 끊기는 것을 막습니다. 이 필드를 지원하는 서버(Strata)에서만 켜세요. 0이면 보내지 않습니다.</p>
			<div class="setting-row">
				<label>Handoff No-Thinking</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={editing.handoff_no_thinking} />
					<span>{editing.handoff_no_thinking ? 'On' : 'Off'}</span>
				</label>
			</div>
			<p class="hint">켜면 컨텍스트 핸드오프 요약 요청에 <code>chat_template_kwargs: {'{'}"enable_thinking": false{'}'}</code>를 보내 추론 없이 요약하게 합니다.</p>
			<div class="setting-row">
				<label>Subagent</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={editing.subagent} />
					<span>{editing.subagent ? 'On' : 'Off'}</span>
				</label>
			</div>
			<p class="hint">켜면 <code>spawn_subagents</code>의 endpoint_id로 이 엔드포인트를 사용할 수 있습니다. 서브에이전트는 embedded 프로바이더만 지원합니다.</p>
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

<!-- Copilot device-flow modal -->
{#if copilot?.open}
	<div class="modal-overlay">
		<div class="modal-card copilot-modal">
			<h3>Add Copilot Endpoint</h3>

			{#if copilot.step === 'starting'}
				<p class="text-muted">GitHub 인증을 준비하는 중...</p>
			{/if}

			{#if copilot.step === 'authorize'}
				{#if copilot.device}
					<p>GitHub에서 아래 코드를 입력해 Copilot 접근을 승인하세요.</p>
					<div class="copilot-code-row">
						<code class="copilot-code">{copilot.device.user_code}</code>
						<button class="btn btn-sm btn-outline" onclick={copyUserCode}>복사</button>
						<a class="btn btn-sm btn-outline copilot-link" href={copilot.device.verification_uri} target="_blank" rel="noreferrer">GitHub 열기</a>
					</div>
					<p class="hint">승인하면 자동으로 모델 목록을 불러옵니다. 코드 만료: {Math.round((copilot.device.expires_in || 900) / 60)}분</p>
				{/if}
				<div class="copilot-manual">
					<label class="hint" for="copilot-token">이미 GitHub 토큰이 있다면 직접 입력</label>
					<div class="copilot-manual-row">
						<input id="copilot-token" class="input" type="password" bind:value={copilot.manualToken} placeholder="ghu_... 또는 ghp_..." />
						<button class="btn btn-sm btn-outline" onclick={useManualToken}>토큰으로 불러오기</button>
					</div>
				</div>
			{/if}

			{#if copilot.step === 'models'}
				{#if copilot.busy}
					<p class="text-muted">모델 목록을 불러오는 중...</p>
				{:else}
					<p class="hint">사용할 모델을 선택하세요. 도구 호출을 지원하는 모델을 권장합니다.</p>
					<div class="copilot-model-list">
						{#each copilot.models as m}
							<button class="copilot-model" class:selected={copilot.selected?.id === m.id} onclick={() => selectCopilotModel(m)}>
								<span class="copilot-model-name">{m.name}</span>
								<span class="copilot-model-meta">{m.id}</span>
								<span class="copilot-model-badges">
									<span>context {(m.context_tokens || 0).toLocaleString()}</span>
									{#if m.api === 'responses'}<span class="badge badge-muted">Responses API</span>{/if}
									{#if m.supports_tools}<span class="badge badge-subagent">Tools</span>{/if}
									{#if m.supports_vision}<span class="badge badge-success">Vision</span>{/if}
									{#if !m.supports_tools}<span class="badge badge-muted">No tools</span>{/if}
								</span>
							</button>
						{/each}
					</div>
					{#if copilot.selected}
						<div class="setting-row">
							<label>ID</label>
							<input class="input" type="text" bind:value={copilot.id} />
						</div>
						<div class="setting-row">
							<label>Label</label>
							<input class="input" type="text" bind:value={copilot.label} />
						</div>
						<div class="setting-row">
							<label>Context Tokens</label>
							<input class="input" type="number" bind:value={copilot.contextTokens} min="1024" max="2000000" step="1024" />
						</div>
						<p class="hint">핸드오프 기준은 Context Tokens의 75%입니다. Copilot 모델의 max_prompt_tokens(없으면 컨텍스트 창)으로 자동 설정됩니다.</p>
					{/if}
				{/if}
			{/if}

			{#if copilot.error}
				<p class="hint error">{copilot.error}</p>
				{#if copilot.step === 'error'}
					<div class="embedded-editor-actions">
						<button class="btn btn-outline" onclick={startCopilotAuth}>다시 인증</button>
						<button class="btn btn-outline" onclick={closeCopilot}>취소</button>
					</div>
				{/if}
			{/if}

			{#if copilot.step !== 'error'}
				<div class="embedded-editor-actions">
					<button class="btn btn-primary" onclick={createCopilotEndpoint} disabled={copilot.step !== 'models' || copilot.busy || !copilot.selected}>
						Add Endpoint
					</button>
					<button class="btn btn-outline" onclick={closeCopilot}>Cancel</button>
				</div>
			{/if}
		</div>
	</div>
{/if}

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
		font-size: 13px;
		font-weight: 500;
		flex: 0 0 180px;
		color: var(--text-primary);
	}

	.setting-row .input {
		flex: 1 1 200px;
		max-width: 340px;
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
		font-size: 12px;
		color: var(--text-tertiary);
		line-height: 1.55;
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
	.embedded-header-actions {
		display: flex;
		gap: 6px;
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

	.copilot-modal {
		max-width: 620px;
	}
	.copilot-code-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin: 12px 0;
		flex-wrap: wrap;
	}
	.copilot-code {
		font-size: 22px;
		font-weight: 700;
		letter-spacing: 3px;
		padding: 8px 14px;
		border: 1px dashed var(--accent);
		border-radius: 8px;
		background: var(--bg-input);
	}
	.copilot-link {
		text-decoration: none;
	}
	.copilot-manual {
		margin-top: 14px;
		padding-top: 12px;
		border-top: 1px solid var(--border);
	}
	.copilot-manual-row {
		display: flex;
		gap: 8px;
		margin-top: 6px;
	}
	.copilot-manual-row .input {
		flex: 1 1 auto;
		min-width: 0;
		max-width: none;
	}
	.copilot-model-list {
		max-height: 260px;
		overflow-y: auto;
		border: 1px solid var(--border);
		border-radius: 8px;
		margin: 8px 0 12px;
	}
	.copilot-model {
		display: grid;
		grid-template-columns: 1fr auto;
		gap: 2px 12px;
		width: 100%;
		padding: 8px 12px;
		text-align: left;
		background: transparent;
		border: none;
		border-bottom: 1px solid var(--border);
		color: var(--text-primary);
		cursor: pointer;
		font: inherit;
	}
	.copilot-model:last-child {
		border-bottom: none;
	}
	.copilot-model:hover {
		background: var(--bg-tertiary);
	}
	.copilot-model.selected {
		background: color-mix(in srgb, var(--accent) 12%, transparent);
		box-shadow: inset 3px 0 0 var(--accent);
	}
	.copilot-model-name {
		font-weight: 600;
		font-size: 13px;
	}
	.copilot-model-meta {
		grid-column: 1;
		font-size: 11px;
		color: var(--text-muted);
	}
	.copilot-model-badges {
		grid-column: 2;
		grid-row: 1 / span 2;
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 11px;
		color: var(--text-secondary);
		white-space: nowrap;
	}
	.hint.error {
		color: var(--danger);
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
	.badge-subagent {
		background: color-mix(in srgb, var(--accent) 15%, transparent);
		color: var(--accent);
	}


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
</style>
