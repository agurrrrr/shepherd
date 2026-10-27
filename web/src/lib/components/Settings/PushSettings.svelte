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
	import { translate, t } from '$lib/i18n/index.js';

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
			flash(translate('pushSettings.enabledFlash'), 'ok');
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
			flash(translate('pushSettings.disabledFlash'), 'ok');
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
			if (sent > 0) flash(translate('pushSettings.testSent', { count: sent }), 'ok');
			else flash(translate('pushSettings.testNoSubscription'), 'error');
		} catch (err) {
			flash(err?.message || String(err), 'error');
		} finally {
			busy = false;
		}
	}
</script>

<hr class="setting-divider" />
<div class="setting-section-title">{$t('pushSettings.sectionTitle')}</div>
<p class="hint intro">
	{$t('pushSettings.intro')}
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
	<label>{$t('pushSettings.thisDevice')}</label>
	<div class="device-status">
		{#if !secure}
			<span class="pill warn">{$t('pushSettings.pillHttps')}</span>
		{:else if !supported}
			<span class="pill warn">{$t('pushSettings.pillUnsupported')}</span>
		{:else if ios && !standalone}
			<span class="pill warn">{$t('pushSettings.pillInstallFirst')}</span>
		{:else if permission === 'denied'}
			<span class="pill danger">{$t('pushSettings.pillDenied')}</span>
		{:else if subscribed}
			<span class="pill ok">{$t('pushSettings.pillSubscribed')}</span>
		{:else}
			<span class="pill">{$t('pushSettings.pillNotSubscribed')}</span>
		{/if}
		<span class="hint">{$t('pushSettings.deviceCount', { count: deviceCount })}</span>
	</div>
	<div class="device-actions">
		{#if subscribed}
			<button class="btn" type="button" onclick={onDisable} disabled={busy}>{$t('pushSettings.disableDevice')}</button>
		{:else}
			<button class="btn btn-primary" type="button" onclick={onEnable} disabled={busy || !supported || (ios && !standalone)}>
				{$t('pushSettings.enableDevice')}
			</button>
		{/if}
		<button class="btn" type="button" onclick={onTest} disabled={busy || !subscribed}>{$t('pushSettings.testNotification')}</button>
	</div>
	{#if ios && !standalone}
		<span class="hint">{$t('pushSettings.iosHint')}</span>
	{/if}
	{#if !secure}
		<span class="hint">{$t('pushSettings.chromeHint')}</span>
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
		font-size: var(--fs-sm);
		font-weight: 500;
		flex: 0 0 180px;
		color: var(--text-primary);
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
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		line-height: 1.55;
	}

	.hint.intro {
		margin: 0 0 8px;
	}

	.hint.error {
		color: var(--danger-text);
	}

	.hint.ok {
		color: var(--success);
	}

	.setting-section-title {
		font-size: var(--fs-base);
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
		border-radius: 0;
		font-size: var(--fs-xs);
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
		color: var(--danger-text);
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
