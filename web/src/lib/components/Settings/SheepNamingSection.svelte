<script>
	// LLM sheep naming settings (#638 §7). This section talks ONLY to the
	// dedicated /api/config/sheep-naming endpoints — the generic PATCH
	// /api/config allowlist deliberately does not carry sheep_name_* keys,
	// because the PUT validates provider/model combinations server-side.
	import { onMount } from 'svelte';
	import { apiGet, apiPut, apiPost } from '$lib/api.js';

	let naming = $state(null);
	let loadError = $state('');
	let saveMsg = $state('');
	let savingField = $state('');

	// Preview state: a throwaway project name + the candidates the provider returns.
	let previewName = $state('');
	let previewDesc = $state('');
	let previewNames = $state([]);
	let previewMsg = $state('');
	let previewing = $state(false);

	async function loadNaming() {
		const res = await apiGet('/api/config/sheep-naming');
		if (res?.success && res.data) {
			naming = res.data;
			loadError = '';
		} else {
			loadError = res?.message || '설정을 불러오지 못했습니다.';
		}
	}

	// One field per PUT so the server's validation (provider enabled, model
	// reset on provider change) decides what lands. On failure, refetch to
	// roll the form back to the stored truth.
	async function saveField(field, value) {
		if (!naming) return;
		savingField = field;
		saveMsg = '';
		const res = await apiPut('/api/config/sheep-naming', { [field]: value });
		if (res?.success && res.data) {
			naming = res.data;
			saveMsg = '저장됨';
		} else {
			saveMsg = '저장 실패: ' + (res?.message || '알 수 없는 오류');
			await loadNaming();
		}
		savingField = '';
		setTimeout(() => { saveMsg = ''; }, 4000);
	}

	// select 바인딩은 change 이벤트로 갱신되므로 핸들러도 onchange에 붙인다
	// (wiki: svelte5-select-bind-change-vs-input).
	function onProviderChange(e) {
		saveField('provider', e.target.value);
	}
	function onModelChange(e) {
		saveField('model', e.target.value);
	}

	async function previewSuggest() {
		if (!naming) return;
		const name = previewName.trim();
		if (!name) {
			previewMsg = '미리 볼 프로젝트 이름을 입력하세요.';
			previewNames = [];
			return;
		}
		previewing = true;
		previewMsg = '';
		previewNames = [];
		const res = await apiPost('/api/sheep-names/suggest', {
			project_name: name,
			description: previewDesc.trim(),
			provider: naming.provider,
			model: naming.model
		});
		if (res?.success && res.data?.names) {
			previewNames = res.data.names;
			previewMsg = '';
		} else {
			previewMsg = '후보 생성 실패: ' + (res?.message || '알 수 없는 오류');
		}
		previewing = false;
	}

	onMount(loadNaming);
</script>

<hr class="setting-divider" />
<div class="setting-section-title">양 이름 짓기</div>

{#if loadError}
	<div class="naming-error">{loadError}</div>
{:else if !naming}
	<p class="hint">설정을 불러오는 중…</p>
{:else}
	<div class="setting-row">
		<label>LLM 작명</label>
		<label class="toggle">
			<!-- checkbox bind가 아닌 onchange로 즉시 PUT: PATCH /api/config는 이 키들을 받지 않는다 -->
			<input
				type="checkbox"
				checked={naming.enabled}
				disabled={savingField !== ''}
				onchange={(e) => saveField('enabled', e.target.checked)}
			/>
			<span>{naming.enabled ? 'Enabled' : 'Disabled'}</span>
		</label>
		<span class="hint">프로젝트 등록 시 담당 양 이름을 LLM이 짓습니다. 끄면 이름 풀(양동이, 메에롱…)만 씁니다.</span>
	</div>

	<div class="setting-row">
		<label>Provider</label>
		<!-- select는 change로 바인딩되므로 핸들러도 onchange (wiki svelte5-select-bind-change-vs-input) -->
		<select class="input" value={naming.provider} disabled={savingField !== ''} onchange={onProviderChange}>
			<option value="">— 선택 —</option>
			{#each naming.providers || [] as p}
				<option value={p}>{p}</option>
			{/each}
		</select>
		<span class="hint">magi와 auto는 선택할 수 없습니다. 켜려면 프로바이더를 지정해야 합니다.</span>
	</div>

	{#if naming.model_selectable}
		<div class="setting-row">
			<label>Model</label>
			<select class="input" value={naming.model} disabled={savingField !== ''} onchange={onModelChange}>
				<option value="">프로바이더 기본값</option>
				{#each naming.model_options || [] as m}
					<option value={m.id}>{m.label}</option>
				{/each}
			</select>
			<span class="hint">비우면 해당 프로바이더의 기본 모델을 씁니다. 프로바이더를 바꾸면 이 값은 초기화됩니다.</span>
		</div>
	{/if}

	{#if saveMsg}
		<div class="naming-savemsg" class:error={saveMsg.startsWith('저장 실패')}>{saveMsg}</div>
	{/if}

	<!-- 미리 보기: 저장된 설정으로 실제 LLM 호출을 한 번 해 본다 (30초 초과 가능) -->
	<div class="setting-row column">
		<label>미리 보기</label>
		<div class="naming-preview-row">
			<input class="input" type="text" bind:value={previewName} placeholder="예: tangbisil" />
			<input class="input" type="text" bind:value={previewDesc} placeholder="설명 (선택)" />
			<button class="btn" onclick={previewSuggest} disabled={previewing || !naming.provider}>
				{previewing ? '생성 중… (최대 30초)' : '후보 보기'}
			</button>
		</div>
		{#if previewMsg}
			<span class="hint">{previewMsg}</span>
		{/if}
		{#if previewNames.length > 0}
			<div class="naming-chips">
				{#each previewNames as n}
					<span class="naming-chip">{n}</span>
				{/each}
			</div>
		{/if}
	</div>
{/if}

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

	.setting-row .input {
		flex: 1 1 200px;
		max-width: 340px;
		min-width: 0;
	}

	.setting-row:not(.column) > .hint {
		flex: 0 0 calc(100% - 196px);
		margin-left: 196px;
		min-width: 0;
	}

	.setting-row.column {
		flex-direction: column;
		align-items: stretch;
		gap: 8px;
	}

	.setting-row.column > label:not(.toggle) {
		flex: none;
	}

	.setting-row.column .input {
		flex: none;
		max-width: none;
	}

	.setting-divider {
		border: none;
		border-top: 1px solid var(--border);
		margin: 8px 0;
	}

	.setting-section-title {
		font-size: var(--fs-base);
		font-weight: 600;
		color: var(--text-primary);
		padding-top: 12px;
		margin-bottom: 4px;
	}

	.toggle {
		display: flex;
		align-items: center;
		gap: 8px;
		cursor: pointer;
		font-weight: 400;
		min-width: 0;
	}

	.hint {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		line-height: 1.55;
	}

	.naming-error,
	.naming-savemsg {
		font-size: var(--fs-sm);
		color: var(--text-secondary);
		margin: 4px 0;
	}

	.naming-error,
	.naming-savemsg.error {
		color: var(--danger-text);
	}

	.naming-preview-row {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
		align-items: center;
	}

	.naming-preview-row .input {
		flex: 1 1 160px;
		min-width: 0;
	}

	.naming-chips {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
		margin-top: 4px;
	}

	.naming-chip {
		font-family: var(--font-mono);
		font-size: var(--fs-xs);
		padding: 2px 8px;
		background: var(--well);
		border: 0;
		box-shadow: var(--px-ring);
		color: var(--text-primary);
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

		.setting-row .input {
			flex: none;
			max-width: none;
		}

		.setting-row:not(.column) > .hint {
			flex: 0 0 100%;
			margin-left: 0;
		}
	}
</style>
