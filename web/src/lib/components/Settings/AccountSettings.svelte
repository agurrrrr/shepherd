<script>
	import { apiPut } from '$lib/api.js';
	import { accessToken, refreshToken, username } from '$lib/stores.js';

	let newUsername = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');
	let currentPassword = $state('');
	let busy = $state(false);
	let msg = $state('');

	function validate() {
		const nextName = newUsername.trim();
		if (/\s/.test(nextName)) return '아이디에는 공백을 넣을 수 없습니다.';
		if ((!nextName || nextName === $username) && !newPassword) {
			return '변경할 아이디나 새 비밀번호를 입력해 주세요.';
		}
		if (newPassword !== confirmPassword) return '새 비밀번호와 확인 값이 일치하지 않습니다.';
		if (!currentPassword) return '현재 비밀번호를 입력해 주세요.';
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
				msg = '변경했습니다. 다른 기기에서는 새 정보로 다시 로그인해 주세요.';
			} else {
				msg = 'Error: ' + (res?.message || '변경에 실패했습니다.');
			}
		} catch (err) {
			msg = 'Error: ' + (err?.message || '변경에 실패했습니다.');
		} finally {
			busy = false;
		}
	}
</script>

<div class="card">
	<h2 class="section-title">로그인 계정</h2>
	<p class="sync-desc">
		WebUI 로그인 아이디와 비밀번호를 변경합니다. 바꾸지 않을 항목은 비워 두세요.
		변경하면 이 브라우저를 제외한 모든 기기의 로그인이 해제됩니다.
	</p>

	<form class="account-form" onsubmit={submit}>
		<div class="setting-row readonly">
			<span class="row-label">현재 아이디</span>
			<span class="mono">{$username || '(unknown)'}</span>
		</div>
		<div class="setting-row">
			<label for="account-new-username">새 아이디</label>
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
			<label for="account-new-password">새 비밀번호</label>
			<input
				id="account-new-password"
				class="input"
				type="password"
				autocomplete="new-password"
				bind:value={newPassword}
			/>
		</div>
		<div class="setting-row">
			<label for="account-confirm-password">새 비밀번호 확인</label>
			<input
				id="account-confirm-password"
				class="input"
				type="password"
				autocomplete="new-password"
				bind:value={confirmPassword}
			/>
		</div>
		<div class="setting-row">
			<label for="account-current-password">현재 비밀번호</label>
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
				{busy ? 'Saving...' : '계정 변경'}
			</button>
			{#if msg}
				<span class="sync-msg" class:error={msg.startsWith('Error')}>{msg}</span>
			{/if}
		</div>
	</form>
</div>

<style>
	.card {
		max-width: 640px;
	}
	.section-title {
		font-size: 16px;
		font-weight: 600;
		margin-bottom: 16px;
	}
	.sync-desc {
		font-size: 13px;
		color: var(--text-secondary);
		line-height: 1.5;
		margin-bottom: 12px;
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
		font-size: 13px;
		color: var(--success);
	}
	.sync-msg.error {
		color: var(--danger);
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
		font-size: 14px;
		font-weight: 500;
		flex: 0 0 140px;
	}
	.setting-row .input {
		flex: 1 1 200px;
		max-width: 240px;
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
			flex: none;
		}
		.setting-row .input {
			flex: none;
			max-width: none;
		}
	}
</style>
