<script>
	import { apiPost } from '$lib/api.js';
	import Icon from './Icon.svelte';

	// Shepherd (목자) console input (#598). Deliberately a separate component
	// from CommandInput: the project input talks to /api/tasks with sheep and
	// project context, attachments, thinking/model pickers; the shepherd console
	// only posts {prompt} to /api/shepherd/tasks. The shared part (autosize,
	// Enter-to-send) is small enough to keep local rather than extract a base.
	let {
		disabled = false,           // workspace unset/invalid → block sending
		working = false,           // a shepherd task is running
		runningTaskId = 0,         // its id (Stop/Inject target)
		injectSupported = false,   // provider can take mid-run injections at all
		injectAvailable = false,   // the live run actually has an inject channel
		disabledReason = '',       // shown under the field when disabled
		onSent = () => {},         // after a task is enqueued
		onStopped = () => {},      // after Stop succeeds
		onInjected = () => {}      // after an injection is accepted
	} = $props();

	let prompt = $state('');
	let sending = $state(false);
	let stopping = $state(false);
	let injectMode = $state(false);
	let statusMsg = $state(null); // { ok, text } | null
	let fieldEl = $state(null);

	// Floor-pinned layout (#622): a status line under the input would push the
	// input up and shake the output pane's height, so the message renders as an
	// absolutely positioned toast above the field. Success toasts fade out on
	// their own; errors stay until dismissed.
	let toastTimer = null;
	function setStatus(msg) {
		clearTimeout(toastTimer);
		statusMsg = msg;
		if (msg && msg.ok) toastTimer = setTimeout(() => { statusMsg = null; }, 4000);
	}

	// Textarea placeholders cannot ellipsize (wiki: webui-nocturne-design-system),
	// so phones get the short hint.
	let compact = $state(false);
	$effect(() => {
		const mq = window.matchMedia('(max-width: 640px)');
		compact = mq.matches;
		const onChange = (e) => (compact = e.matches);
		mq.addEventListener('change', onChange);
		return () => mq.removeEventListener('change', onChange);
	});

	let canInject = $derived(injectSupported && injectAvailable && runningTaskId > 0);
	let placeholder = $derived(
		injectMode
			? '추가 지시를 입력하세요...'
			: compact
				? '목자에게 지시…'
				: '목자에게 지시를 입력하세요…'
	);

	function autosize() {
		if (!fieldEl) return;
		fieldEl.style.height = 'auto';
		fieldEl.style.height = Math.min(fieldEl.scrollHeight, 160) + 'px';
	}

	function handleKeydown(e) {
		if (e.key !== 'Enter') return;
		if (e.shiftKey) return; // Shift+Enter = newline
		e.preventDefault();
		submit();
	}

	function clearField() {
		prompt = '';
		if (fieldEl) fieldEl.style.height = 'auto';
	}

	async function submit() {
		if (sending || disabled) return;
		const text = prompt.trim();
		if (!text) return;

		if (injectMode && canInject) {
			await handleInject(text);
			return;
		}

		sending = true;
		setStatus(null);
		clearField();
		injectMode = false;
		try {
			const res = await apiPost('/api/shepherd/tasks', { prompt: text });
			if (res?.success) {
				setStatus({ ok: true, text: `지시 등록 · 작업 #${res.data.task_id}` });
				onSent(res.data);
			} else {
				setStatus({ ok: false, text: res?.message || '지시 등록에 실패했습니다' });
			}
		} catch (err) {
			setStatus({ ok: false, text: err?.message || '지시 등록 요청이 실패했습니다' });
		} finally {
			sending = false;
		}
	}

	async function handleInject(text) {
		sending = true;
		setStatus(null);
		clearField();
		injectMode = false;
		try {
			const res = await apiPost('/api/sheep/shepherd/inject', { prompt: text });
			if (res?.success) {
				setStatus({ ok: true, text: '💬 실행 중인 작업에 추가 지시를 주입했습니다' });
				onInjected();
			} else {
				setStatus({ ok: false, text: res?.message || '주입에 실패했습니다' });
			}
		} catch (err) {
			setStatus({ ok: false, text: err?.message || '주입 요청이 실패했습니다' });
		} finally {
			sending = false;
		}
	}

	async function handleStop() {
		if (stopping || !runningTaskId) return;
		stopping = true;
		setStatus(null);
		try {
			const res = await apiPost(`/api/tasks/${runningTaskId}/stop`, {});
			if (res?.success) {
				setStatus({ ok: true, text: `작업 #${runningTaskId} 중지 요청을 보냈습니다` });
				onStopped();
			} else {
				setStatus({ ok: false, text: res?.message || '중지에 실패했습니다' });
			}
		} catch (err) {
			setStatus({ ok: false, text: err?.message || '중지 요청이 실패했습니다' });
		} finally {
			stopping = false;
		}
	}
</script>

<div class="console-wrap">
	{#if statusMsg}
		<div class="console-toast" class:error={!statusMsg.ok} role={statusMsg.ok ? 'status' : 'alert'}>
			<span>{statusMsg.text}</span>
			{#if !statusMsg.ok}
				<button class="toast-close" onclick={() => setStatus(null)} aria-label="알림 닫기">
					<Icon name="x" size={12} />
				</button>
			{/if}
		</div>
	{/if}

	<div class="shepherd-console" class:inject={injectMode} class:locked={disabled}>
	<span class="prompt-glyph" aria-hidden="true">
		<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="7 6 13 12 7 18"/><line x1="15" y1="18" x2="19" y2="18"/></svg>
	</span>
	<textarea
		class="console-field"
		bind:this={fieldEl}
		bind:value={prompt}
		onkeydown={handleKeydown}
		oninput={autosize}
		{placeholder}
		aria-label={injectMode ? '추가 지시' : '목자 지시'}
		disabled={disabled || sending}
		rows="1"
	></textarea>
	<div class="console-actions">
		{#if !prompt && !sending && !working && !disabled}
			<span class="hint" aria-hidden="true"><kbd>↵</kbd> 전송 <kbd>⇧↵</kbd> 줄바꿈</span>
		{/if}
		{#if disabled}
			<span class="locked-hint">{disabledReason || '워크스페이스를 먼저 지정하세요'}</span>
		{/if}
		{#if working && runningTaskId}
			{#if canInject}
				<button
					class="btn btn-sm btn-live btn-inject"
					class:active={injectMode}
					onclick={() => { injectMode = !injectMode; fieldEl?.focus(); }}
					disabled={sending || stopping}
					aria-pressed={injectMode}
					title="실행 중인 작업에 추가 지시 주입"
				>
					<Icon name="message-square" size={13} />
					주입
				</button>
			{/if}
			<button class="btn btn-sm btn-danger btn-stop" onclick={handleStop} disabled={stopping} title={`작업 #${runningTaskId} 중지`}>
				<Icon name="square" size={11} stroke={2.6} />
				{stopping ? '중지 중…' : '중지'}
			</button>
		{/if}
		<button
			class="send"
			class:ready={!!prompt.trim()}
			onclick={submit}
			disabled={disabled || sending || !prompt.trim()}
			aria-label={injectMode ? '추가 지시 전송' : '지시 전송'}
			title={injectMode ? '추가 지시 전송' : '전송 (Enter)'}
		>
			{#if sending}
				<span class="spinner" aria-hidden="true"></span>
			{:else}
				<Icon name="arrow-up-right" size={16} stroke={2.2} />
			{/if}
		</button>
	</div>
	</div>
</div>

<style>
	.shepherd-console {
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

	.shepherd-console.inject {
		box-shadow: inset 0 0 0 1px var(--live-line), inset 0 1px 3px rgba(0, 0, 0, 0.4), 0 0 0 3px var(--live-soft);
	}

	.shepherd-console.locked {
		opacity: 0.55;
	}

	.prompt-glyph {
		flex-shrink: 0;
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		color: var(--accent);
	}

	.console-field {
		flex: 1;
		min-width: 0;
		resize: none;
		border: none;
		outline: none;
		background: transparent;
		color: var(--text-primary);
		font-family: inherit;
		font-size: var(--fs-base);
		line-height: 1.5;
		padding: 9px 0;
		max-height: 160px;
	}

	.console-field::placeholder {
		color: var(--text-tertiary);
	}

	.console-field:disabled {
		cursor: not-allowed;
	}

	.console-actions {
		flex-shrink: 0;
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.hint,
	.locked-hint {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		white-space: nowrap;
	}

	.locked-hint {
		color: var(--warning);
	}

	.hint kbd {
		font-family: var(--font-mono);
		font-size: 10px;
		border: 1px solid var(--border);
		border-radius: 4px;
		padding: 1px 4px;
		background: var(--bg-3);
	}

	.btn-inject.active {
		background: var(--live-soft);
		box-shadow: inset 0 0 0 1px var(--live-line);
	}

	.send {
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border-radius: 9px;
		border: 1px solid var(--border);
		background: var(--bg-3);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: background var(--dur-1) ease, color var(--dur-1) ease, border-color var(--dur-1) ease;
	}

	.send.ready {
		background: var(--accent);
		border-color: var(--accent);
		color: #1a1206;
	}

	.send:hover:not(:disabled) {
		border-color: var(--accent-line);
		color: var(--accent);
	}

	.send.ready:hover:not(:disabled) {
		color: #1a1206;
		filter: brightness(1.08);
	}

	.send:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.spinner {
		width: 14px;
		height: 14px;
		border-radius: 50%;
		border: 2px solid rgba(237, 230, 218, 0.25);
		border-top-color: var(--accent);
		animation: spin 0.8s linear infinite;
	}

	@keyframes spin {
		to { transform: rotate(360deg); }
	}

	.console-wrap {
		position: relative;
	}

	/* Result message: floats over the top edge of the input so it never adds
	   height to the floor-pinned command bar (#622). Success fades by itself;
	   errors keep a close button and stay until dismissed. */
	.console-toast {
		position: absolute;
		left: 8px;
		right: 8px;
		bottom: calc(100% + 6px);
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 6px 10px;
		border-radius: var(--radius);
		background: var(--bg-3);
		border: 1px solid var(--success-line, var(--border));
		color: var(--success);
		font-size: var(--fs-xs);
		box-shadow: var(--shadow-2);
		animation: rise-in var(--dur-2) var(--ease-out) backwards;
	}

	.console-toast.error {
		color: var(--danger);
		border-color: var(--danger-line, var(--border));
	}

	.console-toast span { flex: 1; min-width: 0; }

	.toast-close {
		display: grid;
		place-items: center;
		width: 20px;
		height: 20px;
		flex-shrink: 0;
		border: none;
		border-radius: var(--radius-sm);
		background: none;
		color: inherit;
		cursor: pointer;
	}

	.toast-close:hover { background: var(--bg-hover); }

	@media (max-width: 640px) {
		.hint { display: none; }
	}
</style>
