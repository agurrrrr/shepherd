<script>
	import { goto } from '$app/navigation';
	import { login } from '$lib/api.js';

	let id = '';
	let password = '';
	let error = '';
	let loading = false;

	async function handleSubmit() {
		error = '';
		loading = true;

		const result = await login(id, password);

		if (result.success) {
			goto('/');
		} else {
			error = result.error;
		}
		loading = false;
	}

	function handleKeydown(e) {
		if (e.key === 'Enter') handleSubmit();
	}
</script>

<div class="login-page">
	<div class="login-card">
		<div class="login-header">
			<span class="login-icon">&#x1F411;</span>
			<h1>Shepherd</h1>
			<p class="login-subtitle">AI Coding Orchestration</p>
		</div>

		{#if error}
			<div class="login-error">{error}</div>
		{/if}

		<div class="login-form">
			<div class="form-group">
				<label for="username">Username</label>
				<input
					id="username"
					class="input"
					type="text"
					bind:value={id}
					placeholder="admin"
					disabled={loading}
					onkeydown={handleKeydown}
				/>
			</div>

			<div class="form-group">
				<label for="password">Password</label>
				<input
					id="password"
					class="input"
					type="password"
					bind:value={password}
					placeholder="Password"
					disabled={loading}
					onkeydown={handleKeydown}
				/>
			</div>

			<button class="btn btn-primary login-btn" onclick={handleSubmit} disabled={loading}>
				{loading ? 'Logging in...' : 'Login'}
			</button>
		</div>
	</div>
</div>

<style>
	.login-page {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 100vh;
		background: var(--bg-primary);
	}

	.login-card {
		width: 100%;
		max-width: 380px;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 8px;
		padding: 32px;
	}

	.login-header {
		text-align: center;
		margin-bottom: 24px;
	}

	.login-icon {
		font-size: 48px;
		display: block;
		margin-bottom: 8px;
	}

	.login-header h1 {
		font-size: 24px;
		font-weight: 600;
		color: var(--text-primary);
	}

	.login-subtitle {
		color: var(--text-secondary);
		font-size: 13px;
		margin-top: 4px;
	}

	.login-error {
		background: rgba(248, 81, 73, 0.1);
		border: 1px solid var(--danger);
		color: var(--danger);
		padding: 8px 12px;
		border-radius: var(--radius);
		font-size: 13px;
		margin-bottom: 16px;
	}

	.login-form {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}

	.form-group {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.form-group label {
		font-size: 13px;
		font-weight: 500;
		color: var(--text-secondary);
	}

	.form-group .input {
		width: 100%;
	}

	.login-btn {
		width: 100%;
		justify-content: center;
		padding: 10px;
		font-weight: 500;
		margin-top: 4px;
	}
</style>
