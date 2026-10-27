<script>
	import { apiGet } from '$lib/api.js';
	import { t } from '$lib/i18n/index.js';

	/** @type {'claude' | 'opencode' | 'pi' | 'grok' | 'embedded' | 'magi'} */
	export let provider = 'claude';
	/** Sheep to preview ("shepherd" renders the 목자 prompt, #595). Empty = no sheep context. */
	export let sheep = '';
	/** Optional header override, e.g. "목자" for the shepherd settings tab. */
	export let title = '';

	// 프로바이더별로 노출할 프리뷰 모드 집합
	const MODE_SETS = {
		claude: [
			{ key: 'streaming', label: 'Streaming (--append-system-prompt)' },
			{ key: 'withGuide', label: 'With Guide (Interactive)' }
		],
		opencode: [
			{ key: 'opencode', label: 'OpenCode (Actual)' },
			{ key: 'compact', label: 'Compact' }
		],
		pi: [{ key: 'pi', label: 'Pi' }],
		grok: [{ key: 'grok', label: 'Grok' }],
		embedded: [{ key: 'embedded', label: 'Embedded (system prompt)' }],
		magi: [{ key: 'magi', label: 'MAGI (proposer system prompt)' }]
	};

	$: modes = MODE_SETS[provider] || MODE_SETS.claude;
	$: if (modes && !modes.some((m) => m.key === mode)) mode = modes[0].key;

	let preview = null;
	let loading = false;
	let error = '';
	let mode = (MODE_SETS[provider] || MODE_SETS.claude)[0].key;
	let open = false;

	async function loadPreview() {
		loading = true;
		error = '';
		const q = sheep ? `?sheep=${encodeURIComponent(sheep)}` : '';
		const res = await apiGet('/api/config/system-prompt-preview' + q);
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

	// The 목자 tab previews the manager's actual prompt: the API renders the
	// orchestrator block when sheep=shepherd (#595).
	$: header = title
		? `System Prompt Preview — ${title} (${provider.charAt(0).toUpperCase() + provider.slice(1)})`
		: `System Prompt Preview — ${provider.charAt(0).toUpperCase() + provider.slice(1)}`;
</script>

<div class="preview-section card">
	<div class="preview-header">
		<h2 class="section-title">{header}</h2>
		<button class="btn btn-sm btn-outline" onclick={toggle}>
			{open ? 'Hide' : 'Show'}
		</button>
	</div>
	<p class="preview-desc">
		{$t('preview.desc')}
		{#if sheep === 'shepherd'}{$t('preview.shepherdNote')}{/if}
	</p>
	{#if open}
		{#if loading}
			<p class="text-muted">Loading...</p>
		{:else if error}
			<p class="preview-error">{error}</p>
		{:else if preview}
			<div class="preview-tabs">
				{#each modes as m}
					<button class="preview-tab" class:active={mode === m.key} onclick={() => mode = m.key}>{m.label}</button>
				{/each}
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
		font-size: var(--fs-sm);
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
		font-size: var(--fs-xs);
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
		border-radius: 0;
		padding: 12px;
		font-family: monospace;
		font-size: var(--fs-xs);
		line-height: 1.5;
		max-height: 480px;
		overflow: auto;
		white-space: pre-wrap;
		word-break: break-word;
		margin-bottom: 8px;
	}
	.preview-error {
		color: var(--danger-text);
		font-size: var(--fs-sm);
	}
	.section-title {
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		font-weight: 400;
		letter-spacing: 0;
		margin-bottom: 12px;
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
</style>
