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
		modelOptions = { claude: [], opencode: [], pi: [], grok: [], embedded: [] },
		/** Reports the manager's saved provider to the settings page so the 목자
		    prompt preview under this form renders the right provider (#595). */
		onProvider = () => {}
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
	// Wake caps (#597). 0 means "use the daemon default" (rounds 3 / wakeups 30).
	let maxReviewRounds = $state(0);
	let maxWakeupsPerDay = $state(0);
	// Attention thresholds (#599). 0 means "use the daemon default"
	// (stuck 120분 / pending stale 60분).
	let stuckMinutes = $state(0);
	let pendingStaleMinutes = $state(0);
	// Patrol (#600). Raw duration string ("30m"; "0" = off, nonzero clamped
	// to a 5-minute floor by the daemon) plus the wake switch.
	let patrolInterval = $state('30m');
	let patrolWake = $state(true);
	// Autonomy (#601). tier is one of report/plan/execute/full (GET returns the
	// effective value, so an unset config shows "execute"). The two caps mirror
	// the daemon: max_active_delegations 0 → default 3, daily_cost_limit_usd 0 →
	// no limit.
	let autonomy = $state('execute');
	let maxActiveDelegations = $state(0);
	let dailyCostLimitUsd = $state(0);
	// Report notifications (#602). notify / notifyQuietPatrol default true,
	// muteDelegatedNotifications defaults false. The daemon decides per report;
	// these switches only turn the whole class or the quiet rules on and off.
	let notify = $state(true);
	let notifyQuietPatrol = $state(true);
	let muteDelegatedNotifications = $state(false);

	const tierChoices = [
		{ value: 'report', label: '보고만', desc: '읽고 보고만 합니다. 이슈를 만들거나 실행하지 않습니다.' },
		{ value: 'plan', label: '계획', desc: '이슈를 만들고 고칠 수 있습니다 (issue_upsert). 실행은 요청합니다.' },
		{ value: 'execute', label: '실행', desc: '감시 이슈를 상한 안에서 실행합니다 (issue_execute). 작업 중단은 요청합니다. 기본값입니다.' },
		{ value: 'full', label: '전권', desc: '작업 중단(task_stop)까지 스스로 합니다. 이슈 삭제는 어떤 단계에서도 승인 전용입니다.' }
	];
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
		maxReviewRounds = Number(payload.max_review_rounds ?? 0);
		maxWakeupsPerDay = Number(payload.max_wakeups_per_day ?? 0);
		stuckMinutes = Number(payload.stuck_minutes ?? 0);
		pendingStaleMinutes = Number(payload.pending_stale_minutes ?? 0);
		patrolInterval = payload.patrol_interval || '30m';
		patrolWake = payload.patrol_wake !== false;
		autonomy = payload.autonomy || 'execute';
		maxActiveDelegations = Number(payload.max_active_delegations ?? 0);
		dailyCostLimitUsd = Number(payload.daily_cost_limit_usd ?? 0);
		notify = payload.notify !== false;
		notifyQuietPatrol = payload.notify_quiet_patrol !== false;
		muteDelegatedNotifications = payload.mute_delegated_notifications === true;
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

	// Report the SAVED provider upward (not the in-form selection): the 목자
	// prompt preview must show what a queued shepherd task would actually get
	// today, and re-runs whenever a save changes it (#595).
	$effect(() => {
		if (savedProvider) onProvider(savedProvider);
	});

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
				scope_include_hidden: includeHidden,
				max_review_rounds: Math.max(0, Math.trunc(Number(maxReviewRounds) || 0)),
				max_wakeups_per_day: Math.max(0, Math.trunc(Number(maxWakeupsPerDay) || 0)),
				stuck_minutes: Math.max(0, Math.trunc(Number(stuckMinutes) || 0)),
				pending_stale_minutes: Math.max(0, Math.trunc(Number(pendingStaleMinutes) || 0)),
				patrol_interval: patrolInterval.trim(),
				patrol_wake: patrolWake,
				autonomy,
				max_active_delegations: Math.max(0, Math.trunc(Number(maxActiveDelegations) || 0)),
				daily_cost_limit_usd: Math.max(0, Number(dailyCostLimitUsd) || 0),
				notify,
				notify_quiet_patrol: notifyQuietPatrol,
				mute_delegated_notifications: muteDelegatedNotifications
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

		<!-- Wake caps (#597): how often the shepherd may be woken to review
		     finished work on watched issues. 0 = daemon default. -->
		<section class="block">
			<h3 class="block-title">위임과 깨우기</h3>
			<div class="setting-row">
				<label for="sh-max-rounds">이슈당 검토 회차 상한</label>
				<input
					id="sh-max-rounds"
					class="input"
					type="number"
					min="0"
					max="100"
					bind:value={maxReviewRounds}
				/>
			</div>
			<span class="hint">감시 이슈를 목자가 검토할 수 있는 최대 횟수입니다. 넘으면 더 깨우지 않고 확인 필요로 남깁니다. 0은 기본값 3회.</span>
			<div class="setting-row">
				<label for="sh-max-wakeups">하루 깨우기 상한</label>
				<input
					id="sh-max-wakeups"
					class="input"
					type="number"
					min="0"
					max="1000"
					bind:value={maxWakeupsPerDay}
				/>
			</div>
			<span class="hint">하루(자정 기준) 목자 검토·순찰 작업 생성 상한입니다. 0은 기본값 30회.</span>

			<!-- Attention thresholds (#599): when the panel calls a task slow or a
			     backlog stale. 0 = daemon default. -->
			<div class="setting-row">
				<label for="sh-stuck-minutes">오래 실행 기준 (분)</label>
				<input
					id="sh-stuck-minutes"
					class="input"
					type="number"
					min="0"
					max="10000"
					bind:value={stuckMinutes}
				/>
			</div>
			<span class="hint">running 작업이 이 시간을 넘게 실행되면 확인 필요 항목으로 올립니다. 0은 기본값 120분.</span>
			<div class="setting-row">
				<label for="sh-pending-stale-minutes">pending 적체 기준 (분)</label>
				<input
					id="sh-pending-stale-minutes"
					class="input"
					type="number"
					min="0"
					max="10000"
					bind:value={pendingStaleMinutes}
				/>
			</div>
			<span class="hint">프로젝트에서 가장 오래된 pending 작업이 이 시간을 넘게 기다리면 적체로 봅니다. 0은 기본값 60분.</span>
		</section>

		<!-- Patrol (#600): the code re-checks the management scope on a timer and
		     wakes the shepherd only for problems that are new since the previous
		     pass. The interval is a Go duration string; "0" turns patrolling off. -->
		<section class="block">
			<h3 class="block-title">주기 순찰</h3>
			<div class="setting-row">
				<label for="sh-patrol-interval">순찰 주기</label>
				<input
					id="sh-patrol-interval"
					class="input mono"
					type="text"
					bind:value={patrolInterval}
					placeholder="30m"
					autocomplete="off"
					spellcheck="false"
				/>
			</div>
			<span class="hint">코드가 관리 범위를 점검하는 주기입니다. <code>"30m"</code>, <code>"1h"</code>처럼 입력하고 <code>"0"</code>이면 끕니다. 켜면 최소 5분입니다.</span>
			<div class="setting-row">
				<label for="sh-patrol-wake">새 이상에서 목자 깨우기</label>
				<label class="toggle" for="sh-patrol-wake">
					<input id="sh-patrol-wake" type="checkbox" bind:checked={patrolWake} />
					<span>{patrolWake ? '깨움' : '기록만'}</span>
				</label>
			</div>
			<span class="hint">직전 순찰 이후 새로 생긴 이상이 있을 때만 목자 순찰 작업을 만듭니다. 끄면 순찰 기록만 남기고 목자는 깨우지 않습니다.</span>
		</section>

		<!-- Report notifications (#602): shepherd tasks (검토·순찰·지시) report as
		     "목자 보고" instead of the generic "작업 완료". Quiet mode drops the
		     reports that carry no news; mute drops the generic completion notice
		     for watched-issue work so it is not reported twice. -->
		<section class="block">
			<h3 class="block-title">목자 보고 알림</h3>
			<div class="setting-row">
				<label for="sh-notify">목자 보고 보내기</label>
				<label class="toggle" for="sh-notify">
					<input id="sh-notify" type="checkbox" bind:checked={notify} />
					<span>{notify ? '보냄' : '안 보냄'}</span>
				</label>
			</div>
			<span class="hint">목자 작업(검토·순찰·지시)이 끝나면 "작업 완료" 대신 <strong>목자 보고</strong> 제목으로 웹 푸시와 Discord에 알립니다. 본문에는 목자의 보고 문장이 들어갑니다.</span>
			<div class="setting-row">
				<label for="sh-notify-quiet">이상 없는 순찰은 알리지 않기</label>
				<label class="toggle" for="sh-notify-quiet">
					<input id="sh-notify-quiet" type="checkbox" bind:checked={notifyQuietPatrol} />
					<span>{notifyQuietPatrol ? '조용히' : '모두 알림'}</span>
				</label>
			</div>
			<span class="hint">새 이상이 없던 순찰은 알리지 않습니다. 검토 작업이 이슈를 하나도 바꾸지 않은 경우도 알리지 않지만, 판단할 정보가 없으면 그 검토는 그대로 알립니다.</span>
			<div class="setting-row">
				<label for="sh-mute-delegated">감시 이슈의 일반 완료 알림 끄기</label>
				<label class="toggle" for="sh-mute-delegated">
					<input id="sh-mute-delegated" type="checkbox" bind:checked={muteDelegatedNotifications} />
					<span>{muteDelegatedNotifications ? '끄음' : '보냄'}</span>
				</label>
			</div>
			<span class="hint">목자가 감시하는 이슈의 작업이 끝나면 목자가 검토해서 보고하므로, 같은 일의 일반 "작업 완료" 알림을 생략합니다. 실패·사용자 중단 알림은 이 설정과 무관하게 항상 보냅니다.</span>
		</section>

		<!-- Autonomy (#601): how far the shepherd acts on its own. Deleting an
		     issue is never a direct tool at any tier — it only runs through a
		     user-approved request, so the panel (not this form) handles it. -->
		<section class="block">
			<h3 class="block-title">자율성과 상한</h3>
			<div class="setting-row column">
				<label>권한 단계</label>
				<div class="tier-list">
					{#each tierChoices as tier (tier.value)}
						<label class="tier-option" class:active={autonomy === tier.value}>
							<input type="radio" name="sh-autonomy" value={tier.value} bind:group={autonomy} />
							<span class="tier-text">
								<span class="tier-label">{tier.label}</span>
								<span class="tier-desc">{tier.desc}</span>
							</span>
						</label>
					{/each}
				</div>
			</div>
			<span class="hint">모든 요청형 동작(이슈 삭제 등)은 단계와 무관하게 목자 화면에서 사용자가 승인합니다. 프로바이더가 호출자를 식별하지 못하면 데몬은 이 단계 검사를 건너뜁니다. 그 사실은 이 설정으로 막을 수 없습니다.</span>
			<div class="setting-row">
				<label for="sh-max-delegations">동시 위임 상한</label>
				<input
					id="sh-max-delegations"
					class="input"
					type="number"
					min="0"
					max="100"
					bind:value={maxActiveDelegations}
				/>
			</div>
			<span class="hint">감시 이슈에서 동시에 pending·running으로 둘 수 있는 작업 수입니다. 넘으면 목자는 새 작업을 쌓지 않고 끝나길 기다립니다. 0은 기본값 3개.</span>
			<div class="setting-row">
				<label for="sh-daily-cost-limit">하루 비용 상한 (USD)</label>
				<input
					id="sh-daily-cost-limit"
					class="input"
					type="number"
					min="0"
					step="0.5"
					bind:value={dailyCostLimitUsd}
				/>
			</div>
			<span class="hint">하루(자정 기준) 목자 작업과 감시 이슈 작업의 합산 비용 상한입니다. 넘으면 자동 깨우기가 멈추고 확인 필요에 비용 항목이 생깁니다. 0은 제한 없음.</span>
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

	/* Autonomy tier radios (#601) — a stacked list, each row with a label and a description. */
	.tier-list {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.tier-option {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 8px 12px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius);
		cursor: pointer;
	}

	.tier-option.active {
		border-color: var(--accent);
		background: var(--bg-3);
	}

	.tier-option input[type='radio'] {
		margin-top: 3px;
		flex-shrink: 0;
	}

	.tier-text {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}

	.tier-label {
		font-size: 13px;
		font-weight: 500;
		color: var(--text-primary);
	}

	.tier-desc {
		font-size: 12px;
		color: var(--text-tertiary);
		line-height: 1.5;
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
