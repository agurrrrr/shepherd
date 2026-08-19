<script>
	import { onMount } from 'svelte';
	import {
		disablePush,
		enablePush,
		fetchPushStatus,
		getCurrentSubscription,
		isIOS,
		isPushSupported,
		isSecureContextNow,
		isStandaloneDisplay,
		sendTestPush
	} from '$lib/push.js';

	/** @type {{ configData: Record<string, any> }} */
	let { configData } = $props();

	let supported = $state(false);
	let secure = $state(true);
	let standalone = $state(false);
	let ios = $state(false);
	let permission = $state('default');
	let subscribed = $state(false);
	let deviceCount = $state(0);
	let busy = $state(false);
	let msg = $state('');
	let msgKind = $state('');

	onMount(async () => {
		supported = isPushSupported();
		secure = isSecureContextNow();
		standalone = isStandaloneDisplay();
		ios = isIOS();
		if (typeof Notification !== 'undefined') permission = Notification.permission;
		await refresh();
	});

	async function refresh() {
		try {
			const status = await fetchPushStatus();
			if (status) deviceCount = status.subscription_count ?? 0;
		} catch {
			// daemon may not have the route yet
		}
		try {
			const sub = await getCurrentSubscription();
			subscribed = !!sub;
		} catch {
			subscribed = false;
		}
		if (typeof Notification !== 'undefined') permission = Notification.permission;
	}

	function flash(text, kind = '') {
		msg = text;
		msgKind = kind;
		setTimeout(() => {
			if (msg === text) {
				msg = '';
				msgKind = '';
			}
		}, 4000);
	}

	async function onEnable() {
		busy = true;
		try {
			await enablePush();
			await refresh();
			flash('이 기기에서 푸시를 켰습니다.', 'ok');
		} catch (err) {
			flash(err?.message || String(err), 'error');
		} finally {
			busy = false;
		}
	}

	async function onDisable() {
		busy = true;
		try {
			await disablePush();
			await refresh();
			flash('이 기기 구독을 해제했습니다.', 'ok');
		} catch (err) {
			flash(err?.message || String(err), 'error');
		} finally {
			busy = false;
		}
	}

	async function onTest() {
		busy = true;
		try {
			const result = await sendTestPush();
			const sent = result?.sent ?? 0;
			if (sent > 0) flash(`테스트 알림 ${sent}건을 보냈습니다.`, 'ok');
			else flash('보낼 구독이 없습니다. 먼저 이 기기에서 푸시를 켜 주세요.', 'error');
		} catch (err) {
			flash(err?.message || String(err), 'error');
		} finally {
			busy = false;
		}
	}
</script>

<hr class="setting-divider" />
<div class="setting-section-title">PWA 푸시 알림</div>
<p class="hint intro">
	홈 화면에 설치한 Shepherd에서 작업이 끝나거나 실패하면 시스템 알림을 받습니다.
	탭이 닫혀 있어도 Web Push로 전달됩니다. HTTPS(또는 localhost)가 필요합니다.
</p>

<div class="setting-row">
	<label>Enabled</label>
	<div class="toggle">
		<input type="checkbox" bind:checked={configData.webpush_enabled} />
		<span>{configData.webpush_enabled ? 'Enabled' : 'Disabled'}</span>
	</div>
</div>
<div class="setting-row">
	<label>Notify on Complete</label>
	<div class="toggle">
		<input type="checkbox" bind:checked={configData.webpush_notify_on_complete} />
		<span>{configData.webpush_notify_on_complete ? 'Enabled' : 'Disabled'}</span>
	</div>
</div>
<div class="setting-row">
	<label>Notify on Fail</label>
	<div class="toggle">
		<input type="checkbox" bind:checked={configData.webpush_notify_on_fail} />
		<span>{configData.webpush_notify_on_fail ? 'Enabled' : 'Disabled'}</span>
	</div>
</div>

<div class="setting-row column">
	<label>이 기기</label>
	<div class="device-status">
		{#if !secure}
			<span class="pill warn">HTTPS 필요</span>
		{:else if !supported}
			<span class="pill warn">이 브라우저는 Push 미지원</span>
		{:else if ios && !standalone}
			<span class="pill warn">홈 화면 설치 후 사용</span>
		{:else if permission === 'denied'}
			<span class="pill danger">알림 권한 거부됨</span>
		{:else if subscribed}
			<span class="pill ok">구독됨</span>
		{:else}
			<span class="pill">미구독</span>
		{/if}
		<span class="hint">등록된 기기 {deviceCount}대</span>
	</div>
	<div class="device-actions">
		{#if subscribed}
			<button class="btn" type="button" onclick={onDisable} disabled={busy}>이 기기 끄기</button>
		{:else}
			<button class="btn btn-primary" type="button" onclick={onEnable} disabled={busy || !supported || (ios && !standalone)}>
				이 기기에서 알림 켜기
			</button>
		{/if}
		<button class="btn" type="button" onclick={onTest} disabled={busy || !subscribed}>테스트 알림</button>
	</div>
	{#if ios && !standalone}
		<span class="hint">Safari 공유 → 홈 화면에 추가한 뒤, 그 아이콘으로 연 다음 버튼을 눌러 주세요. (iOS 16.4+)</span>
	{/if}
	{#if !secure}
		<span class="hint">Chrome/Android는 HTTPS 또는 localhost에서만 푸시를 허용합니다.</span>
	{/if}
	{#if msg}
		<span class="hint" class:error={msgKind === 'error'} class:ok={msgKind === 'ok'}>{msg}</span>
	{/if}
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

	.setting-row.column {
		flex-direction: column;
		align-items: stretch;
		gap: 8px;
	}

	.setting-row.column > label:not(.toggle) {
		flex: none;
	}

	.hint {
		font-size: 12px;
		color: var(--text-secondary);
	}

	.hint.intro {
		margin: 0 0 8px;
	}

	.hint.error {
		color: var(--danger);
	}

	.hint.ok {
		color: var(--success);
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

	.device-status {
		display: flex;
		align-items: center;
		gap: 10px;
		flex-wrap: wrap;
	}

	.device-actions {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
	}

	.pill {
		display: inline-flex;
		align-items: center;
		padding: 2px 8px;
		border-radius: 999px;
		font-size: 12px;
		background: var(--bg-3);
		color: var(--text-secondary);
	}

	.pill.ok {
		background: var(--success-soft);
		color: var(--success);
	}

	.pill.warn {
		background: var(--warning-soft);
		color: var(--warning);
	}

	.pill.danger {
		background: var(--danger-soft);
		color: var(--danger);
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
	}
</style>
