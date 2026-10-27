<script>
	import { apiPut } from '$lib/api.js';
	import { accessToken, refreshToken, username } from '$lib/stores.js';
	import { translate, t } from '$lib/i18n/index.js';

	let newUsername = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');
	let currentPassword = $state('');
	let busy = $state(false);
	let msg = $state('');

	function validate() {
		const nextName = newUsername.trim();
		if (/\s/.test(nextName)) return translate('account.errWhitespace');
		if ((!nextName || nextName === $username) && !newPassword) {
			return translate('account.errNothing');
		}
		if (newPassword !== confirmPassword) return translate('account.errMismatch');
		if (!currentPassword) return translate('account.errNeedCurrent');
		return '';
	}

	async function submit(e) {
		e.preventDefault();
		msg = '';
		const invalid = validate();
		if (invalid) {
			msg = 'Error: ' + invalid;
			return;
		}

		busy = true;
		try {
			const res = await apiPut('/api/auth/credentials', {
				current_password: currentPassword,
				new_username: newUsername.trim(),
				new_password: newPassword
			});
			if (res?.success) {
				// The server rotated the JWT secret, so the old tokens are dead —
				// switch to the fresh pair to keep this browser signed in.
				accessToken.set(res.data.access_token);
				refreshToken.set(res.data.refresh_token);
				username.set(res.data.username);
				newUsername = '';
				newPassword = '';
				confirmPassword = '';
				currentPassword = '';
				msg = translate('account.changed');
			} else {
				msg = 'Error: ' + (res?.message || translate('account.changeFailed'));
			}
		} catch (err) {
			msg = 'Error: ' + (err?.message || translate('account.changeFailed'));
		} finally {
			busy = false;
		}
	}
</script>

<div class="card">
	<h2 class="section-title">{$t('account.title')}</h2>
	<p class="sync-desc">
		{$t('account.desc')}
	</p>

	<form class="account-form" onsubmit={submit}>
		<div class="setting-row readonly">
			<span class="row-label">{$t('account.currentUsername')}</span>
			<span class="mono">{$username || '(unknown)'}</span>
		</div>
		<div class="setting-row">
			<label for="account-new-username">{$t('account.newUsername')}</label>
			<input
				id="account-new-username"
				class="input"
				type="text"
				autocomplete="username"
				bind:value={newUsername}
				placeholder={$username || 'admin'}
			/>
		</div>
		<div class="setting-row">
			<label for="account-new-password">{$t('account.newPassword')}</label>
			<input
				id="account-new-password"
				class="input"
				type="password"
				autocomplete="new-password"
				bind:value={newPassword}
			/>
		</div>
		<div class="setting-row">
			<label for="account-confirm-password">{$t('account.confirmPassword')}</label>
			<input
				id="account-confirm-password"
				class="input"
				type="password"
				autocomplete="new-password"
				bind:value={confirmPassword}
			/>
		</div>
		<div class="setting-row">
			<label for="account-current-password">{$t('account.currentPassword')}</label>
			<input
				id="account-current-password"
				class="input"
				type="password"
				autocomplete="current-password"
				bind:value={currentPassword}
				required
			/>
		</div>

		<div class="sync-actions">
			<button class="btn btn-sm btn-primary" type="submit" disabled={busy}>
				{busy ? 'Saving...' : $t('account.submit')}
			</button>
			{#if msg}
				<span class="sync-msg" class:error={msg.startsWith('Error')}>{msg}</span>
			{/if}
		</div>
	</form>
</div>

<style>
	.card {
		max-width: none;
	}
	.section-title {
		font-family: var(--font-display);
		font-size: var(--fs-lg);
		font-weight: 400;
		letter-spacing: 0;
		margin-bottom: 12px;
	}
	.sync-desc {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		line-height: 1.5;
		margin-bottom: 12px;
		max-width: 70ch;
	}
	.account-form {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.sync-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 12px;
		margin-top: 4px;
	}
	.sync-msg {
		font-size: var(--fs-sm);
		color: var(--success);
	}
	.sync-msg.error {
		color: var(--danger-text);
	}

	.setting-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		column-gap: 16px;
		row-gap: 6px;
	}
	.setting-row > label,
	.setting-row > .row-label {
		font-size: var(--fs-sm);
		font-weight: 500;
		flex: 0 0 180px;
		color: var(--text-primary);
	}
	.setting-row .input {
		flex: 1 1 200px;
		max-width: 340px;
		min-width: 0;
	}
	.setting-row.readonly {
		color: var(--text-secondary);
	}

	@media (max-width: 768px) {
		.setting-row {
			flex-direction: column;
			align-items: stretch;
			gap: 6px;
		}
		.setting-row > label,
		.setting-row > .row-label {
			flex: 0 0 180px;
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}
		.setting-row .input {
			flex: none;
			max-width: none;
		}
	}
</style>
