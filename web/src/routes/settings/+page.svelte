<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPatch, apiPost, apiDelete, apiDownload, apiUpload } from '$lib/api.js';

	let configData = {};
	let loaded = false;
	let saving = false;
	let saveMsg = '';
	let restarting = false;

	let mcpStatus = { claude: null, opencode: null };
	let mcpLoaded = false;
	let mcpRegistering = {};
	let mcpError = '';

	let syncing = false;
	let syncResult = '';

	let preview = null;
	let previewLoading = false;
	let previewError = '';
	let previewMode = 'streaming'; // streaming | compact | withGuide
	let previewOpen = false;

	let modelOptions = { claude: [], opencode: [], pi: [] };

	// Embedded endpoints state
	let embeddedEndpoints = [];
	let embeddedLoaded = false;
	let embeddedEditing = null; // null = not editing, object = editing/creating
	let embeddedTestBusy = {};
	let embeddedTestResult = {};

	// Backup / export / import state
	let projectsList = [];
	let backupBusy = false;
	let backupMsg = '';
	let exportBusy = false;
	let exportProject = '';
	let exportMsg = '';
	let importFile = null;
	let importBusy = false;
	let importPreview = null;
	let importResult = null;
	let importMsg = '';

	onMount(async () => {
		const [configRes, mcpRes, modelRes, projRes, embeddedRes] = await Promise.all([
			apiGet('/api/config'),
			apiGet('/api/mcp/status'),
			apiGet('/api/config/model-options'),
			apiGet('/api/projects'),
			apiGet('/api/config/embedded')
		]);
		if (configRes?.data) {
			configData = configRes.data;
			// concurrency_limits may be null when nothing is configured yet.
			if (!configData.concurrency_limits) configData.concurrency_limits = {};
		}
		if (mcpRes?.data) mcpStatus = mcpRes.data;
		if (modelRes?.data) modelOptions = modelRes.data;
		if (projRes?.data) projectsList = projRes.data;
		if (embeddedRes?.data) {
			embeddedEndpoints = embeddedRes.data.endpoints || [];
			embeddedLoaded = true;
		}
		mcpLoaded = true;
		loaded = true;
	});

	function triggerDownload(blob, filename) {
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = filename;
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
		setTimeout(() => URL.revokeObjectURL(url), 1000);
	}

	async function downloadBackup() {
		backupBusy = true;
		backupMsg = '';
		try {
			const blob = await apiDownload('/api/settings/db-backup');
			if (!blob) {
				backupMsg = 'Error: backup failed';
				return;
			}
			const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
			triggerDownload(blob, `shepherd-${ts}.db`);
			backupMsg = 'Downloaded';
		} catch (err) {
			backupMsg = 'Error: ' + (err?.message || 'download failed');
		} finally {
			backupBusy = false;
			setTimeout(() => backupMsg = '', 3000);
		}
	}

	async function exportTasks() {
		exportBusy = true;
		exportMsg = '';
		try {
			const url = exportProject
				? `/api/settings/tasks-export?project=${encodeURIComponent(exportProject)}`
				: '/api/settings/tasks-export';
			const blob = await apiDownload(url);
			if (!blob) {
				exportMsg = 'Error: export failed';
				return;
			}
			const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
			const namePart = exportProject ? `-${exportProject}` : '';
			triggerDownload(blob, `shepherd-tasks${namePart}-${ts}.jsonl`);
			exportMsg = 'Downloaded';
		} catch (err) {
			exportMsg = 'Error: ' + (err?.message || 'download failed');
		} finally {
			exportBusy = false;
			setTimeout(() => exportMsg = '', 3000);
		}
	}

	function onImportFileChange(e) {
		const f = e.target.files?.[0] || null;
		importFile = f;
		importPreview = null;
		importResult = null;
		importMsg = '';
	}

	async function previewImport() {
		if (!importFile) return;
		importBusy = true;
		importMsg = '';
		importResult = null;
		try {
			const fd = new FormData();
			fd.append('file', importFile);
			const res = await apiUpload('/api/settings/tasks-import-preview', fd);
			if (res?.success) {
				importPreview = res.data;
			} else {
				importPreview = null;
				importMsg = 'Error: ' + (res?.message || 'preview failed');
			}
		} finally {
			importBusy = false;
		}
	}

	async function confirmImport() {
		if (!importFile) return;
		if (!confirm('Import these tasks now?')) return;
		importBusy = true;
		importMsg = '';
		try {
			const fd = new FormData();
			fd.append('file', importFile);
			const res = await apiUpload('/api/settings/tasks-import', fd);
			if (res?.success) {
				importResult = res.data;
				importPreview = null;
			} else {
				importMsg = 'Error: ' + (res?.message || 'import failed');
			}
		} finally {
			importBusy = false;
		}
	}

	// If the configured model is not present in the select's options
	// (e.g. user edited config.json manually), append it so it stays visible.
	function optionsWithCurrent(opts, current) {
		if (!current) return opts;
		if (opts.some((o) => o.id === current)) return opts;
		return [...opts, { id: current, label: current + ' (not in config)' }];
	}

	async function loadPreview() {
		previewLoading = true;
		previewError = '';
		const res = await apiGet('/api/config/system-prompt-preview');
		if (res?.data) {
			preview = res.data;
		} else {
			previewError = res?.message || 'Failed to load preview';
		}
		previewLoading = false;
	}

	async function togglePreview() {
		previewOpen = !previewOpen;
		if (previewOpen && !preview) {
			await loadPreview();
		}
	}

	async function registerMCP(provider) {
		mcpRegistering[provider] = true;
		mcpError = '';
		const res = await apiPost('/api/mcp/register', { provider });
		if (res?.success) {
			const statusRes = await apiGet('/api/mcp/status');
			if (statusRes?.data) mcpStatus = statusRes.data;
		} else {
			mcpError = res?.message || 'Registration failed';
		}
		mcpRegistering[provider] = false;
	}

	// — Embedded endpoint CRUD —

	async function loadEmbeddedEndpoints() {
		const res = await apiGet('/api/config/embedded');
		if (res?.data) {
			embeddedEndpoints = res.data.endpoints || [];
			embeddedLoaded = true;
		}
	}

	function openEmbeddedEditor(ep = null) {
		embeddedEditing = ep ? { ...ep } : {
			id: '',
			label: '',
			base_url: 'http://127.0.0.1:8080/v1',
			api_key: '',
			model: '',
			enabled: true,
			thinking: false,
			max_iterations: 40,
			context_tokens: 32768
		};
	}

	function closeEmbeddedEditor() {
		embeddedEditing = null;
	}

	async function saveEmbeddedEndpoint() {
		if (!embeddedEditing) return;
		const isEdit = !!embeddedEditing._existing;
		const body = {
			id: embeddedEditing.id,
			label: embeddedEditing.label,
			base_url: embeddedEditing.base_url,
			api_key: embeddedEditing.api_key,
			model: embeddedEditing.model,
			enabled: embeddedEditing.enabled,
			thinking: embeddedEditing.thinking,
			max_iterations: parseInt(embeddedEditing.max_iterations) || 40,
			context_tokens: parseInt(embeddedEditing.context_tokens) || 32768
		};

		let res;
		if (isEdit) {
			res = await fetch(`/api/config/embedded/${encodeURIComponent(body.id)}`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then(r => r.json());
		} else {
			res = await apiPost('/api/config/embedded', body);
		}

		if (res?.success || res?.message) {
			closeEmbeddedEditor();
			await loadEmbeddedEndpoints();
		} else {
			alert('Error: ' + (res?.message || 'Failed to save'));
		}
	}

	async function deleteEmbeddedEndpoint(id) {
		if (!confirm('Delete this endpoint?')) return;
		const res = await fetch(`/api/config/embedded/${encodeURIComponent(id)}`, {
			method: 'DELETE'
		}).then(r => r.json());
		if (res?.success || res?.message) {
			await loadEmbeddedEndpoints();
		} else {
			alert('Error: ' + (res?.message || 'Failed to delete'));
		}
	}

	async function setActiveEndpoint(id) {
		const res = await fetch(`/api/config/embedded/${encodeURIComponent(id)}/set-active`, {
			method: 'POST'
		}).then(r => r.json());
		if (res?.success || res?.message) {
			configData.embedded_active_id = id;
			await loadEmbeddedEndpoints();
		} else {
			alert('Error: ' + (res?.message || 'Failed to set active'));
		}
	}

	async function testEmbeddedEndpoint(ep) {
		embeddedTestBusy[ep.id] = true;
		embeddedTestResult[ep.id] = '';
		try {
			const res = await apiPost('/api/config/embedded/test', {
				base_url: ep.base_url,
				api_key: ep.api_key,
				model: ep.model
			});
			if (res?.success) {
				embeddedTestResult[ep.id] = '✅ Connected';
			} else {
				embeddedTestResult[ep.id] = '❌ ' + (res?.message || 'Failed');
			}
		} catch (e) {
			embeddedTestResult[ep.id] = '❌ ' + e.message;
		}
		embeddedTestBusy[ep.id] = false;
		setTimeout(() => { embeddedTestResult[ep.id] = ''; }, 5000);
	}

	// Build a clean {provider: limit} map from the per-provider inputs.
	// Only positive integers are kept; 0 / blank means "no limit for that group".
	function buildConcurrencyLimits() {
		const src = configData.concurrency_limits || {};
		const out = {};
		for (const key of Object.keys(src)) {
			const n = parseInt(src[key]);
			if (Number.isFinite(n) && n > 0) out[key] = n;
		}
		return out;
	}

	async function save() {
		saving = true;
		saveMsg = '';
		const res = await apiPatch('/api/config', {
			language: configData.language,
			default_provider: configData.default_provider,
			max_sheep: parseInt(configData.max_sheep) || 12,
			max_concurrent_tasks: parseInt(configData.max_concurrent_tasks) || 0,
			concurrency_limits: buildConcurrencyLimits(),
			auto_approve: configData.auto_approve,
			session_reuse: configData.session_reuse,
			include_task_history: configData.include_task_history,
			include_mcp_guide: configData.include_mcp_guide,
			include_sheep_memory: configData.include_sheep_memory,
			sheep_memory_prompt: configData.sheep_memory_prompt || '',
			enable_file_browser: configData.enable_file_browser,
			custom_prompt_claude: configData.custom_prompt_claude || '',
			custom_prompt_opencode: configData.custom_prompt_opencode || '',
			custom_prompt_pi: configData.custom_prompt_pi || '',
			opencode_compact_prompt: configData.opencode_compact_prompt,
			opencode_thinking_default: configData.opencode_thinking_default,
			opencode_thinking_proxy_enabled: configData.opencode_thinking_proxy_enabled,
			opencode_thinking_proxy_port: parseInt(configData.opencode_thinking_proxy_port) || 8686,
			opencode_thinking_proxy_target: configData.opencode_thinking_proxy_target || '',
			opencode_thinking_model: configData.opencode_thinking_model || '',
			model_claude: configData.model_claude || '',
			model_opencode: configData.model_opencode || '',
			model_pi: configData.model_pi || '',
			task_timeout: (configData.task_timeout || '').trim() || '4h',
			wiki_enabled: configData.wiki_enabled,
			wiki_auto_ingest: configData.wiki_auto_ingest,
			wiki_max_context_pages: parseInt(configData.wiki_max_context_pages) || 2,
			wiki_max_page_content_chars: parseInt(configData.wiki_max_page_content_chars) || 2000,
			discord_notifications_enabled: configData.discord_notifications_enabled,
			discord_webhook_url: configData.discord_webhook_url || '',
			discord_notify_on_complete: configData.discord_notify_on_complete,
			discord_notify_on_fail: configData.discord_notify_on_fail,
			embedded_active_id: configData.embedded_active_id || '',
			custom_prompt_embedded: configData.custom_prompt_embedded || ''
		});
		if (res?.success) {
			saveMsg = 'Saved';
			if (previewOpen) await loadPreview();
		} else {
			saveMsg = 'Error: ' + (res?.message || 'Failed to save');
		}
		saving = false;
		setTimeout(() => saveMsg = '', 3000);
	}

	async function syncSkills() {
		syncing = true;
		syncResult = '';
		const res = await apiPost('/api/skills/sync-all');
		if (res?.success) {
			const d = res.data;
			syncResult = `Synced ${d.synced} skills to ${d.projects} projects` + (d.errors > 0 ? ` (${d.errors} errors)` : '');
		} else {
			syncResult = 'Error: ' + (res?.message || 'Sync failed');
		}
		syncing = false;
		setTimeout(() => syncResult = '', 5000);
	}

	async function restart() {
		if (!confirm('Restart the server?')) return;
		restarting = true;
		try {
			await apiPost('/api/system/restart', {});
		} catch {
			// Expected: server shuts down
		}
		// Wait and reload page
		setTimeout(() => {
			window.location.reload();
		}, 3000);
	}
</script>

<div class="page">
	<h1 class="page-title">Settings</h1>

	{#if !loaded}
		<p class="text-muted">Loading...</p>
	{:else}
		<div class="settings-form card">
			<div class="setting-row">
				<label>Language</label>
				<select class="input" bind:value={configData.language}>
					<option value="ko">한국어</option>
					<option value="en">English</option>
				</select>
			</div>

			<div class="setting-row">
				<label>Default Provider</label>
				<select class="input" bind:value={configData.default_provider}>
					<option value="claude">Claude</option>
					<option value="opencode">OpenCode</option>
					<option value="pi">Pi</option>
					<option value="embedded">Embedded (로컬 LLM)</option>
					<option value="auto">Auto</option>
				</select>
			</div>

			<div class="setting-row">
				<label>Claude Model</label>
				<select class="input" bind:value={configData.model_claude}>
					{#each optionsWithCurrent(modelOptions.claude, configData.model_claude) as opt}
						<option value={opt.id}>{opt.label}</option>
					{/each}
				</select>
			</div>

			<div class="setting-row">
				<label>OpenCode Model</label>
				<select class="input" bind:value={configData.model_opencode}>
					{#each optionsWithCurrent(modelOptions.opencode, configData.model_opencode) as opt}
						<option value={opt.id}>{opt.label}</option>
					{/each}
				</select>
			</div>

			<div class="setting-row">
				<label>Pi Model</label>
				<select class="input" bind:value={configData.model_pi}>
					{#each optionsWithCurrent(modelOptions.pi, configData.model_pi) as opt}
						<option value={opt.id}>{opt.label}</option>
					{/each}
				</select>
			</div>

			<div class="setting-row">
				<label>Max Sheep</label>
				<input class="input" type="number" bind:value={configData.max_sheep} min="1" max="50" />
			</div>

			<div class="setting-row">
				<label>Max Concurrent Tasks</label>
				<input class="input" type="number" bind:value={configData.max_concurrent_tasks} min="0" max="50" />
				<span class="hint">전체 동시 실행 작업 수의 천장(ceiling). 0이면 제한 없음. 아래 provider별 제한과 함께 적용되며, 작업은 두 제한을 모두 통과해야 실행됩니다.</span>
			</div>

			<div class="setting-row">
				<label>Per-Group Limits</label>
				<div class="conc-limits">
					<div class="conc-row">
						<span class="conc-label">🟠 Claude{configData.model_claude ? ` (${configData.model_claude})` : ''}</span>
						<input class="input conc-input" type="number" bind:value={configData.concurrency_limits['claude']} min="0" max="50" placeholder="0" />
					</div>
					{#each modelOptions.opencode as opt}
						{@const key = opt.id ? `opencode/${opt.id}` : 'opencode'}
						<div class="conc-row">
							<span class="conc-label" title={opt.id ? opt.id : 'OpenCode 모델 미지정 작업의 기본 그룹'}>🟢 {opt.id ? opt.label : 'OpenCode (모델 미지정 / 기본)'}</span>
							<input class="input conc-input" type="number" bind:value={configData.concurrency_limits[key]} min="0" max="50" placeholder="0" />
						</div>
					{/each}
					{#each modelOptions.pi as opt}
						{@const key = opt.id ? `pi/${opt.id}` : 'pi'}
						<div class="conc-row">
							<span class="conc-label" title={opt.id ? opt.id : 'Pi 모델 미지정 작업의 기본 그룹'}>🔵 {opt.id ? opt.label : 'Pi (모델 미지정 / 기본)'}</span>
							<input class="input conc-input" type="number" bind:value={configData.concurrency_limits[key]} min="0" max="50" placeholder="0" />
						</div>
					{/each}
				</div>
				<span class="hint">provider+model 그룹별 동시 실행 제한. 0이면 그 그룹은 제한 없음(전역 천장만 적용). OpenCode 모델 목록은 <code>~/.config/opencode/config.json</code>에 등록된 모델을 자동 표시합니다. 여러 local-llm 시스템을 모델로 구분해 각각 한도를 둘 수 있고, 작업별로 선택한 모델이 그 그룹에 집계됩니다. 모델을 지정하지 않은 OpenCode 작업은 "기본" 그룹으로 묶입니다. <code>auto</code> provider는 Claude 그룹에 포함됩니다.</span>
			</div>

			<div class="setting-row">
				<label>Task Timeout</label>
				<input class="input" type="text" bind:value={configData.task_timeout} placeholder="4h" />
				<span class="hint">Per-task execution cap (e.g. <code>30m</code>, <code>4h</code>, <code>8h30m</code>). Use <code>unlimited</code>, <code>0</code>, or <code>-1</code> to disable the deadline. Default: 4h.</span>
			</div>

			<div class="setting-row">
				<label>Auto Approve</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.auto_approve} />
					<span>{configData.auto_approve ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="setting-row">
				<label>File Browser</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.enable_file_browser} />
					<span>{configData.enable_file_browser ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="setting-section">Prompt Injection</div>

			<div class="setting-row">
				<label>Session Reuse</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.session_reuse} />
					<span>{configData.session_reuse ? 'Reuse' : 'Fresh'}</span>
				</label>
			</div>

			<div class="setting-row">
				<label>Task History</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.include_task_history} />
					<span>{configData.include_task_history ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="setting-row">
				<label>MCP Guide</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.include_mcp_guide} />
					<span>{configData.include_mcp_guide ? 'Enabled' : 'Disabled'}</span>
				</label>
			</div>

			<div class="setting-row">
				<label>Sheep Memory</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.include_sheep_memory} />
					<span>{configData.include_sheep_memory ? 'Enabled' : 'Disabled'}</span>
				</label>
				<span class="hint">양 이름 단위로 <code>~/.shepherd/sheep/&lt;name&gt;/</code> 에 누적되는 개인 기억. 프로젝트와 무관하게 양을 따라다니며 CLI(Claude/OpenCode/codex)에 중립이다.</span>
			</div>

			<div class="setting-row column">
				<label>Sheep Memory — System Prompt</label>
				<textarea
					class="input textarea"
					bind:value={configData.sheep_memory_prompt}
					rows="12"
					placeholder={`양에게 전달할 메모리 가이드라인. {{.MemoryDir}} 가 실제 디렉토리 경로로 치환됩니다.`}
				></textarea>
				<span class="hint"><code>{`{{.MemoryDir}}`}</code> 토큰은 작업 시점에 양의 실제 메모리 디렉토리 경로로 치환됩니다. 비워두면 메모리 섹션이 주입되지 않습니다.</span>
			</div>

			<div class="setting-row">
				<label>OpenCode Compact</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.opencode_compact_prompt} />
					<span>{configData.opencode_compact_prompt ? 'Compact' : 'Full (same as Claude)'}</span>
				</label>
			</div>

			<div class="setting-row">
				<label>OpenCode Thinking (default)</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.opencode_thinking_default} />
					<span>{configData.opencode_thinking_default ? 'On' : 'Off'}</span>
				</label>
				<span class="hint">Default reasoning mode for OpenCode tasks. Per-project toggle on the project page overrides this.</span>
			</div>

			<div class="setting-row">
				<label>Thinking Proxy</label>
				<label class="toggle">
					<input type="checkbox" bind:checked={configData.opencode_thinking_proxy_enabled} />
					<span>{configData.opencode_thinking_proxy_enabled ? 'Enabled' : 'Disabled'}</span>
				</label>
				<span class="hint">Loopback proxy that injects <code>chat_template_kwargs.enable_thinking</code> into OpenAI-compatible chat completions before forwarding to the upstream server. Required because opencode strips that field. Restart the daemon after toggling.</span>
			</div>

			<div class="setting-row">
				<label>Thinking Proxy Port</label>
				<input
					class="input"
					type="number"
					min="1024"
					max="65535"
					bind:value={configData.opencode_thinking_proxy_port}
				/>
				<span class="hint">127.0.0.1:&lt;port&gt; that the proxy listens on. Use this URL as <code>baseURL</code> in your opencode config thinking provider entry.</span>
			</div>

			<div class="setting-row">
				<label>Thinking Proxy Target</label>
				<input
					class="input"
					type="text"
					placeholder="http://127.0.0.1:8083/v1"
					bind:value={configData.opencode_thinking_proxy_target}
				/>
				<span class="hint">Real OpenAI-compatible endpoint the proxy forwards to (your llama-server, etc.). Include scheme, host, port, and any path prefix.</span>
			</div>

			<div class="setting-row">
				<label>Thinking Model</label>
				<input
					class="input"
					type="text"
					placeholder="qwen3.6-thinking/qwen3.6-27b"
					bind:value={configData.opencode_thinking_model}
				/>
				<span class="hint"><code>provider/model</code> id used when the per-project Thinking toggle is on. The provider entry in opencode config should set <code>baseURL</code> to the proxy.</span>
			</div>

			<div class="setting-row column">
				<label>Custom Prompt — Claude</label>
				<textarea
					class="input textarea"
					bind:value={configData.custom_prompt_claude}
					rows="6"
					placeholder="Claude Code 실행 시 추가로 전달할 지시문을 입력하세요."
				></textarea>
				<span class="hint">Injected only when the task runs on Claude Code.</span>
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

			<hr class="setting-divider" />

			<div class="setting-section-title">Wiki</div>

			<div class="setting-row">
				<label>Wiki Enabled</label>
				<div class="toggle">
					<input type="checkbox" bind:checked={configData.wiki_enabled} />
					<span>{configData.wiki_enabled ? 'Enabled' : 'Disabled'}</span>
				</div>
			</div>

			<div class="setting-row">
				<label>Auto Ingest</label>
				<div class="toggle">
					<input type="checkbox" bind:checked={configData.wiki_auto_ingest} />
					<span>{configData.wiki_auto_ingest ? 'Enabled' : 'Disabled'}</span>
				</div>
			</div>

			<div class="setting-row">
				<label>Max Context Pages</label>
				<input class="input" type="number" bind:value={configData.wiki_max_context_pages} min="1" max="20" />
			</div>

			<div class="setting-row">
				<label>Max Page Content Chars</label>
				<input class="input" type="number" bind:value={configData.wiki_max_page_content_chars} min="100" max="10000" step="100" />
			</div>

			<hr class="setting-divider" />

			<div class="setting-section-title">Discord Notifications</div>

			<div class="setting-row">
				<label>Enabled</label>
				<div class="toggle">
					<input type="checkbox" bind:checked={configData.discord_notifications_enabled} />
					<span>{configData.discord_notifications_enabled ? 'Enabled' : 'Disabled'}</span>
				</div>
			</div>

			<div class="setting-row column">
				<label>Webhook URL</label>
				<input
					class="input"
					type="text"
					bind:value={configData.discord_webhook_url}
					placeholder="https://discord.com/api/webhooks/..."
				/>
				<span class="hint">Discord 채널의 Incoming Webhook URL을 입력하세요. Server Setting > Integrations > Webhooks에서 생성할 수 있습니다.</span>
			</div>

			<div class="setting-row">
				<label>Notify on Complete</label>
				<div class="toggle">
					<input type="checkbox" bind:checked={configData.discord_notify_on_complete} />
					<span>{configData.discord_notify_on_complete ? 'Enabled' : 'Disabled'}</span>
				</div>
			</div>

			<div class="setting-row">
				<label>Notify on Fail</label>
				<div class="toggle">
					<input type="checkbox" bind:checked={configData.discord_notify_on_fail} />
					<span>{configData.discord_notify_on_fail ? 'Enabled' : 'Disabled'}</span>
				</div>
			</div>

			<hr class="setting-divider" />

			<div class="setting-section-title">Embedded Provider (로컬 LLM)</div>
			<p class="hint">OpenAI 호환 API 서버(llama.cpp, vLLM, Ollama 등)를 직접 연결합니다. 서브프로세스 없이 in-process 에이전트 루프가 실행됩니다.</p>

			{#if !embeddedLoaded}
				<p class="text-muted">Loading...</p>
			{:else}
				<!-- Active endpoint selector -->
				<div class="setting-row">
					<label>Active Endpoint</label>
					<select class="input" bind:value={configData.embedded_active_id}>
						<option value="">— 선택 —</option>
						{#each embeddedEndpoints as ep}
							<option value={ep.id}>{ep.label || ep.id} ({ep.model})</option>
						{/each}
					</select>
				</div>

				<!-- Custom prompt for embedded -->
				<div class="setting-row column">
					<label>Custom Prompt — Embedded</label>
					<textarea
						class="input textarea"
						bind:value={configData.custom_prompt_embedded}
						rows="4"
						placeholder="임베디드 프로바이더 실행 시 추가로 전달할 지시문을 입력하세요."
					></textarea>
				</div>

				<!-- Endpoint list -->
				<div class="embedded-endpoints">
					<div class="embedded-header">
						<h3>Endpoints</h3>
						<button class="btn btn-sm btn-outline" onclick={() => openEmbeddedEditor(null)}>+ Add</button>
					</div>

					{#if embeddedEndpoints.length === 0}
						<p class="text-muted">No endpoints configured yet.</p>
					{:else}
						{#each embeddedEndpoints as ep}
							<div class="embedded-card" class:active={ep.is_active}>
								<div class="embedded-card-header">
									<strong>{ep.label || ep.id}</strong>
									{#if ep.is_active}<span class="badge badge-success">Active</span>{/if}
									{#if !ep.enabled}<span class="badge badge-muted">Disabled</span>{/if}
									<div class="embedded-actions">
										<button class="btn btn-sm btn-outline" onclick={() => testEmbeddedEndpoint(ep)} disabled={embeddedTestBusy[ep.id]}>
											{embeddedTestBusy[ep.id] ? '...' : 'Test'}
										</button>
										<button class="btn btn-sm btn-outline" onclick={() => setActiveEndpoint(ep.id)} disabled={ep.is_active}>Set Active</button>
										<button class="btn btn-sm btn-outline" onclick={() => openEmbeddedEditor(ep)}>Edit</button>
										<button class="btn btn-sm btn-outline" onclick={() => deleteEmbeddedEndpoint(ep.id)} disabled={ep.is_active}>Delete</button>
										<span class="embedded-test-result">{embeddedTestResult[ep.id] || ''}</span>
									</div>
								</div>
								<div class="embedded-card-body">
									<div class="embedded-detail"><span class="embedded-label">Model:</span> {ep.model}</div>
									<div class="embedded-detail"><span class="embedded-label">URL:</span> <code>{ep.base_url}</code></div>
									<div class="embedded-detail"><span class="embedded-label">Context:</span> {ep.context_tokens?.toLocaleString()} tokens</div>
									<div class="embedded-detail"><span class="embedded-label">Max Iterations:</span> {ep.max_iterations}</div>
									<div class="embedded-detail"><span class="embedded-label">Thinking:</span> {ep.thinking ? 'On' : 'Off'}</div>
								</div>
							</div>
						{/each}
					{/if}
				</div>
			{/if}

			<!-- Embedded endpoint editor modal -->
			{#if embeddedEditing}
				<div class="modal-overlay" onclick={closeEmbeddedEditor}>
					<div class="modal-card" onclick={(e) => e.stopPropagation()}>
						<h3>{embeddedEditing._existing ? 'Edit Endpoint' : 'Add Endpoint'}</h3>
						<div class="setting-row">
							<label>ID</label>
							<input class="input" type="text" bind:value={embeddedEditing.id} placeholder="local-qwen" disabled={!!embeddedEditing._existing} />
						</div>
						<div class="setting-row">
							<label>Label</label>
							<input class="input" type="text" bind:value={embeddedEditing.label} placeholder="Qwen3 27B" />
						</div>
						<div class="setting-row">
							<label>Base URL</label>
							<input class="input" type="text" bind:value={embeddedEditing.base_url} placeholder="http://127.0.0.1:8080/v1" />
						</div>
						<div class="setting-row">
							<label>API Key</label>
							<input class="input" type="password" bind:value={embeddedEditing.api_key} placeholder="Leave empty for local servers" />
						</div>
						<div class="setting-row">
							<label>Model</label>
							<input class="input" type="text" bind:value={embeddedEditing.model} placeholder="qwen3-27b" />
						</div>
						<div class="setting-row">
							<label>Max Iterations</label>
							<input class="input" type="number" bind:value={embeddedEditing.max_iterations} min="1" max="200" />
						</div>
						<div class="setting-row">
							<label>Context Tokens</label>
							<input class="input" type="number" bind:value={embeddedEditing.context_tokens} min="1024" max="131072" step="1024" />
						</div>
						<div class="setting-row">
							<label>Thinking</label>
							<label class="toggle">
								<input type="checkbox" bind:checked={embeddedEditing.thinking} />
								<span>{embeddedEditing.thinking ? 'On' : 'Off'}</span>
							</label>
						</div>
						<div class="setting-row">
							<label>Enabled</label>
							<label class="toggle">
								<input type="checkbox" bind:checked={embeddedEditing.enabled} />
								<span>{embeddedEditing.enabled ? 'Enabled' : 'Disabled'}</span>
							</label>
						</div>
						<div class="embedded-editor-actions">
							<button class="btn btn-primary" onclick={saveEmbeddedEndpoint}>Save</button>
							<button class="btn btn-outline" onclick={closeEmbeddedEditor}>Cancel</button>
						</div>
					</div>
				</div>
			{/if}

			<div class="setting-row readonly">
				<label>Server Host</label>
				<span class="mono">{configData.server_host}:{configData.server_port}</span>
			</div>

			<div class="setting-row readonly">
				<label>Workspace</label>
				<span class="mono">{configData.workspace_path || '(not set)'}</span>
			</div>

			<div class="setting-actions">
				<button class="btn btn-primary" onclick={save} disabled={saving}>
					{saving ? 'Saving...' : 'Save Settings'}
				</button>
				<button class="btn btn-restart" onclick={restart} disabled={restarting}>
					{restarting ? 'Restarting...' : 'Restart Server'}
				</button>
				{#if saveMsg}
					<span class="save-msg" class:error={saveMsg.startsWith('Error')}>{saveMsg}</span>
				{/if}
			</div>
		</div>

		<div class="preview-section card">
			<div class="preview-header">
				<h2 class="section-title">System Prompt Preview</h2>
				<button class="btn btn-sm btn-outline" onclick={togglePreview}>
					{previewOpen ? 'Hide' : 'Show'}
				</button>
			</div>
			<p class="preview-desc">
				Task 실행 시 Claude/OpenCode에 실제로 전달되는 시스템 프롬프트입니다. MCP 도구 리스트, 최근 작업 히스토리, 프로젝트 스킬 요약, 그리고 Custom Prompt가 포함됩니다. (Sheep별 히스토리·스킬은 Sheep 컨텍스트에서만 채워집니다.)
			</p>
			{#if previewOpen}
				{#if previewLoading}
					<p class="text-muted">Loading...</p>
				{:else if previewError}
					<p class="preview-error">{previewError}</p>
				{:else if preview}
					<div class="preview-tabs">
						<button class="preview-tab" class:active={previewMode === 'streaming'} onclick={() => previewMode = 'streaming'}>Streaming (Claude --append-system-prompt)</button>
						<button class="preview-tab" class:active={previewMode === 'withGuide'} onclick={() => previewMode = 'withGuide'}>With Guide (Claude Interactive)</button>
						<button class="preview-tab" class:active={previewMode === 'opencode'} onclick={() => previewMode = 'opencode'}>OpenCode (Actual)</button>
						<button class="preview-tab" class:active={previewMode === 'pi'} onclick={() => previewMode = 'pi'}>Pi</button>
						<button class="preview-tab" class:active={previewMode === 'compact'} onclick={() => previewMode = 'compact'}>Compact</button>
					</div>
					<pre class="preview-body">{preview[previewMode] || '(empty)'}</pre>
					<button class="btn btn-sm btn-outline" onclick={loadPreview}>Refresh</button>
				{/if}
			{/if}
		</div>

		<div class="mcp-section card">
			<h2 class="section-title">MCP Registration</h2>
			{#if !mcpLoaded}
				<p class="text-muted">Loading...</p>
			{:else}
				<div class="mcp-providers">
					{#each [
						{ key: 'claude', label: 'Claude Code' },
						{ key: 'opencode', label: 'OpenCode' },
						{ key: 'pi', label: 'Pi' }
					] as provider}
						{@const status = mcpStatus[provider.key]}
						<div class="mcp-provider">
							<div class="mcp-provider-info">
								<span class="mcp-provider-name">{provider.label}</span>
								{#if status?.registered}
									<span class="badge badge-success">Registered</span>
								{:else}
									<span class="badge badge-muted">Not Registered</span>
								{/if}
							</div>
							<div class="mcp-provider-path">{status?.config_path || ''}</div>
							{#if status?.error}
								<div class="mcp-provider-error">{status.error}</div>
							{/if}
							{#if !status?.registered}
								<button
									class="btn btn-sm"
									onclick={() => registerMCP(provider.key)}
									disabled={mcpRegistering[provider.key]}
								>
									{mcpRegistering[provider.key] ? 'Registering...' : 'Register'}
								</button>
							{/if}
						</div>
					{/each}
				</div>
				{#if mcpError}
					<div class="mcp-error">{mcpError}</div>
				{/if}
			{/if}
		</div>
		<div class="sync-section card">
			<h2 class="section-title">Skill Sync</h2>
			<p class="sync-desc">Sync all enabled skills to each project's <code>.claude/skills/</code> directory so Claude Code and OpenCode can use them natively with frontmatter (effort, maxTurns, etc).</p>
			<div class="sync-actions">
				<button class="btn btn-sm" onclick={syncSkills} disabled={syncing}>
					{syncing ? 'Syncing...' : 'Sync All Skills'}
				</button>
				{#if syncResult}
					<span class="sync-msg" class:error={syncResult.startsWith('Error')}>{syncResult}</span>
				{/if}
			</div>
		</div>

		<div class="backup-section card">
			<h2 class="section-title">Database Backup</h2>
			<p class="sync-desc">
				Download a consistent SQLite snapshot via <code>VACUUM INTO</code>. Same-machine restore: stop shepherd, replace <code>shepherd.db</code> with the downloaded file, then start it again.
			</p>
			<div class="sync-actions">
				<button class="btn btn-sm" onclick={downloadBackup} disabled={backupBusy}>
					{backupBusy ? 'Preparing...' : 'Download Backup'}
				</button>
				{#if backupMsg}
					<span class="sync-msg" class:error={backupMsg.startsWith('Error')}>{backupMsg}</span>
				{/if}
			</div>
		</div>

		<div class="backup-section card">
			<h2 class="section-title">Task History — Export</h2>
			<p class="sync-desc">
				Export task records as JSONL (one task per line). Project records are <strong>not</strong> included — paths are machine-specific. On the target machine, the receiving project must already exist with the same name.
			</p>
			<div class="setting-row">
				<label>Project</label>
				<select class="input" bind:value={exportProject}>
					<option value="">(All projects)</option>
					{#each projectsList as p}
						<option value={p.name}>{p.name}</option>
					{/each}
				</select>
			</div>
			<div class="sync-actions">
				<button class="btn btn-sm" onclick={exportTasks} disabled={exportBusy}>
					{exportBusy ? 'Preparing...' : 'Export Tasks'}
				</button>
				{#if exportMsg}
					<span class="sync-msg" class:error={exportMsg.startsWith('Error')}>{exportMsg}</span>
				{/if}
			</div>
		</div>

		<div class="backup-section card">
			<h2 class="section-title">Task History — Import</h2>
			<p class="sync-desc">
				Import a JSONL dump from another machine. Records are matched by <code>project_name</code>; tasks for projects that don't exist here are skipped. Re-importing the same dump is safe — duplicates are detected by (project, prompt, created_at).
			</p>
			<div class="import-controls">
				<input type="file" accept=".jsonl,.ndjson,application/x-ndjson,application/json" onchange={onImportFileChange} />
				<button class="btn btn-sm" onclick={previewImport} disabled={!importFile || importBusy}>
					{importBusy ? 'Working...' : 'Preview'}
				</button>
				{#if importPreview}
					<button class="btn btn-sm btn-restart" onclick={confirmImport} disabled={importBusy}>
						{importBusy ? 'Importing...' : 'Confirm Import'}
					</button>
				{/if}
			</div>
			{#if importMsg}
				<div class="sync-msg" class:error={importMsg.startsWith('Error')}>{importMsg}</div>
			{/if}
			{#if importPreview}
				<div class="import-preview">
					<div><strong>Total in file:</strong> {importPreview.total}</div>
					<div><strong>Will import:</strong> {importPreview.matched}</div>
					<div><strong>Will skip (no matching project):</strong> {importPreview.skipped}</div>
					{#if importPreview.matched_by_project && Object.keys(importPreview.matched_by_project).length > 0}
						<div class="preview-detail">
							<div class="preview-detail-title">Matched by project:</div>
							<ul>
								{#each Object.entries(importPreview.matched_by_project) as [name, count]}
									<li><code>{name}</code>: {count}</li>
								{/each}
							</ul>
						</div>
					{/if}
					{#if importPreview.skipped_by_project && Object.keys(importPreview.skipped_by_project).length > 0}
						<div class="preview-detail">
							<div class="preview-detail-title">Skipped (project not found here):</div>
							<ul>
								{#each Object.entries(importPreview.skipped_by_project) as [name, count]}
									<li><code>{name}</code>: {count}</li>
								{/each}
							</ul>
						</div>
					{/if}
				</div>
			{/if}
			{#if importResult}
				<div class="import-result">
					<div><strong>Imported:</strong> {importResult.imported}</div>
					<div><strong>Skipped:</strong> {importResult.skipped}</div>
					<div><strong>Duplicates:</strong> {importResult.duplicates}</div>
					{#if importResult.failed > 0}
						<div class="error-text"><strong>Failed:</strong> {importResult.failed}</div>
					{/if}
				</div>
			{/if}
		</div>
	{/if}
</div>

<style>
	.page-title { font-size: 20px; font-weight: 600; margin-bottom: 20px; }
	.text-muted { color: var(--text-secondary); }

	.settings-form {
		max-width: 640px;
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

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

	.setting-row.readonly {
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

	.setting-section-title {
		font-size: 14px;
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

	.toggle {
		display: flex;
		align-items: center;
		gap: 8px;
		cursor: pointer;
		font-weight: 400;
		min-width: 0;
	}

	.setting-actions {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-top: 8px;
		padding-top: 16px;
		border-top: 1px solid var(--border);
	}

	.save-msg {
		font-size: 13px;
		color: var(--success);
	}

	.save-msg.error {
		color: var(--danger);
	}

	.btn-restart {
		padding: 6px 16px;
		font-size: 13px;
		font-weight: 600;
		background: var(--bg-tertiary);
		color: var(--text-primary);
		border: 1px solid var(--border);
		border-radius: 6px;
		cursor: pointer;
		transition: background 0.15s, border-color 0.15s;
	}
	.btn-restart:hover {
		border-color: var(--accent);
		background: var(--bg-secondary);
	}
	.btn-restart:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.preview-section {
		max-width: 900px;
		margin-top: 24px;
	}
	.preview-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 8px;
	}
	.preview-desc {
		font-size: 13px;
		color: var(--text-secondary);
		line-height: 1.5;
		margin-bottom: 12px;
	}
	.preview-tabs {
		display: flex;
		gap: 4px;
		flex-wrap: wrap;
		margin-bottom: 8px;
		border-bottom: 1px solid var(--border);
	}
	.preview-tab {
		padding: 6px 12px;
		font-size: 12px;
		font-weight: 500;
		background: transparent;
		color: var(--text-secondary);
		border: none;
		border-bottom: 2px solid transparent;
		cursor: pointer;
		transition: color 0.15s, border-color 0.15s;
	}
	.preview-tab:hover {
		color: var(--text-primary);
	}
	.preview-tab.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
	}
	.preview-body {
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: 12px;
		font-family: monospace;
		font-size: 12px;
		line-height: 1.5;
		max-height: 480px;
		overflow: auto;
		white-space: pre-wrap;
		word-break: break-word;
		margin-bottom: 8px;
	}
	.preview-error {
		color: var(--danger);
		font-size: 13px;
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

	.mcp-section {
		max-width: 500px;
		margin-top: 24px;
	}

	.section-title {
		font-size: 16px;
		font-weight: 600;
		margin-bottom: 16px;
	}

	.mcp-providers {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.mcp-provider {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 12px;
		background: var(--bg-secondary);
		border-radius: 8px;
		border: 1px solid var(--border);
	}

	.mcp-provider-info {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.mcp-provider-name {
		font-size: 14px;
		font-weight: 600;
	}

	.mcp-provider-path {
		font-size: 12px;
		color: var(--text-secondary);
		font-family: monospace;
	}

	.mcp-provider-error {
		font-size: 12px;
		color: var(--danger);
	}

	.mcp-error {
		margin-top: 12px;
		font-size: 13px;
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

	.btn-sm {
		padding: 4px 12px;
		font-size: 12px;
		font-weight: 600;
		background: var(--accent);
		color: white;
		border: none;
		border-radius: 6px;
		cursor: pointer;
		align-self: flex-start;
		transition: opacity 0.15s;
	}
	.btn-sm:hover { opacity: 0.85; }
	.btn-sm:disabled { opacity: 0.5; cursor: not-allowed; }

	.sync-section {
		max-width: 500px;
		margin-top: 24px;
	}
	.sync-desc {
		font-size: 13px;
		color: var(--text-secondary);
		line-height: 1.5;
		margin-bottom: 12px;
	}
	.sync-desc code {
		background: var(--bg-tertiary, #2a2a2a);
		padding: 1px 5px;
		border-radius: 3px;
		font-size: 12px;
	}
	.sync-actions {
		display: flex;
		align-items: center;
		gap: 12px;
	}
	.sync-msg {
		font-size: 13px;
		color: var(--success);
	}
	.sync-msg.error {
		color: var(--danger);
	}

	.backup-section {
		max-width: 640px;
		margin-top: 24px;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.import-controls {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
		align-items: center;
	}

	.import-preview,
	.import-result {
		font-size: 13px;
		line-height: 1.6;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: 10px 12px;
	}

	.import-preview ul,
	.import-result ul {
		margin: 4px 0 0;
		padding-left: 18px;
	}

	.preview-detail {
		margin-top: 8px;
	}

	.preview-detail-title {
		font-weight: 600;
		font-size: 12px;
		color: var(--text-secondary);
	}

	.error-text {
		color: var(--danger);
	}

	@media (max-width: 768px) {
		.settings-form {
			max-width: none;
		}

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

		/* 모바일에서 input/select 높이 과다 문제 해결 */
		.setting-row .input,
		.setting-row select.input,
		.setting-row input.input,
		.setting-row input[type="number"].input,
		.setting-row input[type="text"].input {
			padding: 4px 8px !important;
			height: 34px !important;
			min-height: 34px !important;
			max-height: 34px !important;
			line-height: 1.3 !important;
			box-sizing: border-box !important;
		}

		.setting-row select.input {
			appearance: none !important;
			-webkit-appearance: none !important;
			background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%239aa4af' d='M6 8L1 3h10z'/%3E%3C/svg%3E") !important;
			background-repeat: no-repeat !important;
			background-position: right 8px center !important;
			padding-right: 28px !important;
		}

		.setting-row textarea.input {
			height: auto !important;
			min-height: auto !important;
			padding: 6px 8px !important;
		}

		.setting-row:not(.column) > .hint {
			flex: 0 0 100%;
			margin-left: 0;
		}

		.setting-actions {
			flex-wrap: wrap;
		}

		.mcp-section {
			max-width: none;
		}

		.backup-section {
			max-width: none;
		}
	}

	/* — Embedded provider section — */
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

	/* Modal overlay for embedded editor */
	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.5);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 1000;
	}
	.modal-card {
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: 12px;
		padding: 24px;
		width: 90%;
		max-width: 500px;
		max-height: 80vh;
		overflow-y: auto;
	}
	.modal-card h3 {
		margin: 0 0 16px;
		font-size: 16px;
	}
</style>
