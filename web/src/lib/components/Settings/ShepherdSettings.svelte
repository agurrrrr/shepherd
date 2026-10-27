<script>
	import { onMount } from 'svelte';
	import { apiGet, apiPatch } from '$lib/api.js';
	import Icon from '$lib/components/Icon.svelte';
	import { t, translate } from '$lib/i18n/index.js';

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
		{ value: 'report', key: 'report' },
		{ value: 'plan', key: 'plan' },
		{ value: 'execute', key: 'execute' },
		{ value: 'full', key: 'full' }
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
		magi: 'MAGI'
	};

	// Only "Embedded" has words to translate; the rest are product names. Called
	// from the template, so translate() follows the current locale.
	function providerLabel(p) {
		if (p === 'embedded') return translate('shepherdSettings.provider.embedded');
		return providerLabels[p] || p;
	}

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
				loadError = res?.message || translate('shepherdSettings.loadError');
			}
		} catch (err) {
			loadError = translate('shepherdSettings.loadErrorWith', { error: err?.message || 'Unknown error' });
		}
	}

	onMount(load);

	// Model choices follow the provider selected in this tab, even before saving,
	// so switching the provider updates the list immediately. The shared
	// /api/config/model-options payload is the source; its empty-ID entry is
	// relabeled "프로바이더 기본값" because that is what an unset shepherd_model means.
	let modelChoices = $derived.by(() => {
		const list = (modelOptions && modelOptions[provider]) || data?.model_options || [];
		return list.map((o) => (o.id === '' ? { id: '', label: translate('shepherdSettings.modelDefaultOption') } : o));
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

		if (providerChanged && !confirm(translate('shepherdSettings.providerChangeConfirm'))) {
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
				msg = translate('shepherdSettings.saved');
			} else {
				msg = 'Error: ' + (res?.message || translate('shepherdSettings.saveFailed'));
			}
		} catch (err) {
			msg = 'Error: ' + (err?.message || translate('shepherdSettings.saveFailed'));
		} finally {
			busy = false;
			setTimeout(() => (msg = ''), 4000);
		}
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
			<h3 class="block-title">{$t('shepherdSettings.section.workspace')}</h3>
			<div class="setting-row column">
				<label for="sh-workspace">{$t('shepherdSettings.workspacePathLabel')}</label>
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
						<Icon name="alert-triangle" size={13} /> {$t('shepherdSettings.workspace.unset')}
					</span>
				{:else if validationError}
					<span class="hint danger"><Icon name="alert-triangle" size={13} /> {validationError}</span>
				{:else}
					<span class="hint ok"><Icon name="check" size={13} /> {$t('shepherdSettings.workspace.ok', { path: workspacePath })}</span>
				{/if}
				<span class="hint">{$t('shepherdSettings.workspace.hint')}</span>
			</div>
		</section>

		<!-- Provider: the manager sheep's row is the single source of truth. -->
		<section class="block">
			<h3 class="block-title">{$t('shepherdSettings.section.provider')}</h3>
			<div class="setting-row">
				<label for="sh-provider">{$t('shepherdSettings.providerLabel')}</label>
				<!-- bind:value on <select> listens to `change`, so any follow-up read
				     must also be on `change` (wiki: svelte5-select-bind-change-vs-input). -->
				<select id="sh-provider" class="input" bind:value={provider}>
					{#each providerChoices as p}
						<option value={p}>{providerLabel(p)}{providerEnabled[p] === false ? $t('shepherdSettings.providerDisabledSuffix') : ''}</option>
					{/each}
				</select>
			</div>
			{#if providerChanged}
				<span class="hint warn"><Icon name="alert-triangle" size={13} /> {$t('shepherdSettings.providerChangedHint')}</span>
			{:else}
				<span class="hint">{$t('shepherdSettings.providerHint')}</span>
			{/if}
		</section>

		<!-- Model: options follow the selected provider. MAGI deliberates over its
		     own three-model config, so there is nothing to pick here. -->
		{#if modelSelectable}
			<section class="block">
				<h3 class="block-title">{$t('shepherdSettings.section.model')}</h3>
				<div class="setting-row">
					<label for="sh-model">{$t('shepherdSettings.modelLabel')}</label>
					<select id="sh-model" class="input" bind:value={model}>
						{#each modelChoices as opt (opt.id)}
							<option value={opt.id}>{opt.label}</option>
						{/each}
						{#if model && !modelChoices.some((o) => o.id === model)}
							<option value={model}>{model} {$t('shepherdSettings.modelNotListed')}</option>
						{/if}
					</select>
				</div>
				<span class="hint">{$t('shepherdSettings.modelHint')}</span>
			</section>
		{:else}
			<section class="block">
				<h3 class="block-title">{$t('shepherdSettings.section.model')}</h3>
				<span class="hint">{$t('shepherdSettings.modelMagiHint')}</span>
			</section>
		{/if}

		<!-- Extra instructions: appended after the built-in shepherd guidance (#595). -->
		<section class="block">
			<h3 class="block-title">{$t('shepherdSettings.section.instructions')}</h3>
			<div class="setting-row column">
				<label for="sh-instructions">{$t('shepherdSettings.instructionsLabel')}</label>
				<textarea
					id="sh-instructions"
					class="input textarea"
					rows="6"
					bind:value={instructions}
					placeholder={$t('shepherdSettings.instructionsPlaceholder')}
				></textarea>
				<span class="hint">{$t('shepherdSettings.instructionsHint')}</span>
			</div>
		</section>

		<!-- Wake caps (#597): how often the shepherd may be woken to review
		     finished work on watched issues. 0 = daemon default. -->
		<section class="block">
			<h3 class="block-title">{$t('shepherdSettings.section.wake')}</h3>
			<div class="setting-row">
				<label for="sh-max-rounds">{$t('shepherdSettings.maxRoundsLabel')}</label>
				<input
					id="sh-max-rounds"
					class="input"
					type="number"
					min="0"
					max="100"
					bind:value={maxReviewRounds}
				/>
			</div>
			<span class="hint">{$t('shepherdSettings.maxRoundsHint')}</span>
			<div class="setting-row">
				<label for="sh-max-wakeups">{$t('shepherdSettings.maxWakeupsLabel')}</label>
				<input
					id="sh-max-wakeups"
					class="input"
					type="number"
					min="0"
					max="1000"
					bind:value={maxWakeupsPerDay}
				/>
			</div>
			<span class="hint">{$t('shepherdSettings.maxWakeupsHint')}</span>

			<!-- Attention thresholds (#599): when the panel calls a task slow or a
			     backlog stale. 0 = daemon default. -->
			<div class="setting-row">
				<label for="sh-stuck-minutes">{$t('shepherdSettings.stuckLabel')}</label>
				<input
					id="sh-stuck-minutes"
					class="input"
					type="number"
					min="0"
					max="10000"
					bind:value={stuckMinutes}
				/>
			</div>
			<span class="hint">{$t('shepherdSettings.stuckHint')}</span>
			<div class="setting-row">
				<label for="sh-pending-stale-minutes">{$t('shepherdSettings.pendingStaleLabel')}</label>
				<input
					id="sh-pending-stale-minutes"
					class="input"
					type="number"
					min="0"
					max="10000"
					bind:value={pendingStaleMinutes}
				/>
			</div>
			<span class="hint">{$t('shepherdSettings.pendingStaleHint')}</span>
		</section>

		<!-- Patrol (#600): the code re-checks the management scope on a timer and
		     wakes the shepherd only for problems that are new since the previous
		     pass. The interval is a Go duration string; "0" turns patrolling off. -->
		<section class="block">
			<h3 class="block-title">{$t('shepherdSettings.section.patrol')}</h3>
			<div class="setting-row">
				<label for="sh-patrol-interval">{$t('shepherdSettings.patrolIntervalLabel')}</label>
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
			<span class="hint">{$t('shepherdSettings.patrolIntervalHint')}</span>
			<div class="setting-row">
				<label for="sh-patrol-wake">{$t('shepherdSettings.patrolWakeLabel')}</label>
				<label class="toggle" for="sh-patrol-wake">
					<input id="sh-patrol-wake" type="checkbox" bind:checked={patrolWake} />
					<span>{patrolWake ? $t('shepherdSettings.toggleOn') : $t('shepherdSettings.toggleOffRecordOnly')}</span>
				</label>
			</div>
			<span class="hint">{$t('shepherdSettings.patrolWakeHint')}</span>
		</section>

		<!-- Report notifications (#602): shepherd tasks (검토·순찰·지시) report as
		     "목자 보고" instead of the generic "작업 완료". Quiet mode drops the
		     reports that carry no news; mute drops the generic completion notice
		     for watched-issue work so it is not reported twice. -->
		<section class="block">
			<h3 class="block-title">{$t('shepherdSettings.section.notify')}</h3>
			<div class="setting-row">
				<label for="sh-notify">{$t('shepherdSettings.notifyLabel')}</label>
				<label class="toggle" for="sh-notify">
					<input id="sh-notify" type="checkbox" bind:checked={notify} />
					<span>{notify ? $t('shepherdSettings.notifyOn') : $t('shepherdSettings.notifyOff')}</span>
				</label>
			</div>
			<span class="hint">{$t('shepherdSettings.notifyHint')}</span>
			<div class="setting-row">
				<label for="sh-notify-quiet">{$t('shepherdSettings.notifyQuietLabel')}</label>
				<label class="toggle" for="sh-notify-quiet">
					<input id="sh-notify-quiet" type="checkbox" bind:checked={notifyQuietPatrol} />
					<span>{notifyQuietPatrol ? $t('shepherdSettings.quietOn') : $t('shepherdSettings.quietOff')}</span>
				</label>
			</div>
			<span class="hint">{$t('shepherdSettings.notifyQuietHint')}</span>
			<div class="setting-row">
				<label for="sh-mute-delegated">{$t('shepherdSettings.muteDelegatedLabel')}</label>
				<label class="toggle" for="sh-mute-delegated">
					<input id="sh-mute-delegated" type="checkbox" bind:checked={muteDelegatedNotifications} />
					<span>{muteDelegatedNotifications ? $t('shepherdSettings.toggleOff') : $t('shepherdSettings.notifyOn')}</span>
				</label>
			</div>
			<span class="hint">{$t('shepherdSettings.muteDelegatedHint')}</span>
		</section>

		<!-- Autonomy (#601): how far the shepherd acts on its own. Deleting an
		     issue is never a direct tool at any tier — it only runs through a
		     user-approved request, so the panel (not this form) handles it. -->
		<section class="block">
			<h3 class="block-title">{$t('shepherdSettings.section.autonomy')}</h3>
			<div class="setting-row column">
				<label>{$t('shepherdSettings.autonomyLabel')}</label>
				<div class="tier-list">
					{#each tierChoices as tier (tier.value)}
						<label class="tier-option" class:active={autonomy === tier.value}>
							<input type="radio" name="sh-autonomy" value={tier.value} bind:group={autonomy} />
							<span class="tier-text">
								<span class="tier-label">{$t(`shepherdSettings.tier.${tier.key}`)}</span>
								<span class="tier-desc">{$t(`shepherdSettings.tier.${tier.key}Desc`)}</span>
							</span>
						</label>
					{/each}
				</div>
			</div>
			<span class="hint">{$t('shepherdSettings.autonomyHint')}</span>
			<div class="setting-row">
				<label for="sh-max-delegations">{$t('shepherdSettings.maxDelegationsLabel')}</label>
				<input
					id="sh-max-delegations"
					class="input"
					type="number"
					min="0"
					max="100"
					bind:value={maxActiveDelegations}
				/>
			</div>
			<span class="hint">{$t('shepherdSettings.maxDelegationsHint')}</span>
			<div class="setting-row">
				<label for="sh-daily-cost-limit">{$t('shepherdSettings.dailyCostLabel')}</label>
				<input
					id="sh-daily-cost-limit"
					class="input"
					type="number"
					min="0"
					step="0.5"
					bind:value={dailyCostLimitUsd}
				/>
			</div>
			<span class="hint">{$t('shepherdSettings.dailyCostHint')}</span>
		</section>

		<!-- Scope: "every visible project minus the exclusion list". A project
		     registered later joins automatically, so this is a checklist of the
		     projects that exist right now, not an allow-list. -->
		<section class="block">
			<h3 class="block-title">{$t('shepherdSettings.section.scope')}</h3>
			<div class="scope-head">
				<span class="hint">{$t('shepherdSettings.scopeHint')}</span>
				<span class="scope-actions">
					<button class="btn btn-sm" type="button" onclick={() => setAllProjects(true)}>{$t('shepherdSettings.selectAll')}</button>
					<button class="btn btn-sm" type="button" onclick={() => setAllProjects(false)}>{$t('shepherdSettings.selectNone')}</button>
				</span>
			</div>

			{#if totalProjects === 0}
				<span class="hint">{$t('shepherdSettings.noProjects')}</span>
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
								<span class="chip chip-mono">{$t('shepherdSettings.chipHidden')}</span>
							{/if}
							{#if data.workspace_path && !item.in_workspace}
								<span class="chip chip-info" title={$t('shepherdSettings.chipOutsideTitle')}>{$t('shepherdSettings.chipOutside')}</span>
							{/if}
						</li>
					{/each}
				</ul>
				<span class="hint">{$t('shepherdSettings.managedCount', { managed: managedCount, total: totalProjects })}</span>
			{/if}

			<div class="setting-row">
				<label for="sh-include-hidden">{$t('shepherdSettings.includeHiddenLabel')}</label>
				<label class="toggle" for="sh-include-hidden">
					<input id="sh-include-hidden" type="checkbox" bind:checked={includeHidden} />
					<span>{includeHidden ? $t('shepherdSettings.toggleInclude') : $t('shepherdSettings.toggleExclude')}</span>
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
				{busy ? $t('common.saving') : $t('shepherdSettings.saveButton')}
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
		font-size: var(--fs-2xs);
		font-weight: 500;
		letter-spacing: var(--tracking-caps);
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
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		line-height: 1.55;
		min-width: 0;
	}

	.hint.warn {
		color: var(--warning);
	}

	.hint.danger {
		color: var(--danger-text);
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
		border-radius: 0;
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
		font-size: var(--fs-sm);
		font-weight: 500;
		color: var(--text-primary);
	}

	.tier-desc {
		font-size: var(--fs-xs);
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
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.scope-path {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		flex: 1 1 auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.chip {
		font-size: var(--fs-2xs);
		padding: 2px 8px;
		border-radius: 0;
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
		font-size: var(--fs-sm);
		color: var(--success);
	}

	.save-msg.error {
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

		.setting-row .input {
			flex: none;
			max-width: none;
		}
	}
</style>
