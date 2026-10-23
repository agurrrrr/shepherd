<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPatch } from '$lib/api.js';
	import Icon from '$lib/components/Icon.svelte';

	// Runes mode ($state below), so props come from $props().
	/** Shared /api/config payload — kept for parity with sibling tabs; this tab reads its own endpoint. */
	let {
		configData = {},
		/** Provider On/Off flags from the settings page — the provider selector lists enabled ones only. */
		providerEnabled = { claude: true, opencode: true, pi: true, grok: true, embedded: true, magi: true },
		/** Full /api/config/model-options payload — the model selector follows the selected provider. */
		modelOptions = { claude: [], opencode: [], pi: [], grok: [], embedded: [] }
	} = $props();

	// The tab owns its own state and its own save call (PATCH /api/shepherd/config).
	// It does not write into configData: the shared "Save settings" button posts a
	// whitelist of /api/config keys and would clobber these fields in between.
	let data = $state(null);
	let loadError = $state('');

	let workspacePath = $state('');
	let provider = $state('claude');
	let model = $state('');
	let instructions = $state('');
	let includeHidden = $state(false);
	/** name -> true when the project is managed (unchecked names become shepherd_scope_exclude) */
	let managed = $state({});

	let savedProvider = $state('');
	let busy = $state(false);
	let msg = $state('');
	let validationError = $state('');

	const providerLabels = {
		claude: 'Claude',
		opencode: 'OpenCode',
		pi: 'Pi',
		grok: 'Grok',
		embedded: 'Embedded (로컬 LLM)',
		magi: 'MAGI'
	};

	// Enabled providers only, plus the saved one in case it is disabled or is a
	// legacy value like "auto" — otherwise the select would show nothing and a
	// save would silently rewrite the manager's provider.
	let enabledProviders = $derived(
		Object.keys(providerLabels).filter((p) => providerEnabled[p] !== false || p === savedProvider)
	);
	let providerChoices = $derived(
		enabledProviders.includes(provider) ? enabledProviders : [provider, ...enabledProviders]
	);

	// A provider switch clears the manager session and can reset the model, so the
	// user must see that before saving (the issue asks for an explicit notice).
	let providerChanged = $derived(provider !== savedProvider);

	function applyPayload(payload) {
		data = payload;
		workspacePath = payload.workspace_path || '';
		provider = payload.provider;
		savedProvider = payload.provider;
		model = payload.model || '';
		instructions = payload.instructions || '';
		includeHidden = payload.scope_include_hidden === true;
		validationError = payload.workspace_error || '';

		const next = {};
		for (const item of payload.scope_projects || []) {
			next[item.name] = !item.excluded;
		}
		managed = next;
	}

	async function load() {
		loadError = '';
		try {
			const res = await apiGet('/api/shepherd/config');
			if (res?.data) {
				applyPayload(res.data);
			} else {
				loadError = res?.message || '목자 설정을 불러오지 못했습니다.';
			}
		} catch (err) {
			loadError = '목자 설정을 불러오지 못했습니다: ' + (err?.message || 'Unknown error');
		}
	}

	onMount(load);

	// Model choices follow the provider selected in this tab, even before saving,
	// so switching the provider updates the list immediately. The shared
	// /api/config/model-options payload is the source; its empty-ID entry is
	// relabeled "프로바이더 기본값" because that is what an unset shepherd_model means.
	let modelChoices = $derived.by(() => {
		const list = (modelOptions && modelOptions[provider]) || data?.model_options || [];
		return list.map((o) => (o.id === '' ? { id: '', label: '프로바이더 기본값' } : o));
	});

	// MAGI has no single model — it deliberates over its own consensus config.
	let modelSelectable = $derived(provider !== 'magi');

	function setAllProjects(on) {
		const next = {};
		for (const name of Object.keys(managed)) next[name] = on;
		managed = next;
	}

	let managedCount = $derived(Object.values(managed).filter(Boolean).length);
	let totalProjects = $derived(Object.keys(managed).length);

	async function save() {
		msg = '';

		if (providerChanged && !confirm('프로바이더를 바꾸면 목자 세션이 지워집니다. 새 프로바이더에 없는 모델은 기본값으로 되돌아갑니다. 계속할까요?')) {
			return;
		}

		const exclude = [];
		for (const [name, on] of Object.entries(managed)) {
			if (!on) exclude.push(name);
		}

		busy = true;
		try {
			const res = await apiPatch('/api/shepherd/config', {
				workspace_path: workspacePath.trim(),
				provider,
				model,
				instructions,
				scope_exclude: exclude,
				scope_include_hidden: includeHidden
			});
			if (res?.success && res.data) {
				applyPayload(res.data);
				msg = '저장했습니다.';
			} else {
				msg = 'Error: ' + (res?.message || '저장에 실패했습니다.');
			}
		} catch (err) {
			msg = 'Error: ' + (err?.message || '저장에 실패했습니다.');
		} finally {
			busy = false;
			setTimeout(() => (msg = ''), 4000);
		}
	}

	function providerLabel(p) {
		return providerLabels[p] || p;
	}
</script>

<div class="shepherd-settings">
	{#if loadError}
		<div class="error-banner" role="alert"><Icon name="alert-triangle" size={15} /> {loadError}</div>
	{:else if !data}
		<div class="skeleton" style="height: 320px"></div>
	{:else}
		<!-- Workspace (top): the manager's working directory. Scope is NOT derived
		     from it — projects outside it stay managed and only get a marker. -->
		<section class="block">
			<h3 class="block-title">워크스페이스</h3>
			<div class="setting-row column">
				<label for="sh-workspace">워크스페이스 경로</label>
				<input
					id="sh-workspace"
					class="input mono"
					type="text"
					bind:value={workspacePath}
					placeholder={'/home/<user>/code'}
					autocomplete="off"
					spellcheck="false"
				/>
				{#if data.workspace_unset}
					<span class="hint warn">
						<Icon name="alert-triangle" size={13} /> 목자가 일할 워크스페이스를 먼저 지정하세요.
					</span>
				{:else if validationError}
					<span class="hint danger"><Icon name="alert-triangle" size={13} /> {validationError}</span>
				{:else}
					<span class="hint ok"><Icon name="check" size={13} /> <span class="mono">{workspacePath}</span> — 목자의 작업 디렉터리입니다.</span>
				{/if}
				<span class="hint">저장할 때 <code>~</code>를 펴고 절대 경로로 정규화합니다. 워크스페이스 밖의 프로젝트도 관리 대상에 남습니다.</span>
			</div>
		</section>

		<!-- Provider: the manager sheep's row is the single source of truth. -->
		<section class="block">
			<h3 class="block-title">프로바이더</h3>
			<div class="setting-row">
				<label for="sh-provider">목자 프로바이더</label>
				<!-- bind:value on <select> listens to `change`, so any follow-up read
				     must also be on `change` (wiki: svelte5-select-bind-change-vs-input). -->
				<select id="sh-provider" class="input" bind:value={provider}>
					{#each providerChoices as p}
						<option value={p}>{providerLabel(p)}{providerEnabled[p] === false ? ' (사용 안 함)' : ''}</option>
					{/each}
				</select>
			</div>
			{#if providerChanged}
				<span class="hint warn"><Icon name="alert-triangle" size={13} /> 프로바이더를 바꾸면 목자 세션이 지워지고, 새 프로바이더에 없는 모델은 기본값으로 되돌아갑니다.</span>
			{:else}
				<span class="hint">매니저 양(<code>shepherd</code>)의 프로바이더를 저장합니다. 작업 실행 시 이 값으로 프로바이더를 고릅니다.</span>
			{/if}
		</section>

		<!-- Model: options follow the selected provider. MAGI deliberates over its
		     own three-model config, so there is nothing to pick here. -->
		{#if modelSelectable}
			<section class="block">
				<h3 class="block-title">모델</h3>
				<div class="setting-row">
					<label for="sh-model">목자 모델</label>
					<select id="sh-model" class="input" bind:value={model}>
						{#each modelChoices as opt (opt.id)}
							<option value={opt.id}>{opt.label}</option>
						{/each}
						{#if model && !modelChoices.some((o) => o.id === model)}
							<option value={model}>{model} (이 프로바이더 목록에 없음)</option>
						{/if}
					</select>
				</div>
				<span class="hint">비워 두면 프로바이더 기본 모델을 따릅니다. 임베디드 프로바이더는 엔드포인트를 고르는 것과 같습니다.</span>
			</section>
		{:else}
			<section class="block">
				<h3 class="block-title">모델</h3>
				<span class="hint">MAGI는 세 모델 합의로 동작합니다. 쓸 모델은 MAGI 탭의 합의 설정에서 정하므로, 목자에는 모델 선택이 없습니다.</span>
			</section>
		{/if}

		<!-- Extra instructions: appended after the built-in shepherd guidance (#595). -->
		<section class="block">
			<h3 class="block-title">추가 운영 지침</h3>
			<div class="setting-row column">
				<label for="sh-instructions">지침</label>
				<textarea
					id="sh-instructions"
					class="input textarea"
					rows="6"
					bind:value={instructions}
					placeholder="예: 이슈를 done으로 닫기 전에 테스트가 통과했는지 확인하라."
				></textarea>
				<span class="hint">목자 기본 지침 뒤에 이 내용이 추가됩니다. 기본 지침은 목자 프롬프트 단계(#595)에서 정해집니다.</span>
			</div>
		</section>

		<!-- Scope: "every visible project minus the exclusion list". A project
		     registered later joins automatically, so this is a checklist of the
		     projects that exist right now, not an allow-list. -->
		<section class="block">
			<h3 class="block-title">관리 범위</h3>
			<div class="scope-head">
				<span class="hint">숨기지 않은 모든 프로젝트가 관리 대상입니다. 해제한 프로젝트는 제외 목록에 들어갑니다.</span>
				<span class="scope-actions">
					<button class="btn btn-sm" type="button" onclick={() => setAllProjects(true)}>전체 선택</button>
					<button class="btn btn-sm" type="button" onclick={() => setAllProjects(false)}>전체 해제</button>
				</span>
			</div>

			{#if totalProjects === 0}
				<span class="hint">등록된 프로젝트가 없습니다.</span>
			{:else}
				<ul class="scope-list">
					{#each data.scope_projects as item (item.name)}
						<li class="scope-item" class:off={managed[item.name] === false}>
							<label class="toggle">
								<input type="checkbox" bind:checked={managed[item.name]} />
								<span class="scope-name">{item.name}</span>
							</label>
							<span class="scope-path mono" title={item.path}>{item.path}</span>
							{#if item.hidden}
								<span class="chip chip-mono">숨김</span>
							{/if}
							{#if data.workspace_path && !item.in_workspace}
								<span class="chip chip-info" title="워크스페이스 밖이지만 관리 대상입니다">워크스페이스 밖</span>
							{/if}
						</li>
					{/each}
				</ul>
				<span class="hint">{managedCount}/{totalProjects}개 관리 중</span>
			{/if}

			<div class="setting-row">
				<label for="sh-include-hidden">숨김 프로젝트 포함</label>
				<label class="toggle" for="sh-include-hidden">
					<input id="sh-include-hidden" type="checkbox" bind:checked={includeHidden} />
					<span>{includeHidden ? '포함' : '제외'}</span>
				</label>
			</div>
		</section>

		<div class="setting-actions">
			{#if msg}
				<span class="save-msg" class:error={msg.startsWith('Error')} role="status">
					<Icon name={msg.startsWith('Error') ? 'alert-triangle' : 'check'} size={14} />
					{msg}
				</span>
			{/if}
			<button class="btn btn-primary" type="button" onclick={save} disabled={busy}>
				{busy ? '저장 중…' : '목자 설정 저장'}
			</button>
		</div>
	{/if}
</div>

<style>
	.shepherd-settings {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.block {
		padding: 18px 0;
		border-top: 1px solid var(--border-subtle);
	}

	.block:first-child {
		padding-top: 4px;
		border-top: none;
	}

	.block-title {
		font-family: var(--font-mono);
		font-size: 11px;
		font-weight: 500;
		letter-spacing: 0.12em;
		text-transform: uppercase;
		color: var(--accent);
		margin: 0 0 12px;
	}

	.setting-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		column-gap: 16px;
		row-gap: 6px;
	}

	.setting-row > label:not(.toggle) {
		font-size: 13px;
		font-weight: 500;
		flex: 0 0 180px;
		color: var(--text-primary);
	}

	.setting-row .input {
		flex: 1 1 200px;
		max-width: 340px;
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

	/* Column rows stack, so the row's 200px flex-basis would become the field's height. */
	.setting-row.column .input {
		flex: none;
		max-width: none;
	}

	.textarea {
		width: 100%;
		resize: vertical;
		font-family: inherit;
		line-height: 1.5;
		padding: 8px 10px;
	}

	.hint {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 12px;
		color: var(--text-tertiary);
		line-height: 1.55;
		min-width: 0;
	}

	.hint.warn {
		color: var(--warning);
	}

	.hint.danger {
		color: var(--danger);
	}

	.hint.ok {
		color: var(--success);
	}

	.scope-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
		margin-bottom: 10px;
	}

	.scope-actions {
		display: flex;
		gap: 8px;
	}

	.scope-list {
		list-style: none;
		margin: 0 0 8px;
		padding: 0;
		display: flex;
		flex-direction: column;
	}

	.scope-item {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 10px;
		padding: 6px 0;
		border-bottom: 1px solid var(--border-subtle);
		min-width: 0;
	}

	.scope-item.off .scope-name,
	.scope-item.off .scope-path {
		color: var(--text-tertiary);
	}

	.scope-name {
		font-size: 13px;
		color: var(--text-primary);
	}

	.scope-path {
		font-size: 12px;
		color: var(--text-tertiary);
		flex: 1 1 auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.chip {
		font-size: 11px;
		padding: 2px 8px;
		border-radius: 999px;
		border: 1px solid var(--border);
		white-space: nowrap;
	}

	.setting-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 12px;
		padding-top: 18px;
		border-top: 1px solid var(--border-subtle);
	}

	.save-msg {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 13px;
		color: var(--success);
	}

	.save-msg.error {
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

		.setting-row .input {
			flex: none;
			max-width: none;
		}
	}
</style>
