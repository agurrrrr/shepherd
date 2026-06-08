<script>
	import { apiGet, apiPost } from '$lib/api.js';

	/** @type {{ claude: any, opencode: any, pi: any }} */
	export let status;
	export let loaded;

	let registering = {};
	let errorMsg = '';

	async function register(provider) {
		registering[provider] = true;
		errorMsg = '';
		const res = await apiPost('/api/mcp/register', { provider });
		if (res?.success) {
			const statusRes = await apiGet('/api/mcp/status');
			if (statusRes?.data) {
				// Dispatch event to update parent
				window.dispatchEvent(new CustomEvent('mcp-status-changed', { detail: statusRes.data }));
			}
		} else {
			errorMsg = res?.message || 'Registration failed';
		}
		registering[provider] = false;
	}
</script>

{#if !loaded}
	<p class="text-muted">Loading...</p>
{:else}
	<div class="mcp-providers">
		{#each [
			{ key: 'claude', label: 'Claude Code' },
			{ key: 'opencode', label: 'OpenCode' },
			{ key: 'pi', label: 'Pi' }
		] as provider}
			{@const s = status[provider.key]}
			<div class="mcp-provider">
				<div class="mcp-provider-info">
					<span class="mcp-provider-name">{provider.label}</span>
					{#if s?.registered}
						<span class="badge badge-success">Registered</span>
					{:else}
						<span class="badge badge-muted">Not Registered</span>
					{/if}
				</div>
				<div class="mcp-provider-path">{s?.config_path || ''}</div>
				{#if s?.error}
					<div class="mcp-provider-error">{s.error}</div>
				{/if}
				{#if !s?.registered}
					<button
						class="btn btn-sm"
						onclick={() => register(provider.key)}
						disabled={registering[provider.key]}
					>
						{registering[provider.key] ? 'Registering...' : 'Register'}
					</button>
				{/if}
			</div>
		{/each}
	</div>
	{#if errorMsg}
		<div class="mcp-error">{errorMsg}</div>
	{/if}
{/if}

<style>
	.text-muted { color: var(--text-secondary); }
	.section-title {
		font-size: 16px;
		font-weight: 600;
		margin-bottom: 16px;
	}
	.mcp-providers {
		display: flex;
		flex-direction: column;
		gap: 16px;
	}
	.mcp-provider {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 12px;
		background: var(--bg-secondary);
		border-radius: 8px;
		border: 1px solid var(--border);
	}
	.mcp-provider-info {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	.mcp-provider-name {
		font-size: 14px;
		font-weight: 600;
	}
	.mcp-provider-path {
		font-size: 12px;
		color: var(--text-secondary);
		font-family: monospace;
	}
	.mcp-provider-error {
		font-size: 12px;
		color: var(--danger);
	}
	.mcp-error {
		margin-top: 12px;
		font-size: 13px;
		color: var(--danger);
	}
	.badge {
		font-size: 11px;
		font-weight: 600;
		padding: 2px 8px;
		border-radius: 10px;
	}
	.badge-success {
		background: color-mix(in srgb, var(--success) 15%, transparent);
		color: var(--success);
	}
	.badge-muted {
		background: var(--bg-tertiary);
		color: var(--text-secondary);
	}
	.btn-sm {
		padding: 4px 12px;
		font-size: 12px;
		font-weight: 600;
		background: var(--accent);
		color: white;
		border: none;
		border-radius: 6px;
		cursor: pointer;
		align-self: flex-start;
		transition: opacity 0.15s;
	}
	.btn-sm:hover { opacity: 0.85; }
	.btn-sm:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
