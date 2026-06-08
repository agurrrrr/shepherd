<script>
	/** @type {{ language: string, default_provider: string, max_sheep: number, max_concurrent_tasks: number, concurrency_limits: Record<string, number>, task_timeout: string, auto_approve: boolean, enable_file_browser: boolean, session_reuse: boolean, include_task_history: boolean, include_mcp_guide: boolean, include_sheep_memory: boolean, sheep_memory_prompt: string, wiki_enabled: boolean, wiki_auto_ingest: boolean, wiki_max_context_pages: number, wiki_max_page_content_chars: number, discord_notifications_enabled: boolean, discord_webhook_url: string, discord_notify_on_complete: boolean, discord_notify_on_fail: boolean, server_host: string, server_port: number, workspace_path: string, model_claude: string }} */
	export let configData;
</script>

<!-- Language -->
<div class="setting-row">
	<label>Language</label>
	<select class="input" bind:value={configData.language}>
		<option value="ko">한국어</option>
		<option value="en">English</option>
	</select>
</div>

<!-- Default Provider -->
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

<p class="hint">각 Provider의 사용유무 · 모델 · Custom Prompt · 동시 실행 제한은 위쪽의 해당 Provider 탭에서 설정합니다.</p>

<!-- Max Sheep -->
<div class="setting-row">
	<label>Max Sheep</label>
	<input class="input" type="number" bind:value={configData.max_sheep} min="1" max="50" />
</div>

<!-- Max Concurrent Tasks -->
<div class="setting-row">
	<label>Max Concurrent Tasks</label>
	<input class="input" type="number" bind:value={configData.max_concurrent_tasks} min="0" max="50" />
	<span class="hint">전체 동시 실행 작업 수의 천장(ceiling). 0이면 제한 없음.</span>
</div>

<!-- Task Timeout -->
<div class="setting-row">
	<label>Task Timeout</label>
	<input class="input" type="text" bind:value={configData.task_timeout} placeholder="4h" />
	<span class="hint">Per-task execution cap (e.g. <code>30m</code>, <code>4h</code>). <code>unlimited</code> or <code>0</code> to disable. Default: 4h.</span>
</div>

<!-- Auto Approve -->
<div class="setting-row">
	<label>Auto Approve</label>
	<label class="toggle">
		<input type="checkbox" bind:checked={configData.auto_approve} />
		<span>{configData.auto_approve ? 'Enabled' : 'Disabled'}</span>
	</label>
</div>

<!-- File Browser -->
<div class="setting-row">
	<label>File Browser</label>
	<label class="toggle">
		<input type="checkbox" bind:checked={configData.enable_file_browser} />
		<span>{configData.enable_file_browser ? 'Enabled' : 'Disabled'}</span>
	</label>
</div>

<!-- Prompt Injection -->
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
	<span class="hint">양 이름 단위로 <code>~/.shepherd/sheep/&lt;name&gt;/</code> 에 누적되는 개인 기억.</span>
</div>

<div class="setting-row column">
	<label>Sheep Memory — System Prompt</label>
	<textarea
		class="input textarea"
		bind:value={configData.sheep_memory_prompt}
		rows="10"
		placeholder={`양에게 전달할 메모리 가이드라인. {{.MemoryDir}} 가 실제 디렉토리 경로로 치환됩니다.`}
	></textarea>
	<span class="hint"><code>{`{{.MemoryDir}}`}</code> 토큰은 작업 시점에 양의 실제 메모리 디렉토리 경로로 치환됩니다.</span>
</div>

<!-- Wiki -->
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

<!-- Discord Notifications -->
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
	<input class="input" type="text" bind:value={configData.discord_webhook_url} placeholder="https://discord.com/api/webhooks/..." />
	<span class="hint">Discord Incoming Webhook URL을 입력하세요.</span>
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

<!-- Server Info -->
<hr class="setting-divider" />
<div class="setting-row readonly">
	<label>Server Host</label>
	<span class="mono">{configData.server_host}:{configData.server_port}</span>
</div>
<div class="setting-row readonly">
	<label>Workspace</label>
	<span class="mono">{configData.workspace_path || '(not set)'}</span>
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
