<script>
	import { t } from '$lib/i18n/index.js';

	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, grok: boolean, embedded: boolean, magi: boolean }} */
	export let providerEnabled;
	/** @type {'claude' | 'opencode' | 'pi' | 'grok' | 'embedded' | 'magi'} */
	export let provider;
	/** @type {string} 예: '🟠 Claude' */
	export let label;
</script>

<div class="provider-enable" class:off={!providerEnabled[provider]}>
	<label class="toggle">
		<input type="checkbox" bind:checked={providerEnabled[provider]} />
		<span class="prov-label">{label}</span>
		<span class="state">{providerEnabled[provider] ? $t('providerToggle.on') : $t('providerToggle.off')}</span>
	</label>
	<p class="hint">{$t('providerToggle.hint')}</p>
</div>

<style>
	.provider-enable {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 12px 14px;
		border: 1px solid var(--border);
		border-radius: 0;
		background: color-mix(in srgb, var(--success) 8%, transparent);
		margin-bottom: 8px;
	}
	.provider-enable.off {
		background: color-mix(in srgb, var(--danger) 8%, transparent);
	}
	.toggle {
		display: flex;
		align-items: center;
		gap: 10px;
		cursor: pointer;
	}
	.prov-label {
		font-size: var(--fs-md);
		font-weight: 600;
	}
	.state {
		font-size: var(--fs-xs);
		font-weight: 600;
		padding: 1px 8px;
		border-radius: 0;
		background: color-mix(in srgb, var(--success) 20%, transparent);
		color: var(--success);
	}
	.provider-enable.off .state {
		background: color-mix(in srgb, var(--danger) 20%, transparent);
		color: var(--danger-text);
	}
	.hint {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		margin: 0;
		line-height: 1.55;
	}
</style>
