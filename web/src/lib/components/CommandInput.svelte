<script>
	import { apiPost, apiGet, apiUpload } from '$lib/api.js';
	import Icon from './Icon.svelte';

	// `thinking`: tri-state — true/false override the server default, null means
	// "do not include the field" so the server applies opencode_thinking_default.
	// `model`: per-project model override for OpenCode. Empty string or null means
	// "use the global default".
	let { projectName = '', sheepName = '', sheepStatus = 'idle', hasRunningTask = false, thinking = null, model = null } = $props();

	let prompt = $state('');
	let loading = $state(false);
	let stopping = $state(false);
	let injectMode = $state(false);
	let lastResult = $state(null);
	let attachedFiles = $state([]);
	let fileInputEl = $state(null);
	let fieldEl = $state(null);

	// Show Stop/Inject when the sheep reports working OR a task is still marked
	// running — the latter covers the desync where sheepStatus drops to 'idle'
	// while a task is genuinely running (server restart, missed SSE event).
	let isWorking = $derived(sheepStatus === 'working' || hasRunningTask);

	// A textarea placeholder can't ellipsize, so phones get a shorter hint.
	let compact = $state(false);
	$effect(() => {
		const mq = window.matchMedia('(max-width: 640px)');
		compact = mq.matches;
		const onChange = (e) => (compact = e.matches);
		mq.addEventListener('change', onChange);
		return () => mq.removeEventListener('change', onChange);
	});

	let placeholder = $derived.by(() => {
		if (injectMode) return '추가 지시를 입력하세요...';
		if (projectName) return compact ? 'Send a task…' : `Send a task to ${projectName}…`;
		return 'Send a task…';
	});
	let hasAttachments = $derived(attachedFiles.length > 0);
	// A task needs both a project and a sheep; otherwise sending is disabled.
	let canSend = $derived(!!projectName && !!sheepName);

	function openFilePicker() {
		fileInputEl?.click();
	}

	function handleFileSelect(e) {
		const files = Array.from(e.target.files || []);
		for (const f of files) {
			if (f.size > 10 * 1024 * 1024) {
				lastResult = { success: false, message: `${f.name} exceeds 10MB limit` };
				continue;
			}
			attachedFiles = [...attachedFiles, { file: f, preview: null }];
			if (f.type.startsWith('image/')) {
				const idx = attachedFiles.length - 1;
				const reader = new FileReader();
				reader.onload = (ev) => {
					attachedFiles = attachedFiles.map((a, i) =>
						i === idx ? { ...a, preview: ev.target.result } : a
					);
				};
				reader.readAsDataURL(f);
			}
		}
		// Reset input so same file can be re-selected
		e.target.value = '';
	}

	function handlePaste(e) {
		if (!projectName) return;
		const items = e.clipboardData?.items;
		if (!items) return;

		for (const item of items) {
			if (item.type.startsWith('image/')) {
				e.preventDefault();
				const blob = item.getAsFile();
				if (!blob) continue;
				const ext = blob.type.split('/')[1] || 'png';
				const file = new File([blob], `clipboard-${Date.now()}.${ext}`, { type: blob.type });
				const entry = { file, preview: null };
				attachedFiles = [...attachedFiles, entry];
				const idx = attachedFiles.length - 1;
				const reader = new FileReader();
				reader.onload = (ev) => {
					attachedFiles = attachedFiles.map((a, i) =>
						i === idx ? { ...a, preview: ev.target.result } : a
					);
				};
				reader.readAsDataURL(file);
				break;
			}
		}
	}

	function removeFile(index) {
		attachedFiles = attachedFiles.filter((_, i) => i !== index);
	}

	function formatSize(bytes) {
		if (bytes < 1024) return bytes + 'B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + 'KB';
		return (bytes / (1024 * 1024)).toFixed(1) + 'MB';
	}

	async function handleKeydown(e) {
		if (e.key !== 'Enter') return;
		if (e.shiftKey) return; // Shift+Enter = newline
		e.preventDefault();
		await submit();
	}

	function autosize() {
		if (!fieldEl) return;
		fieldEl.style.height = 'auto';
		fieldEl.style.height = Math.min(fieldEl.scrollHeight, 160) + 'px';
	}

	async function submit() {
		if (loading) return;
		// Without a project and sheep there is no task target; the shepherd
		// console (/shepherd) handles free-form instructions instead.
		if (!projectName || !sheepName) return;
		if (!prompt.trim() && !hasAttachments) return;

		// Inject mode: send via inject API instead of creating a new task
		if (injectMode && projectName && sheepName) {
			await handleInject();
			return;
		}

		loading = true;
		lastResult = null;
		const text = prompt;
		prompt = '';
		// Reset textarea height
		if (fieldEl) fieldEl.style.height = 'auto';
		const filesToUpload = [...attachedFiles];
		attachedFiles = [];
		injectMode = false;

		try {
			let finalPrompt = text;

			// Upload files first if any
			if (filesToUpload.length > 0 && projectName) {
				const formData = new FormData();
				formData.append('project_name', projectName);
				for (const a of filesToUpload) {
					formData.append('files', a.file);
				}
				const uploadRes = await apiUpload('/api/upload', formData);
				if (!uploadRes?.success) {
					lastResult = { success: false, message: uploadRes?.message || 'Upload failed' };
					loading = false;
					return;
				}
				const paths = uploadRes.data.files.map(f => f.path);
				const attachBlock = '[Attached files]\n' + paths.map(p => '- ' + p).join('\n') + '\n\n';
				finalPrompt = attachBlock + text;
			}

			let result;
			const taskBody = {
				prompt: finalPrompt,
				sheep_name: sheepName,
				project_name: projectName
			};
			if (thinking !== null) {
				taskBody.thinking = !!thinking;
			}
			if (model) {
				taskBody.model = model;
			}
			result = await apiPost('/api/tasks', taskBody);
			lastResult = result;
		} catch (err) {
			lastResult = { success: false, message: err.message || 'Request failed' };
		} finally {
			loading = false;
		}
	}

	async function handleInject() {
		const text = prompt.trim();
		if (!text || loading || stopping) return;

		loading = true;
		lastResult = null;
		const savedPrompt = text;
		prompt = '';
		if (fieldEl) fieldEl.style.height = 'auto';
		injectMode = false;

		try {
			const res = await apiPost(`/api/sheep/${encodeURIComponent(sheepName)}/inject`, { prompt: savedPrompt });
			if (res?.success) {
				lastResult = { success: true, data: {}, message: '💬 주입됨' };
			} else {
				lastResult = { success: false, message: res?.message || '주입에 실패했습니다' };
			}
		} catch (err) {
			lastResult = { success: false, message: err.message || '주입 요청 실패' };
		} finally {
			loading = false;
		}
	}

	async function handleStop() {
		if (stopping || !projectName) return;
		stopping = true;
		lastResult = null;

		try {
			const tasksRes = await apiGet(`/api/tasks?project=${encodeURIComponent(projectName)}&status=running&limit=1`);
			const runningTask = tasksRes?.data?.[0];
			if (!runningTask) {
				lastResult = { success: false, message: 'No running task found' };
				return;
			}
			const res = await apiPost(`/api/tasks/${runningTask.id}/stop`, {});
			if (res?.success) {
				lastResult = { success: true, data: {}, message: `Task #${runningTask.id} stopped` };
			} else {
				lastResult = { success: false, message: res?.message || 'Failed to stop task' };
			}
		} catch (err) {
			lastResult = { success: false, message: err.message || 'Stop request failed' };
		} finally {
			stopping = false;
		}
	}
</script>

<!-- Attachment previews above input -->
{#if hasAttachments}
	<div class="attachments-bar">
		{#each attachedFiles as a, i}
			<div class="attachment-chip">
				{#if a.preview}
					<img src={a.preview} alt={a.file.name} class="attachment-thumb" />
				{:else}
					<span class="attachment-file-icon"><Icon name="file-text" size={14} /></span>
				{/if}
				<span class="attachment-name">{a.file.name}</span>
				<span class="attachment-size mono">{formatSize(a.file.size)}</span>
				<button class="attachment-remove" onclick={() => removeFile(i)} title="Remove" aria-label="Remove {a.file.name}">
					<Icon name="x" size={12} />
				</button>
			</div>
		{/each}
	</div>
{/if}

<div class="command-input" class:inject={injectMode} class:busy={loading}>
	{#if projectName}
		<button class="btn-attach" onclick={openFilePicker} title="Attach file" aria-label="Attach file" disabled={loading}>
			<Icon name="paperclip" size={17} stroke={1.8} />
		</button>
		<input
			type="file"
			multiple
			class="file-input-hidden"
			bind:this={fileInputEl}
			onchange={handleFileSelect}
			accept="image/*,.txt,.md,.json,.yaml,.yml,.csv,.log,.py,.js,.go,.rs,.java,.c,.cpp,.h,.ts,.svelte,.html,.css,.sql,.sh,.toml,.xml,.pdf"
		/>
	{:else}
		<span class="prompt-glyph" aria-hidden="true">
			<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="7 6 13 12 7 18"/><line x1="15" y1="18" x2="19" y2="18"/></svg>
		</span>
	{/if}
	<textarea
		class="command-field"
		bind:this={fieldEl}
		bind:value={prompt}
		onkeydown={handleKeydown}
		onpaste={handlePaste}
		oninput={autosize}
		{placeholder}
		aria-label={injectMode ? '추가 지시' : 'Command'}
		disabled={loading}
		rows="1"
	></textarea>
	<div class="command-actions">
		{#if !prompt && !loading && !isWorking}
			<span class="hint" aria-hidden="true"><kbd>↵</kbd> send <kbd>⇧↵</kbd> newline</span>
		{/if}
		{#if isWorking}
			<button
				class="btn btn-sm btn-live btn-inject"
				class:active={injectMode}
				onclick={() => { injectMode = !injectMode; fieldEl?.focus(); }}
				disabled={loading || stopping}
				aria-pressed={injectMode}
				title="추가 지시 주입"
			>
				<Icon name="message-square" size={13} />
				주입
			</button>
			<button class="btn btn-sm btn-danger btn-stop" onclick={handleStop} disabled={stopping} title="Stop the running task">
				<Icon name="square" size={11} stroke={2.6} />
				{stopping ? 'Stopping…' : 'Stop'}
			</button>
		{/if}
		<button
			class="send"
			class:ready={canSend && (!!prompt.trim() || hasAttachments)}
			onclick={submit}
			disabled={loading || !canSend || (!prompt.trim() && !hasAttachments)}
			aria-label={injectMode ? 'Send instruction' : 'Send'}
			title={injectMode ? 'Send instruction' : 'Send (Enter)'}
		>
			{#if loading}
				<span class="spinner" aria-hidden="true"></span>
			{:else}
				<Icon name="arrow-up-right" size={16} stroke={2.2} />
			{/if}
		</button>
	</div>
</div>
{#if lastResult?.message && !lastResult?.success}
	<p class="command-status error" role="alert">{lastResult.message}</p>
{:else if lastResult?.success && lastResult?.message}
	<p class="command-status success" role="status">{lastResult.message}</p>
{/if}

<style>
	.file-input-hidden {
		display: none;
	}

	.command-input {
		display: flex;
		align-items: flex-end;
		gap: 6px;
		min-height: 48px;
		padding: 6px 6px 6px 8px;
		border-radius: 12px;
		background: var(--bg-input);
		box-shadow: inset 0 0 0 1px var(--border-subtle), inset 0 1px 3px rgba(0, 0, 0, 0.4);
		transition: box-shadow var(--dur-2) ease;
	}

	.command-input.inject {
		box-shadow: inset 0 0 0 1px var(--live-line), inset 0 1px 3px rgba(0, 0, 0, 0.4), 0 0 0 3px var(--live-soft);
	}

	.prompt-glyph,
	.btn-attach {
		flex-shrink: 0;
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		color: var(--accent);
	}

	.btn-attach {
		border: none;
		border-radius: 9px;
		background: none;
		color: var(--text-tertiary);
		cursor: pointer;
		transition: color var(--dur-1) ease, background var(--dur-1) ease;
	}

	.btn-attach:hover {
		color: var(--accent);
		background: var(--bg-hover);
	}

	.btn-attach:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}

	.command-field {
		flex: 1;
		min-width: 0;
		min-height: 36px;
		max-height: 160px;
		padding: 8px 4px;
		border: none;
		border-radius: 0;
		background: transparent;
		box-shadow: none;
		color: var(--text-primary);
		font-family: var(--font-sans);
		font-size: 15px;
		line-height: 20px;
		letter-spacing: -0.005em;
		resize: none;
		overflow-y: auto;
		outline: none;
	}

	.command-field:hover,
	.command-field:focus {
		border: none;
		box-shadow: none;
	}

	.command-field::placeholder {
		color: var(--text-tertiary);
	}

	/* Keep the hint on one line — a wrapped hint is clipped by the one-row
	   field. Only applies while empty, so typed text still wraps. */
	.command-field:placeholder-shown {
		white-space: nowrap;
		overflow: hidden;
	}

	.command-field:disabled {
		opacity: 0.6;
	}

	.command-actions {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		gap: 6px;
		height: 36px;
	}

	.hint {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		margin-right: 6px;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.hint kbd + kbd,
	.hint kbd:nth-of-type(2) {
		margin-left: 6px;
	}

	.btn-inject.active {
		background: var(--live);
		border-color: var(--live);
		color: var(--live-ink);
	}

	.send {
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border: 1px solid var(--border);
		border-radius: 10px;
		background: var(--bg-3);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: background var(--dur-2) ease, color var(--dur-1) ease, border-color var(--dur-1) ease, box-shadow var(--dur-2) ease, transform var(--dur-1) ease;
	}

	.send.ready {
		border-color: rgba(255, 214, 150, 0.6);
		background: linear-gradient(180deg, #ffc977, var(--accent));
		color: var(--accent-ink);
		box-shadow: inset 0 1px 0 rgba(255, 244, 220, 0.55), 0 6px 18px -8px var(--accent-glow);
	}

	.inject .send.ready {
		border-color: var(--live);
		background: var(--live);
		color: var(--live-ink);
		box-shadow: 0 6px 18px -8px var(--live-glow);
	}

	.send.ready:hover {
		transform: translateY(-1px);
	}

	.send:disabled {
		cursor: default;
	}

	.send:disabled:not(.ready) {
		opacity: 0.7;
	}

	.spinner {
		width: 14px;
		height: 14px;
		border-radius: 50%;
		border: 2px solid rgba(26, 18, 6, 0.25);
		border-top-color: currentColor;
		animation: spin 700ms linear infinite;
	}

	.command-status {
		margin: 8px 4px 0;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.command-status.success {
		color: var(--success);
	}

	.command-status.error {
		color: var(--danger);
	}

	/* Attachments */
	.attachments-bar {
		display: flex;
		gap: 6px;
		flex-wrap: wrap;
		margin-bottom: 8px;
	}

	.attachment-chip {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 6px 4px 4px;
		border: 1px solid var(--border);
		border-radius: 9px;
		background: var(--bg-3);
		font-size: var(--fs-2xs);
		animation: rise-in var(--dur-2) var(--ease-out) backwards;
	}

	.attachment-thumb {
		width: 26px;
		height: 26px;
		object-fit: cover;
		border-radius: 6px;
	}

	.attachment-file-icon {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border-radius: 6px;
		background: var(--bg-4);
		color: var(--text-secondary);
	}

	.attachment-name {
		max-width: 140px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--text-primary);
	}

	.attachment-size {
		color: var(--text-tertiary);
	}

	.attachment-remove {
		display: grid;
		place-items: center;
		width: 18px;
		height: 18px;
		border: none;
		border-radius: 50%;
		background: none;
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.attachment-remove:hover {
		color: var(--danger);
		background: var(--danger-soft);
	}

	@media (max-width: 768px) {
		.command-field {
			font-size: 16px;
		}

		.hint {
			display: none;
		}

		.btn-attach,
		.send {
			width: 40px;
			height: 40px;
		}

		.command-actions {
			height: 40px;
		}

		.attachment-thumb {
			width: 32px;
			height: 32px;
		}
	}
</style>
