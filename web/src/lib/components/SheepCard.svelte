<script>
	import StatusBadge from './StatusBadge.svelte';
	import OutputViewer from './OutputViewer.svelte';
	import { onMount, onDestroy } from 'svelte';
	import { onSSE } from '$lib/sse.js';
	import { apiPost } from '$lib/api.js';

	export let name = '';
	export let project = '';
	export let status = 'idle';
	export let provider = 'claude';

	let output = [];
	let unsubscribers = [];
	let injectText = '';
	let injecting = false;
	let injectError = '';

	$: lastLogLine = output.length > 0 ? output[output.length - 1] : '';

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

	function trimLine(s) {
		const t = (s || '').replace(/\s+/g, ' ').trim();
		return t.length > 120 ? t.slice(0, 120) + '…' : t;
	}

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
			<span class="project-name">{project || name}</span>
			{#if project}
				<span class="sheep-name">{name}</span>
			{/if}
		</div>
		<StatusBadge {status} />
	</div>
	<div class="card-meta">
		<span class="provider">{provider}</span>
		{#if status === 'working' && lastLogLine}
			<span class="last-log mono" title={lastLogLine}>{trimLine(lastLogLine)}</span>
		{/if}
	</div>
	{#if output.length > 0}
		<OutputViewer lines={output} maxLines={50} />
	{/if}

	<!-- Prompt injection section — visible only when working -->
	{#if status === 'working'}
		<div class="inject-section">
			<div class="inject-header">
				<span class="inject-label">💬 추가 지시</span>
			</div>
			{#if injectError}
				<div class="inject-error">{injectError}</div>
			{/if}
			<div class="inject-row">
				<textarea
					bind:value={injectText}
					onkeydown={handleKeydown}
					placeholder="추가 지시를 입력하세요 (Enter로 전송)"
					class="inject-input"
					rows={1}
					disabled={injecting}
				/>
				<button
					class="inject-btn"
					onclick={handleInject}
					disabled={injecting || !injectText.trim()}
				>
					{injecting ? '전송 중...' : '주입하기'}
				</button>
			</div>
		</div>
	{/if}
</div>

<style>
	.sheep-card {
		transition: border-color 0.3s, background 0.3s;
		position: relative;
		overflow: hidden;
	}
	.sheep-card.working {
		border-color: var(--live);
		background:
			linear-gradient(180deg, var(--live-soft), transparent 40%),
			var(--bg-2);
	}
	.sheep-card.working::before {
		content: '';
		position: absolute;
		top: -1px;
		left: -1px;
		bottom: -1px;
		width: 3px;
		background: var(--live);
		border-radius: var(--radius) 0 0 var(--radius);
	}

	/* Live progress shimmer (top edge of card, working state only) */
	.progress-bar {
		position: absolute;
		top: -1px;
		left: 3px;
		right: 0;
		height: 2px;
		background: linear-gradient(
			90deg,
			transparent 0%,
			var(--live) 30%,
			var(--live-hover) 50%,
			var(--live) 70%,
			transparent 100%
		);
		background-size: 200% 100%;
		animation: shimmer 2.4s linear infinite;
		opacity: 0.85;
	}

	@keyframes shimmer {
		0% { background-position: 100% 0; }
		100% { background-position: -100% 0; }
	}
	.card-header {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		margin-bottom: var(--space-2);
		gap: var(--space-2);
	}
	.card-title {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.project-name {
		font-weight: var(--fw-semibold);
		font-size: var(--fs-base);
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.sheep-name {
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		font-family: var(--font-mono);
	}
	.card-meta {
		margin-bottom: var(--space-2);
		display: flex;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
	}
	.provider {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		font-family: var(--font-mono);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		flex-shrink: 0;
	}
	.last-log {
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
		flex: 1;
		opacity: 0.85;
		padding-left: var(--space-2);
		border-left: 1px solid var(--border);
	}

	/* Inject section */
	.inject-section {
		margin-top: var(--space-3);
		padding-top: var(--space-2);
		border-top: 1px solid var(--border);
	}
	.inject-header {
		margin-bottom: var(--space-1);
	}
	.inject-label {
		font-size: var(--fs-xs);
		font-weight: var(--fw-semibold);
		color: var(--text-secondary);
	}
	.inject-error {
		font-size: var(--fs-xs);
		color: var(--danger);
		margin-bottom: var(--space-1);
	}
	.inject-row {
		display: flex;
		gap: var(--space-1);
		align-items: flex-end;
	}
	.inject-input {
		flex: 1;
		min-width: 0;
		padding: var(--space-1) var(--space-2);
		font-size: var(--fs-sm);
		font-family: inherit;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--bg-1);
		color: var(--text-primary);
		resize: vertical;
		line-height: 1.4;
		transition: border-color 0.2s;
	}
	.inject-input:focus {
		outline: none;
		border-color: var(--accent);
	}
	.inject-input:disabled {
		opacity: 0.5;
	}
	.inject-btn {
		flex-shrink: 0;
		padding: var(--space-1) var(--space-2);
		font-size: var(--fs-xs);
		font-weight: var(--fw-semibold);
		font-family: inherit;
		border: none;
		border-radius: var(--radius-sm);
		background: var(--accent);
		color: var(--bg-1);
		cursor: pointer;
		transition: opacity 0.2s;
		white-space: nowrap;
	}
	.inject-btn:hover:not(:disabled) {
		opacity: 0.85;
	}
	.inject-btn:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
</style>
