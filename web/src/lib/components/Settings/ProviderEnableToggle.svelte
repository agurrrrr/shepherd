<script>
	/** @type {{ claude: boolean, opencode: boolean, pi: boolean, embedded: boolean }} */
	export let providerEnabled;
	/** @type {'claude' | 'opencode' | 'pi' | 'embedded'} */
	export let provider;
	/** @type {string} 예: '🟠 Claude' */
	export let label;
</script>

<div class="provider-enable" class:off={!providerEnabled[provider]}>
	<label class="toggle">
		<input type="checkbox" bind:checked={providerEnabled[provider]} />
		<span class="prov-label">{label}</span>
		<span class="state">{providerEnabled[provider] ? '사용 중' : '사용 안 함'}</span>
	</label>
	<p class="hint">끄면 프로젝트 화면의 provider 선택지에서 숨겨지고, 이 provider로의 작업 실행이 차단됩니다.</p>
</div>

<style>
	.provider-enable {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 12px 14px;
		border: 1px solid var(--border);
		border-radius: 8px;
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
		font-size: 15px;
		font-weight: 600;
	}
	.state {
		font-size: 12px;
		font-weight: 600;
		padding: 1px 8px;
		border-radius: 8px;
		background: color-mix(in srgb, var(--success) 20%, transparent);
		color: var(--success);
	}
	.provider-enable.off .state {
		background: color-mix(in srgb, var(--danger) 20%, transparent);
		color: var(--danger);
	}
	.hint {
		font-size: 12px;
		color: var(--text-secondary);
		margin: 0;
	}
</style>
