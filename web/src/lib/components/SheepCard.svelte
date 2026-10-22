<script>
	import StatusBadge from './StatusBadge.svelte';
	import OutputViewer from './OutputViewer.svelte';
	import { onMount, onDestroy } from 'svelte';
	import { onSSE } from '$lib/sse.js';
	import { apiPost } from '$lib/api.js';
	import Icon from './Icon.svelte';

	let { name = '', project = '', status: initialStatus = 'idle', provider = 'claude' } = $props();

	// Svelte 5 runes mode doesn't allow mutating props directly, so we keep a local reactive copy.
	let status = $state(initialStatus);

	let output = $state([]);
	let unsubscribers = [];
	let injectText = $state('');
	let injecting = $state(false);
	let injectError = $state('');

	onMount(() => {
		unsubscribers.push(onSSE('output', (data) => {
			if (data.sheep_name === name || data.project_name === project) {
				output = [...output.slice(-100), data.text];
			}
		}));

		unsubscribers.push(onSSE('status_change', (data) => {
			if (data.sheep_name === name) {
				status = data.status;
			}
		}));
	});

	onDestroy(() => {
		unsubscribers.forEach(fn => fn());
	});

	async function handleInject() {
		const text = injectText.trim();
		if (!text || injecting) return;

		injecting = true;
		injectError = '';

		try {
			const res = await apiPost(`/api/sheep/${encodeURIComponent(name)}/inject`, { prompt: text });
			if (res?.success) {
				// Echo the injected prompt into output for visibility
				output = [...output.slice(-100), `💬 [주입된 메시지]: ${text}`];
				injectText = '';
			} else {
				injectError = res?.message || '주입에 실패했습니다';
			}
		} catch (e) {
			injectError = '네트워크 오류';
		}

		injecting = false;
	}

	function handleKeydown(e) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleInject();
		}
	}
</script>

<div class="sheep-card card" class:working={status === 'working'}>
	{#if status === 'working'}
		<div class="progress-bar" aria-hidden="true"></div>
	{/if}
	<div class="card-header">
		<div class="card-title">
			{#if project}
				<a class="project-name" href="/projects/{encodeURIComponent(project)}">{project}</a>
			{:else}
				<span class="project-name">{name}</span>
			{/if}
			<span class="sheep-meta">
				{#if project}<span class="sheep-name">{name}</span><span class="sep">·</span>{/if}
				<span class="provider">{provider}</span>
			</span>
		</div>
		<StatusBadge {status} />
	</div>

	{#if status === 'working' && output.length === 0}
		<div class="last-log mono">
			<span class="caret" aria-hidden="true">›</span>
			<span class="last-log-text">Waiting for output…</span>
		</div>
	{/if}

	{#if output.length > 0}
		<div class="card-output">
			<OutputViewer lines={output} maxLines={50} />
		</div>
	{/if}

	<!-- Prompt injection section — visible only when working -->
	{#if status === 'working'}
		<div class="inject-section">
			{#if injectError}
				<div class="inject-error" role="alert">{injectError}</div>
			{/if}
			<div class="inject-row">
				<span class="inject-icon" aria-hidden="true"><Icon name="message-square" size={14} /></span>
				<textarea
					bind:value={injectText}
					onkeydown={handleKeydown}
					placeholder="추가 지시 (Enter로 전송)"
					aria-label="추가 지시"
					class="inject-input"
					rows={1}
					disabled={injecting}
				></textarea>
				<button
					class="inject-btn"
					class:ready={!!injectText.trim()}
					onclick={handleInject}
					disabled={injecting || !injectText.trim()}
					title="주입하기"
					aria-label="주입하기"
				>
					{#if injecting}
						<span class="spinner" aria-hidden="true"></span>
					{:else}
						<Icon name="arrow-up-right" size={14} stroke={2.2} />
					{/if}
				</button>
			</div>
		</div>
	{/if}
</div>

<style>
	.sheep-card {
		position: relative;
		overflow: hidden;
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 18px 18px 14px;
		transition: border-color var(--dur-3) ease, background var(--dur-3) ease;
	}

	.sheep-card.working {
		border-color: var(--live-line);
		background:
			radial-gradient(90% 70% at 0% 0%, rgba(95, 224, 194, 0.1), transparent 60%),
			var(--bg-2);
		box-shadow: var(--sheen), 0 18px 50px -30px rgba(95, 224, 194, 0.35);
	}

	/* Live progress shimmer (top edge of card, working state only) */
	.progress-bar {
		position: absolute;
		top: 0;
		left: 0;
		right: 0;
		height: 1px;
		background: linear-gradient(90deg, transparent 0%, var(--live) 40%, #d9fff4 50%, var(--live) 60%, transparent 100%);
		background-size: 220% 100%;
		animation: shimmer 2.6s linear infinite;
	}

	@keyframes shimmer {
		0% { background-position: 110% 0; }
		100% { background-position: -110% 0; }
	}

	.card-header {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		gap: var(--space-2);
	}

	.card-title {
		display: flex;
		flex-direction: column;
		gap: 3px;
		min-width: 0;
	}

	.project-name {
		font-size: var(--fs-md);
		font-weight: var(--fw-semibold);
		letter-spacing: -0.012em;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	a.project-name:hover {
		color: var(--live-hover);
		text-decoration: none;
	}

	.sheep-meta {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.sep {
		color: var(--text-tertiary);
	}

	.provider {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.08em;
	}

	.last-log {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
		padding: 8px 10px;
		border-radius: var(--radius);
		background: rgba(0, 0, 0, 0.28);
		box-shadow: inset 0 0 0 1px var(--border-subtle);
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.caret {
		color: var(--live);
		flex-shrink: 0;
	}

	.last-log-text {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.card-output {
		min-width: 0;
	}

	/* Inject section */
	.inject-section {
		margin-top: auto;
		padding-top: 12px;
		border-top: 1px solid var(--border-subtle);
	}

	.inject-error {
		font-size: var(--fs-xs);
		color: var(--danger);
		margin-bottom: 6px;
	}

	.inject-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 3px 3px 10px;
		border-radius: 10px;
		background: var(--bg-input);
		box-shadow: inset 0 0 0 1px var(--border-subtle);
		transition: box-shadow var(--dur-2) ease;
	}

	.inject-row:focus-within {
		box-shadow: inset 0 0 0 1px var(--live-line), 0 0 0 3px var(--live-soft);
	}

	.inject-icon {
		display: inline-flex;
		color: var(--text-tertiary);
	}

	.inject-input {
		flex: 1;
		min-width: 0;
		padding: 6px 2px;
		border: none;
		border-radius: 0;
		background: transparent;
		box-shadow: none;
		font-size: var(--fs-sm);
		line-height: 1.4;
		resize: none;
		outline: none;
	}

	.inject-input:hover,
	.inject-input:focus {
		border: none;
		box-shadow: none;
	}

	.inject-input:disabled {
		opacity: 0.5;
	}

	.inject-btn {
		flex-shrink: 0;
		display: grid;
		place-items: center;
		width: 30px;
		height: 30px;
		border: 1px solid var(--border);
		border-radius: 8px;
		background: var(--bg-3);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: background var(--dur-1) ease, color var(--dur-1) ease, border-color var(--dur-1) ease;
	}

	.inject-btn.ready {
		background: var(--live);
		border-color: var(--live);
		color: var(--live-ink);
	}

	.inject-btn:disabled {
		cursor: default;
	}

	.spinner {
		width: 12px;
		height: 12px;
		border-radius: 50%;
		border: 2px solid rgba(4, 20, 15, 0.25);
		border-top-color: currentColor;
		animation: spin 700ms linear infinite;
	}

	@media (max-width: 768px) {
		.inject-input {
			font-size: 16px;
		}
	}
</style>
