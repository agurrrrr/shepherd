<script>
	import { apiPost, apiGet, apiUpload } from '$lib/api.js';

	let { projectName = '', sheepName = '', sheepStatus = 'idle' } = $props();

	let prompt = $state('');
	let loading = $state(false);
	let stopping = $state(false);
	let lastResult = $state(null);
	let attachedFiles = $state([]);
	let fileInputEl = $state(null);

	let isWorking = $derived(sheepStatus === 'working');
	let hasAttachments = $derived(attachedFiles.length > 0);

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
		if (!prompt.trim() && !hasAttachments) return;

		loading = true;
		lastResult = null;
		const text = prompt;
		prompt = '';
		// Reset textarea height
		const ta = e.target;
		if (ta) ta.style.height = 'auto';
		const filesToUpload = [...attachedFiles];
		attachedFiles = [];

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
			if (projectName && sheepName) {
				result = await apiPost('/api/tasks', {
					prompt: finalPrompt,
					sheep_name: sheepName,
					project_name: projectName
				});
			} else {
				result = await apiPost('/api/command', { prompt: finalPrompt });
			}
			lastResult = result;
		} catch (err) {
			lastResult = { success: false, message: err.message || 'Request failed' };
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
					<span class="attachment-file-icon">📄</span>
				{/if}
				<span class="attachment-name">{a.file.name}</span>
				<span class="attachment-size">{formatSize(a.file.size)}</span>
				<button class="attachment-remove" onclick={() => removeFile(i)} title="Remove">&times;</button>
			</div>
		{/each}
	</div>
{/if}

<div class="command-input">
	{#if projectName}
		<button class="btn-attach" onclick={openFilePicker} title="Attach file" disabled={loading}>
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" width="18" height="18">
				<path d="M21.44 11.05l-9.19 9.19a6 6 0 01-8.49-8.49l9.19-9.19a4 4 0 015.66 5.66l-9.2 9.19a2 2 0 01-2.83-2.83l8.49-8.48" />
			</svg>
		</button>
		<input
			type="file"
			multiple
			class="file-input-hidden"
			bind:this={fileInputEl}
			onchange={handleFileSelect}
			accept="image/*,.txt,.md,.json,.yaml,.yml,.csv,.log,.py,.js,.go,.rs,.java,.c,.cpp,.h,.ts,.svelte,.html,.css,.sql,.sh,.toml,.xml,.pdf"
		/>
	{/if}
	<span class="prompt-icon">🐑</span>
	<textarea
		class="input command-field"
		bind:value={prompt}
		onkeydown={handleKeydown}
		onpaste={handlePaste}
		placeholder={projectName ? `Send a task to ${projectName}...` : 'Enter a command...'}
		disabled={loading}
		rows="1"
		oninput={(e) => { e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 120) + 'px'; }}
	></textarea>
	{#if isWorking}
		<button class="btn btn-stop" onclick={handleStop} disabled={stopping}>
			{stopping ? '...' : 'Stop'}
		</button>
	{/if}
	{#if loading}
		<span class="command-status">Processing...</span>
	{/if}
	{#if lastResult?.message && lastResult?.success}
		<span class="command-status success">{lastResult.message}</span>
	{:else if lastResult?.data && !lastResult?.message}
		<span class="command-status success">
			Task #{lastResult.data.task_id || lastResult.data.id} created
			{#if !projectName}
				({lastResult.data.sheep_name} / {lastResult.data.project_name})
			{/if}
		</span>
	{/if}
	{#if lastResult?.message && !lastResult?.success}
		<span class="command-status error">{lastResult.message}</span>
	{/if}
</div>

<style>
	.file-input-hidden {
		display: none;
	}
	.btn-attach {
		flex-shrink: 0;
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		padding: 4px;
		border-radius: 4px;
		display: flex;
		align-items: center;
		transition: color 0.15s, background 0.15s;
	}
	.btn-attach:hover {
		color: var(--accent);
		background: var(--bg-tertiary);
	}
	.btn-attach:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
	.attachments-bar {
		display: flex;
		gap: 6px;
		flex-wrap: wrap;
		margin-bottom: 6px;
	}
	.attachment-chip {
		display: flex;
		align-items: center;
		gap: 4px;
		background: var(--bg-tertiary);
		border: 1px solid var(--border);
		border-radius: 6px;
		padding: 3px 6px;
		font-size: 11px;
	}
	.attachment-thumb {
		width: 24px;
		height: 24px;
		object-fit: cover;
		border-radius: 3px;
	}
	.attachment-file-icon {
		font-size: 14px;
	}
	.attachment-name {
		max-width: 120px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--text-primary);
	}
	.attachment-size {
		color: var(--text-secondary);
	}
	.attachment-remove {
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		font-size: 14px;
		line-height: 1;
		padding: 0 2px;
	}
	.attachment-remove:hover {
		color: var(--danger);
	}
	.command-input {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}
	.prompt-icon {
		font-size: 20px;
	}
	.command-field {
		flex: 1;
		min-width: 200px;
		font-family: var(--font-mono);
		resize: none;
		overflow-y: hidden;
		line-height: 1.4;
	}
	.command-status {
		font-size: 12px;
		color: var(--text-secondary);
	}
	.command-status.success {
		color: var(--success);
	}
	.command-status.error {
		color: var(--danger);
	}
	.btn-stop {
		flex-shrink: 0;
		padding: 4px 12px;
		font-size: 12px;
		font-weight: 600;
		background: var(--danger, #e53e3e);
		color: #fff;
		border: none;
		border-radius: 4px;
		cursor: pointer;
		transition: opacity 0.15s;
	}
	.btn-stop:hover {
		opacity: 0.85;
	}
	.btn-stop:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	@media (max-width: 768px) {
		.command-field {
			min-width: 0;
			font-size: 16px;
		}

		.btn-attach {
			padding: 8px;
			min-width: 44px;
			min-height: 44px;
			display: flex;
			align-items: center;
			justify-content: center;
		}

		.attachment-thumb {
			width: 32px;
			height: 32px;
		}

		.btn-stop {
			padding: 8px 14px;
			font-size: 13px;
		}
	}
</style>
