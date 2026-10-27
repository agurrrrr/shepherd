<script>
	import { apiPost } from '$lib/api.js';
	import { refreshShepherdAttention } from '$lib/attention.js';
	import { shepherdAttention } from '$lib/stores.js';
	import { timeAgo } from '$lib/shepherdFormat.js';
	import { t, enumLabel, translate } from '$lib/i18n/index.js';
	import Icon from '$lib/components/Icon.svelte';

	// Needs-attention panel (#599). GET /api/shepherd/attention returns items
	// already sorted by severity desc (the backend owns that order — the panel
	// must not re-sort). Each item carries an action_kind telling the panel
	// which button to render:
	//   retry_task    → POST /api/tasks/:target_id/retry
	//   stop_task     → POST /api/tasks/:target_id/stop
	//   reset_sheep   → POST /api/sheep/:target_name/reset
	//   open_project  → link to /projects/:target_name
	//   open_issue    → link handled by the page (opens the board's detail)
	//   open_settings → link to /settings (목자 탭)
	// "확인함" posts the fingerprint to /api/shepherd/attention/ack; the item
	// disappears until its fingerprint changes (new failure, new round...).
	//
	// The #601 section on top lists pending shepherd requests
	// (GET /api/shepherd/requests?status=pending). Approving runs the requested
	// action server-side (POST .../approve), rejecting records a note
	// (POST .../reject) — either decision wakes the shepherd for review.

	let { onOpenIssue = () => {}, tick = 0 } = $props();

	let items = $state([]);
	let requests = $state([]);
	let loading = $state(false);
	let error = $state('');
	/** fingerprint -> true while its action/ack request is in flight */
	let busyMap = $state({});
	/** request id -> true while its approve/reject request is in flight */
	let requestBusy = $state({});

	const kindIcons = {
		failed_task: 'x-circle',
		long_running: 'hourglass',
		circuit_open: 'zap',
		pending_backlog: 'clock',
		review_limit: 'alert-triangle',
		user_stopped: 'pause-circle',
		shepherd_blocked: 'alert-triangle',
		cost_limit: 'zap'
	};

	async function load() {
		loading = true;
		error = '';
		// Shared fetch (#623): the store count (items + pending requests) is
		// what the sidebar badge and the tab badge read, so the panel consumes
		// the same result instead of firing its own request pair.
		const { items: result, requests: reqResult } = await refreshShepherdAttention();
		if (result === null) {
			error = translate('attention.loadError');
			items = [];
		} else {
			items = result;
		}
		requests = reqResult;
		loading = false;
	}

	async function decideRequest(req, decision) {
		requestBusy = { ...requestBusy, [req.id]: true };
		error = '';
		try {
			const body = decision === 'reject' ? { note: translate('attention.rejectNote') } : {};
			const res = await apiPost(`/api/shepherd/requests/${req.id}/${decision}`, body);
			if (!res?.success) {
				error = res?.message || translate('attention.requestFailed');
			}
		} catch (e) {
			error = translate('attention.requestFailedWith', { error: e?.message || e });
		} finally {
			requestBusy = { ...requestBusy, [req.id]: false };
			load();
		}
	}

	function requestTargetLabel(req) {
		if (req.kind === 'stop_task' && req.target_id) return translate('attention.targetTask', { id: req.target_id });
		if ((req.kind === 'execute_issue' || req.kind === 'delete_issue') && req.target_id) {
			return `${translate('attention.targetIssue', { id: req.target_id })}${req.project_name ? ' · ' + req.project_name : ''}`;
		}
		return '';
	}

	// Mount load, plus a re-load whenever the page bumps `tick` (SSE debounce
	// refreshes board and panel together).
	let first = true;
	$effect(() => {
		tick; // dependency: the page bumps it after debounced SSE events
		if (first) {
			first = false;
			load();
			return;
		}
		load();
	});

	function setBusy(fp, on) {
		busyMap = { ...busyMap, [fp]: on };
	}

	async function runAction(item) {
		const url = actionUrl(item);
		if (!url) return;
		setBusy(item.fingerprint, true);
		try {
			const res = await apiPost(url, {});
			if (!res?.success) {
				error = res?.message || translate('attention.actionFailed');
			}
		} catch (e) {
			error = translate('attention.actionFailedWith', { error: e?.message || e });
		} finally {
			setBusy(item.fingerprint, false);
			load();
		}
	}

	async function ack(item) {
		setBusy(item.fingerprint, true);
		try {
			const res = await apiPost('/api/shepherd/attention/ack', { fingerprint: item.fingerprint });
			if (!res?.success) {
				error = res?.message || translate('attention.ackFailed');
			}
		} catch (e) {
			error = translate('attention.ackFailedWith', { error: e?.message || e });
		} finally {
			setBusy(item.fingerprint, false);
			load();
		}
	}

	function actionUrl(item) {
		switch (item.action_kind) {
			case 'retry_task': return `/api/tasks/${item.target_id}/retry`;
			case 'stop_task': return `/api/tasks/${item.target_id}/stop`;
			case 'reset_sheep': return `/api/sheep/${encodeURIComponent(item.target_name)}/reset`;
			default: return null;
		}
	}

	function actionLabel(kind) {
		const label = enumLabel($t, 'attention.action', kind);
		return label === String(kind) ? '' : label;
	}

	function linkHref(item) {
		switch (item.action_kind) {
			case 'open_project': return `/projects/${encodeURIComponent(item.target_name)}`;
			case 'open_settings': return '/settings';
			default: return null;
		}
	}
</script>

<div class="attention-panel card">
	<div class="panel-head">
		<span class="eyebrow">Needs attention</span>
		{#if $shepherdAttention > 0}
			<!-- Same store the sidebar badge and the tab badge read (#623):
			     items + pending requests, so all three numbers match. -->
			<span class="count chip chip-danger mono num" aria-label={$t('attention.badgeAria', { count: $shepherdAttention })}>{$shepherdAttention}</span>
		{/if}
		<span class="panel-head-spacer"></span>
		<button class="btn btn-sm btn-ghost" onclick={load} aria-label={$t('common.refresh')}><Icon name="refresh-cw" size={13} /></button>
	</div>

	{#if error}
		<div class="panel-error" role="alert"><Icon name="alert-triangle" size={14} /> {error}</div>
	{/if}

	<!-- #623: the panel used to live in a 340px side column. On the 확인 필요
	     tab it owns the full width, so wide screens split the two zones into
	     columns and cap each column's measure so sentences stay readable.
	     Narrow screens keep the stacked order (requests first, then items). -->
	<div class="panel-zones">
		{#if requests.length > 0}
			<section class="zone zone-requests" aria-label={$t('attention.aria.requests')}>
				<div class="request-head">
					<span class="eyebrow">Shepherd requests</span>
					<span class="count chip chip-danger mono num" aria-label={$t('attention.requestsBadgeAria', { count: requests.length })}>{requests.length}</span>
				</div>
				<ul class="request-list">
					{#each requests as req (req.id)}
						<li class="request-item" class:busy={requestBusy[req.id]}>
							<span class="request-icon" aria-hidden="true">
								<Icon name={kindIcons.cost_limit} size={15} />
							</span>
							<div class="request-body">
								<div class="request-title-row">
									<strong class="request-kind">{enumLabel($t, 'attention.requestKind', req.kind)}</strong>
									{#if req.requester_sheep}
										<span class="request-chip chip">{req.requester_sheep}</span>
									{/if}
									{#if req.project_name}
										<span class="request-chip chip">{req.project_name}</span>
									{/if}
									{#if req.created_at && !String(req.created_at).startsWith('0001')}
										<span class="request-time mono num" title={req.created_at}>{timeAgo(req.created_at)}</span>
									{/if}
								</div>
								{#if requestTargetLabel(req)}
									<p class="request-target mono">{requestTargetLabel(req)}</p>
								{/if}
								{#if req.reason}
									<p class="request-reason">{req.reason}</p>
								{/if}
								<div class="request-actions">
									<button
										class="btn btn-sm btn-primary"
										onclick={() => decideRequest(req, 'approve')}
										disabled={requestBusy[req.id]}
									>{$t('attention.approve')}</button>
									<button
										class="btn btn-sm btn-ghost reject"
										onclick={() => decideRequest(req, 'reject')}
										disabled={requestBusy[req.id]}
									>{$t('attention.reject')}</button>
								</div>
							</div>
						</li>
					{/each}
				</ul>
			</section>
		{/if}

		<section class="zone zone-items" aria-label={$t('attention.aria.items')}>
			{#if loading && items.length === 0}
				<div class="panel-loading" aria-busy="true">
					{#each Array(3) as _}
						<div class="skeleton" style="height: 52px"></div>
					{/each}
				</div>
			{:else if items.length === 0}
				<div class="panel-empty">
					<Icon name="check-circle" size={20} />
					<p>{$t('attention.empty')}</p>
				</div>
			{:else}
				<ul class="item-list">
					{#each items as item (item.fingerprint)}
						<li class="item sev-{Math.min(item.severity, 7)}" class:busy={busyMap[item.fingerprint]}>
							<span class="item-icon" aria-hidden="true">
								<Icon name={kindIcons[item.kind] || 'alert-triangle'} size={15} />
							</span>
							<div class="item-body">
								<div class="item-title-row">
									<strong class="item-title">{item.title}</strong>
									{#if item.project}
										<span class="item-project chip">{item.project}</span>
									{/if}
									{#if item.created_at && !item.created_at.startsWith('0001')}
										<span class="item-time mono num" title={item.created_at}>{timeAgo(item.created_at)}</span>
									{/if}
								</div>
								<p class="item-message">{item.message}</p>
								<div class="item-actions">
									{#if actionUrl(item.action_kind) || item.action_kind === 'reset_sheep'}
										<button
											class="btn btn-sm"
											class:btn-danger={item.action_kind === 'stop_task'}
											onclick={() => runAction(item)}
											disabled={busyMap[item.fingerprint]}
										>{actionLabel(item.action_kind)}</button>
									{/if}
									{#if item.action_kind === 'open_issue' && item.target_id}
										<button class="btn btn-sm" onclick={() => onOpenIssue(item)}>{$t('attention.openIssue')}</button>
									{/if}
									{#if linkHref(item)}
										<a class="btn btn-sm" href={linkHref(item)}>
											{item.action_kind === 'open_settings' ? $t('attention.openSettings') : $t('attention.openProject')}
											<Icon name="arrow-up-right" size={12} />
										</a>
									{/if}
									{#if item.target_id && (item.action_kind === 'retry_task' || item.action_kind === 'stop_task')}
										<a class="btn btn-sm btn-ghost" href="/tasks/{item.target_id}?from=shepherd&tab=attention">{$t('attention.taskDetail')}</a>
									{/if}
									<span class="item-actions-spacer"></span>
									<button
										class="btn btn-sm btn-ghost ack"
										onclick={() => ack(item)}
										disabled={busyMap[item.fingerprint]}
										title={$t('attention.ackTitle')}
									><Icon name="check" size={12} stroke={2.4} /> {$t('attention.ack')}</button>
								</div>
							</div>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	</div>
</div>

<style>
	.attention-panel {
		display: flex;
		flex-direction: column;
		padding: 14px 16px;
		min-height: 120px;
	}

	.panel-head {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 10px;
	}

	.eyebrow {
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.count { min-width: 22px; text-align: center; }

	.panel-head-spacer { flex: 1; }

	.panel-error {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 8px;
		color: var(--danger-text);
		font-size: var(--fs-xs);
	}

	/* Full-width layout (#623): requests and items sit side by side on wide
	   screens, each capped at a readable measure instead of stretching the
	   sentence across the whole tab. Narrow screens stack them. */
	.panel-zones {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(min(420px, 100%), 1fr));
		gap: 16px;
		align-items: start;
	}

	.zone { min-width: 0; }

	.zone-items { max-width: 72ch; }

	.zone-requests { max-width: 64ch; }

	.panel-loading {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.panel-empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		padding: 22px 0;
		color: var(--text-tertiary);
	}

	.panel-empty p { margin: 0; font-size: var(--fs-sm); }

	.item-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.item {
		display: flex;
		gap: 10px;
		padding: 10px 12px;
		border: 1px solid var(--border-subtle);
		border-left: 3px solid var(--text-tertiary);
		border-radius: 0;
		background: var(--bg-3);
	}

	.item.busy { opacity: 0.6; }

	/* Severity tint on the left rule (backend order = list order). */
	.item.sev-7, .item.sev-6 { border-left-color: var(--danger); }
	.item.sev-5 { border-left-color: var(--warning); }
	.item.sev-4 { border-left-color: var(--queued); }
	.item.sev-3, .item.sev-2 { border-left-color: var(--accent); }

	.item-icon {
		flex-shrink: 0;
		margin-top: 2px;
		color: var(--text-secondary);
	}

	.sev-7 .item-icon, .sev-6 .item-icon { color: var(--danger-text); }
	.sev-5 .item-icon { color: var(--warning); }

	.item-body { flex: 1; min-width: 0; }

	.item-title-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}

	.item-title {
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.item-project { font-size: var(--fs-2xs); }

	.item-time {
		margin-left: auto;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.item-message {
		margin: 4px 0 0;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		word-break: keep-all;
	}

	.item-actions {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
		margin-top: 8px;
	}

	.item-actions-spacer { flex: 1; }

	.ack { color: var(--text-tertiary); }
	.ack:hover { color: var(--success); }

	/* Shepherd requests (#601) — the approve/reject queue on top of the panel. */
	.request-head {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 8px;
	}

	.request-list {
		list-style: none;
		margin: 0 0 12px;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.request-item {
		display: flex;
		gap: 10px;
		padding: 10px 12px;
		border: 1px solid var(--border-subtle);
		border-left: 3px solid var(--accent);
		border-radius: 0;
		background: var(--bg-3);
	}

	.request-item.busy { opacity: 0.6; }

	.request-icon {
		flex-shrink: 0;
		margin-top: 2px;
		color: var(--accent);
	}

	.request-body { flex: 1; min-width: 0; }

	.request-title-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}

	.request-kind {
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.request-chip { font-size: var(--fs-2xs); }

	.request-time {
		margin-left: auto;
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.request-target {
		margin: 4px 0 0;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
	}

	.request-reason {
		margin: 4px 0 0;
		font-size: var(--fs-xs);
		color: var(--text-secondary);
		word-break: keep-all;
	}

	.request-actions {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
		margin-top: 8px;
	}

	.reject { color: var(--text-tertiary); }
	.reject:hover { color: var(--danger-text); }
</style>
