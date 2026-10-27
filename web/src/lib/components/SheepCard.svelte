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
	}

	/* Live progress (top edge of the card, working state only): a black
	   6px track with a run of blue cells hopping across it. */
	.progress-bar {
		position: absolute;
		top: 3px;
		left: 3px;
		right: 3px;
		height: 6px;
		background: #000;
		overflow: hidden;
	}

	.progress-bar::after {
		content: '';
		position: absolute;
		top: 0;
		bottom: 0;
		left: 0;
		width: 40%;
		background: repeating-linear-gradient(90deg, var(--live) 0 6px, transparent 6px 8px);
		animation: px-scan 1.6s steps(12) infinite;
	}

	@keyframes px-scan {
		from { transform: translateX(-100%); }
		to { transform: translateX(250%); }
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
		letter-spacing: 0;
		color: var(--highlight);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	a.project-name:hover {
		color: var(--highlight-hover);
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
		letter-spacing: 0;
	}

	.last-log {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
		padding: 8px 10px;
		border-radius: 0;
		background: #000;
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
		padding-top: 15px;
		border-top: 3px solid var(--lo);
	}

	.inject-error {
		font-size: var(--fs-xs);
		color: var(--danger-text);
		margin-bottom: 6px;
	}

	.inject-row {
		display: flex;
		align-items: center;
		gap: 6px;
		margin: 3px;
		padding: 3px 3px 3px 10px;
		border-radius: 0;
		background: var(--well);
		box-shadow: 0 0 0 3px #000;
	}

	/* The field inside drops its own outline; the row carries the focus. */
	.inject-row:focus-within {
		outline: 3px solid var(--highlight);
		outline-offset: 4px;
	}

	.inject-icon {
		display: inline-flex;
		color: var(--queued-text);
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
		border: 0;
		border-radius: 0;
		background: var(--p-dgray);
		color: var(--text-tertiary);
		cursor: pointer;
	}

	.inject-btn.ready {
		background: var(--accent);
		box-shadow: var(--px-bevel-btn);
		color: #000;
	}

	.inject-btn.ready:hover {
		background: var(--accent-hover);
	}

	.inject-btn:disabled {
		cursor: default;
	}

	.spinner {
		width: 12px;
		height: 12px;
		border-radius: 0;
		border: 2px solid transparent;
		border-top-color: currentColor;
		border-left-color: currentColor;
		animation: spin 800ms steps(4) infinite;
	}

	@media (max-width: 768px) {
		.inject-input {
			font-size: var(--fs-md);
		}
	}
</style>
