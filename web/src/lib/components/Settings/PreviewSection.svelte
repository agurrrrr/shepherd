<script>
	import { apiGet } from '$lib/api.js';

	let preview = null;
	let loading = false;
	let error = '';
	let mode = 'streaming'; // streaming | compact | withGuide | opencode | pi
	let open = false;

	async function loadPreview() {
		loading = true;
		error = '';
		const res = await apiGet('/api/config/system-prompt-preview');
		if (res?.data) {
			preview = res.data;
		} else {
			error = res?.message || 'Failed to load preview';
		}
		loading = false;
	}

	async function toggle() {
		open = !open;
		if (open && !preview) {
			await loadPreview();
		}
	}
</script>

<div class="preview-section card">
	<div class="preview-header">
		<h2 class="section-title">System Prompt Preview</h2>
		<button class="btn btn-sm btn-outline" onclick={toggle}>
			{open ? 'Hide' : 'Show'}
		</button>
	</div>
	<p class="preview-desc">
		Task 실행 시 Claude/OpenCode에 실제로 전달되는 시스템 프롬프트입니다. MCP 도구 리스트, 최근 작업 히스토리, 프로젝트 스킬 요약, 그리고 Custom Prompt가 포함됩니다. (Sheep별 히스토리·스킬은 Sheep 컨텍스트에서만 채워집니다.)
	</p>
	{#if open}
		{#if loading}
			<p class="text-muted">Loading...</p>
		{:else if error}
			<p class="preview-error">{error}</p>
		{:else if preview}
			<div class="preview-tabs">
				<button class="preview-tab" class:active={mode === 'streaming'} onclick={() => mode = 'streaming'}>Streaming (Claude --append-system-prompt)</button>
				<button class="preview-tab" class:active={mode === 'withGuide'} onclick={() => mode = 'withGuide'}>With Guide (Claude Interactive)</button>
				<button class="preview-tab" class:active={mode === 'opencode'} onclick={() => mode = 'opencode'}>OpenCode (Actual)</button>
				<button class="preview-tab" class:active={mode === 'pi'} onclick={() => mode = 'pi'}>Pi</button>
				<button class="preview-tab" class:active={mode === 'compact'} onclick={() => mode = 'compact'}>Compact</button>
			</div>
			<pre class="preview-body">{preview[mode] || '(empty)'}</pre>
			<button class="btn btn-sm btn-outline" onclick={loadPreview}>Refresh</button>
		{/if}
	{/if}
</div>

<style>
	.text-muted { color: var(--text-secondary); }
	.preview-section { max-width: 900px; }
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
	.preview-tab:hover { color: var(--text-primary); }
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
	.section-title {
		font-size: 16px;
		font-weight: 600;
		margin-bottom: 16px;
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
</style>
