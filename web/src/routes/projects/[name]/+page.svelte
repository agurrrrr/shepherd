<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiDelete } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { appendLiveOutput } from '$lib/liveOutput.js';
	import { thinkingByProject, modelByProject } from '$lib/stores.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import MagiStreamPanel from '$lib/components/MagiStreamPanel.svelte';
	import CommandInput from '$lib/components/CommandInput.svelte';
	import Pagination from '$lib/components/Pagination.svelte';
	import GitGraph from '$lib/components/GitGraph.svelte';
	import ScheduleForm from '$lib/components/ScheduleForm.svelte';
	import SkillForm from '$lib/components/SkillForm.svelte';
	import FileBrowser from '$lib/components/FileBrowser.svelte';
	import IssueTab from '$lib/components/IssueTab.svelte';
	import ProjectSettings from '$lib/components/ProjectSettings.svelte';

	let projectName = $state('');
	let project = $state(null);
	let loading = $state(true);
	let activeTab = $state('output');

	// Live output
	let liveOutput = $state([]);
	// Tracks whether the last liveOutput entry is an incomplete stream chunk
	// (no trailing newline) so the next SSE payload appends instead of splitting.
	let liveOutputOpen = $state(false);
	let sheepStatus = $state('idle');
	let sheepName = $state('');
	let sheepProvider = $state('claude');
	// Whether this project has a task in the 'running' state. Used as a fallback
	// so Stop/Inject buttons show even when sheepStatus desyncs to 'idle' while a
	// task is still marked running (e.g. after a server restart or missed SSE).
	let hasRunningTask = $state(false);
	// ID of the currently running task, displayed on the Working badge.
	let runningTaskId = $state(null);
	// Global OpenCode "thinking" default; per-project override lives in
	// $thinkingByProject and wins when the user has explicitly toggled.
	let opencodeThinkingDefault = $state(false);
	let thinkingChecked = $derived.by(() => {
		const overrides = $thinkingByProject || {};
		if (projectName && Object.prototype.hasOwnProperty.call(overrides, projectName)) {
			return !!overrides[projectName];
		}
		return opencodeThinkingDefault;
	});

	function toggleThinking(e) {
		const checked = e.target.checked;
		thinkingByProject.update((m) => ({ ...(m || {}), [projectName]: checked }));
	}

	// Global model defaults per provider; per-project override lives in $modelByProject.
	let opencodeModelDefault = $state('');
	let opencodeModelOptions = $state([]);
	let piModelDefault = $state('');
	let piModelOptions = $state([]);
	let grokModelDefault = $state('');
	let grokModelOptions = $state([]);
	let embeddedModelOptions = $state([]);

	// Providers that take an explicit model selector. Claude uses a fixed CLI
	// model, so it has no per-project selector here. Embedded gets a model
	// selector (the configured endpoints) but no Thinking toggle — its thinking
	// is configured per endpoint in Settings.
	let providerHasModel = $derived(
		sheepProvider === 'opencode' ||
		sheepProvider === 'pi' || sheepProvider === 'grok' || sheepProvider === 'embedded'
	);
	// Thinking toggle applies only to the CLI-harness providers, not embedded.
	let providerHasThinking = $derived(
		sheepProvider === 'opencode' || sheepProvider === 'pi'
	);

	// Provider 사용유무 (설정에서 끄면 선택지에서 숨김). 기본 모두 켜짐.
	let providerEnabled = $state({ claude: true, opencode: true, pi: true, grok: true, embedded: true, magi: true });
	const PROVIDER_OPTIONS = [
		{ value: 'claude', label: 'Claude' },
		{ value: 'opencode', label: 'OpenCode' },
		{ value: 'pi', label: 'Pi' },
		{ value: 'grok', label: 'Grok' },
		{ value: 'embedded', label: 'Embedded' },
		{ value: 'magi', label: 'MAGI 🧠' }
	];
	// Model option list and global default for the sheep's current provider.
	let activeModelOptions = $derived(
		sheepProvider === 'pi' ? piModelOptions :
		sheepProvider === 'grok' ? grokModelOptions :
		sheepProvider === 'embedded' ? embeddedModelOptions :
		opencodeModelOptions
	);
	let activeModelDefault = $derived(
		sheepProvider === 'pi' ? piModelDefault :
		sheepProvider === 'grok' ? grokModelDefault :
		sheepProvider === 'embedded' ? '' :
		opencodeModelDefault
	);

	let modelSelected = $derived.by(() => {
		const overrides = $modelByProject || {};
		if (projectName && Object.prototype.hasOwnProperty.call(overrides, projectName)) {
			const v = overrides[projectName];
			if (v) return v;
		}
		return activeModelDefault;
	});

	function changeModel(e) {
		const value = e.target.value;
		modelByProject.update((m) => ({ ...(m || {}), [projectName]: value || null }));
	}

	// Task history
	let tasks = $state([]);
	let taskPage = $state(1);
	let taskTotal = $state(0);
	let taskTotalPages = $state(1);
	let taskLimit = 10;
	let tasksLoaded = $state(false);
	let taskSearchQuery = $state('');

	// Documents
	let docs = $state([]);
	// Git
	let gitLoaded = $state(false);

	// Schedules
	let projectSchedules = $state([]);
	let schedulesLoaded = $state(false);
	let showScheduleForm = $state(false);
	let editingSchedule = $state(null);

	// Skills
	let projectSkills = $state([]);
	let skillsLoaded = $state(false);
	let showSkillForm = $state(false);
	let editingSkillItem = $state(null);

	// Files tab key — changes to force FileBrowser remount on project switch
	let filesKey = $state(0);

	// Wiki
	let wikiPages = $state([]);
	let wikiLoaded = $state(false);
	let wikiSelectedPage = $state(null);
	let wikiEditing = $state(false);
	let wikiEditContent = $state('');
	let wikiEditTitle = $state('');

	let unsubs = [];

	const VALID_TABS = ['output', 'history', 'files', 'git', 'schedules', 'skills', 'issues', 'wiki', 'settings'];

	// React to route param changes
	$effect(() => {
		const newName = decodeURIComponent($page.params.name);
		if (newName !== projectName) {
			projectName = newName;
			if (typeof window !== 'undefined') {
				const requestedTab = $page.url.searchParams.get('tab');
				resetAndLoad(requestedTab);
			}
		}
	});

	function resetAndLoad(initialTab = null) {
		project = null;
		loading = true;
		liveOutput = [];
		liveOutputOpen = false;
		sheepStatus = 'idle';
		hasRunningTask = false;
		runningTaskId = null;
		sheepName = '';
		tasks = [];
		tasksLoaded = false;
		taskPage = 1;
		filesKey++; // force FileBrowser remount
		gitLoaded = false;
		projectSchedules = [];
		schedulesLoaded = false;
		showScheduleForm = false;
		editingSchedule = null;
		projectSkills = [];
		skillsLoaded = false;
		showSkillForm = false;
		editingSkillItem = null;
		activeTab = 'output';
		loadProject();
		if (initialTab && initialTab !== 'output' && VALID_TABS.includes(initialTab)) {
			switchTab(initialTab);
		}
	}

	onMount(() => {
		// SSE: live output (5000줄 버퍼). Incomplete chunks (no trailing \n)
		// append to the previous line so Grok/token streams don't fragment.
		unsubs.push(onSSE('output', (data) => {
			if (data.project_name === projectName) {
				const state = { open: liveOutputOpen };
				liveOutput = appendLiveOutput(liveOutput, data.text, state, 5000);
				liveOutputOpen = state.open;
			}
		}));

		// SSE: status change
		unsubs.push(onSSE('status_change', (data) => {
			if (sheepName && data.sheep_name === sheepName) {
				sheepStatus = data.status;
			}
		}));

		// SSE: provider change (rate limit fallback / restore / manual)
		unsubs.push(onSSE('provider_change', (data) => {
			if (sheepName && data.sheep_name === sheepName) {
				sheepProvider = data.provider;
			}
		}));

		// SSE reconnect: events broadcast while disconnected are lost, so refetch
		// the authoritative sheep status (otherwise a finished task can leave the
		// UI stuck on "working" with the Stop button showing).
		unsubs.push(onSSE('reconnect', () => resyncSheep()));

		// Mobile browsers suspend EventSource when backgrounded and may not fire
		// onerror, so the reconnect event can be missed too. Re-sync whenever the
		// tab becomes visible again as a safety net.
		if (typeof document !== 'undefined') {
			const onVisible = () => { if (document.visibilityState === 'visible') resyncSheep(); };
			document.addEventListener('visibilitychange', onVisible);
			unsubs.push(() => document.removeEventListener('visibilitychange', onVisible));
		}

		// SSE: task events -> refresh history
		unsubs.push(onSSE('task_complete', (data) => {
			if (data.project_name === projectName) {
				hasRunningTask = false;
				runningTaskId = null;
				if (tasksLoaded) loadTasks();
			}
		}));
		unsubs.push(onSSE('task_fail', (data) => {
			if (data.project_name === projectName) {
				hasRunningTask = false;
				runningTaskId = null;
				if (tasksLoaded) loadTasks();
			}
		}));
		unsubs.push(onSSE('task_stop', (data) => {
			if (data.project_name === projectName) {
				hasRunningTask = false;
				runningTaskId = null;
			}
		}));
		unsubs.push(onSSE('task_start', (data) => {
			if (data.project_name === projectName) {
				// 새 작업 시작: 이전 출력 정리 후 프롬프트 표시
				liveOutput = [`▶ ${data.prompt}`, ''];
				liveOutputOpen = false;
				hasRunningTask = true;
				runningTaskId = data.task_id;
				if (tasksLoaded) loadTasks();
			}
		}));
	});

	onDestroy(() => unsubs.forEach(fn => fn()));

	async function loadProject() {
		const [res, configRes, modelRes] = await Promise.all([
			apiGet(`/api/projects/${encodeURIComponent(projectName)}`),
			apiGet('/api/config'),
			apiGet('/api/config/model-options')
		]);
		if (configRes?.data) {
			opencodeThinkingDefault = !!configRes.data.opencode_thinking_default;
			opencodeModelDefault = configRes.data.model_opencode || '';
			piModelDefault = configRes.data.model_pi || '';
			grokModelDefault = configRes.data.model_grok || '';
			providerEnabled = {
				claude: configRes.data.provider_enabled_claude !== false,
				opencode: configRes.data.provider_enabled_opencode !== false,
				pi: configRes.data.provider_enabled_pi !== false,
				grok: configRes.data.provider_enabled_grok !== false,
				embedded: configRes.data.provider_enabled_embedded !== false,
				magi: configRes.data.provider_enabled_magi !== false
			};
		}
		if (modelRes?.data) {
			opencodeModelOptions = modelRes.data.opencode || [];
			piModelOptions = modelRes.data.pi || [];
			grokModelOptions = modelRes.data.grok || [];
			embeddedModelOptions = modelRes.data.embedded || [];
		}
		if (res?.data) {
			project = res.data;
			sheepName = project.sheep || '';

			if (sheepName) {
				const sheepRes = await apiGet(`/api/sheep/${encodeURIComponent(sheepName)}`);
				if (sheepRes?.data) {
					sheepStatus = sheepRes.data.status;
					sheepProvider = sheepRes.data.provider || 'claude';
				}
			}

			await loadLatestOutput();
			await refreshRunningTask();
		}
		loading = false;
	}

	// Refetch authoritative sheep status/provider after a missed-events window
	// (SSE reconnect or tab refocus). Cheap and idempotent.
	async function resyncSheep() {
		if (!sheepName) return;
		const sheepRes = await apiGet(`/api/sheep/${encodeURIComponent(sheepName)}`);
		if (sheepRes?.data) {
			sheepStatus = sheepRes.data.status;
			sheepProvider = sheepRes.data.provider || 'claude';
		}
		await refreshRunningTask();
	}

	// Authoritative check for whether a task is currently running for this project.
	// Drives the Stop/Inject button fallback when sheepStatus is out of sync.
	async function refreshRunningTask() {
		if (!projectName) return;
		const res = await apiGet(`/api/tasks?project=${encodeURIComponent(projectName)}&status=running&limit=1`);
		hasRunningTask = (res?.data?.length || 0) > 0;
		runningTaskId = hasRunningTask ? res.data[0].id : null;
	}

	async function changeProvider(provider) {
		await apiPatch(`/api/sheep/${encodeURIComponent(sheepName)}/provider`, { provider });
		sheepProvider = provider;
	}

	async function loadLatestOutput() {
		const res = await apiGet(`/api/tasks?project=${encodeURIComponent(projectName)}&limit=1`);
		if (res?.data?.length > 0) {
			const taskRes = await apiGet(`/api/tasks/${res.data[0].id}`);
			if (taskRes?.data?.output) {
				liveOutput = taskRes.data.output;
				// Historical lines are complete entries; do not treat last as open.
				liveOutputOpen = false;
			}
		}
	}

	async function loadTasks() {
		const params = new URLSearchParams();
		params.set('project', projectName);
		params.set('page', taskPage);
		params.set('limit', taskLimit);
		if (taskSearchQuery.trim()) params.set('q', taskSearchQuery.trim());
		const res = await apiGet(`/api/tasks?${params}`);
		if (res) {
			tasks = res.data || [];
			taskTotal = res.total || 0;
			taskTotalPages = res.total_pages || 1;
		}
		tasksLoaded = true;
	}

	let retryingId = $state(null);
	let retryFromId = $state(null);

	async function retryTask(e, taskId) {
		e.preventDefault();
		e.stopPropagation();
		if (retryingId) return;
		retryingId = taskId;
		const res = await apiPost(`/api/tasks/${taskId}/retry`);
		retryingId = null;
		if (res?.success) {
			loadTasks();
		}
	}

	async function retryFromTask(e, taskId) {
		e.preventDefault();
		e.stopPropagation();
		if (retryFromId) return;
		retryFromId = taskId;
		const res = await apiPost(`/api/tasks/${taskId}/retry-from`);
		retryFromId = null;
		if (res?.success) {
			loadTasks();
		}
	}

	function switchTab(tab) {
		activeTab = tab;
		if (tab === 'history' && !tasksLoaded) {
			loadTasks();
		}
		// Files tab: FileBrowser component manages its own state
		if (tab === 'git' && !gitLoaded) {
			gitLoaded = true;
		}
		if (tab === 'schedules' && !schedulesLoaded) {
			loadSchedules();
		}
		if (tab === 'skills' && !skillsLoaded) {
			loadProjectSkills();
		}
		if (tab === 'wiki' && !wikiLoaded) {
			loadWikiPages();
		}
	}

	// Wiki functions
	async function loadWikiPages() {
		const res = await apiGet(`/api/wiki/pages?project=${encodeURIComponent(projectName)}`);
		if (res?.data) {
			wikiPages = res.data;
			if (res.data.length > 0 && !wikiSelectedPage) {
				selectWikiPage(res.data[0]);
			}
		}
		wikiLoaded = true;
	}

	function selectWikiPage(page) {
		wikiSelectedPage = page;
		wikiEditing = false;
	}

	function openWikiEdit() {
		if (!wikiSelectedPage) return;
		wikiEditing = true;
		wikiEditContent = wikiSelectedPage.content || '';
		wikiEditTitle = wikiSelectedPage.title || '';
	}

	function closeWikiEdit() {
		wikiEditing = false;
		wikiEditContent = '';
		wikiEditTitle = '';
	}

	async function saveWikiPage() {
		if (!wikiSelectedPage) return;
		const res = await apiPatch(`/api/wiki/pages/${encodeURIComponent(wikiSelectedPage.slug)}?project=${encodeURIComponent(projectName)}`, {
			content: wikiEditContent,
			title: wikiEditTitle
		});
		if (res?.success) {
			wikiEditing = false;
			await loadWikiPages();
			selectWikiPage(wikiSelectedPage);
		}
	}

	function onTaskPageChange(p) {
		taskPage = p;
		loadTasks();
	}

	function onTaskSearch(e) {
		if (e.key === 'Enter') {
			taskPage = 1;
			loadTasks();
		}
	}

	function truncate(s, max) {
		if (!s) return '';
		return s.length > max ? s.slice(0, max) + '...' : s;
	}

	function truncateModel(label) {
		if (!label) return '';
		// "devstral-small-2 (local-llm/devstral-small-2)" → "devstral-small-2"
		const paren = label.indexOf(' (');
		if (paren !== -1) return label.slice(0, paren);
		// "local-llm / devstral-small-2" → "devstral-small-2"
		const sep = label.indexOf(' / ');
		if (sep !== -1) return label.slice(sep + 3);
		// "local-llm/devstral-small-2" → "devstral-small-2"
		const slash = label.indexOf('/');
		if (slash !== -1) return label.slice(slash + 1);
		return label;
	}

	function formatTime(ts) {
		if (!ts) return '';
		try {
			return new Date(ts).toLocaleString();
		} catch {
			return ts;
		}
	}

	// Schedule functions
	async function loadSchedules() {
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/schedules`);
		if (res?.data) {
			projectSchedules = res.data;
		}
		schedulesLoaded = true;
	}

	function openScheduleCreate() {
		editingSchedule = null;
		showScheduleForm = true;
	}

	function openScheduleEdit(sc) {
		editingSchedule = sc;
		showScheduleForm = true;
	}

	function closeScheduleForm() {
		showScheduleForm = false;
		editingSchedule = null;
	}

	async function handleScheduleSave(data) {
		if (editingSchedule) {
			const res = await apiPatch(`/api/projects/${encodeURIComponent(projectName)}/schedules/${editingSchedule.id}`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to update');
		} else {
			const res = await apiPost(`/api/projects/${encodeURIComponent(projectName)}/schedules`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to create');
		}
		closeScheduleForm();
		await loadSchedules();
	}

	async function toggleSchedule(sc) {
		await apiPatch(`/api/projects/${encodeURIComponent(projectName)}/schedules/${sc.id}`, { enabled: !sc.enabled });
		await loadSchedules();
	}

	async function deleteProjectSchedule(sc) {
		if (!confirm(`Delete schedule "${sc.name}"?`)) return;
		await apiDelete(`/api/projects/${encodeURIComponent(projectName)}/schedules/${sc.id}`);
		await loadSchedules();
	}

	async function runScheduleNow(sc) {
		await apiPost(`/api/projects/${encodeURIComponent(projectName)}/schedules/${sc.id}/run`, {});
		await loadSchedules();
	}

	function formatScheduleExpr(sc) {
		if (sc.schedule_type === 'cron') return sc.cron_expr;
		const secs = sc.interval_seconds;
		if (secs >= 86400 && secs % 86400 === 0) return `Every ${secs / 86400}d`;
		if (secs >= 3600 && secs % 3600 === 0) return `Every ${secs / 3600}h`;
		return `Every ${secs / 60}m`;
	}

	// Skill functions
	async function loadProjectSkills() {
		const res = await apiGet(`/api/projects/${encodeURIComponent(projectName)}/skills`);
		if (res?.data) {
			projectSkills = res.data;
		}
		skillsLoaded = true;
	}

	function openSkillCreate() {
		editingSkillItem = null;
		showSkillForm = true;
	}

	function openSkillEdit(sk) {
		editingSkillItem = sk;
		showSkillForm = true;
	}

	function closeSkillForm() {
		showSkillForm = false;
		editingSkillItem = null;
	}

	async function handleSkillSave(data) {
		if (editingSkillItem) {
			const res = await apiPatch(`/api/skills/${editingSkillItem.id}`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to update');
		} else {
			const res = await apiPost(`/api/projects/${encodeURIComponent(projectName)}/skills`, data);
			if (!res?.success) throw new Error(res?.message || 'Failed to create');
		}
		closeSkillForm();
		await loadProjectSkills();
	}

	async function toggleSkill(sk) {
		await apiPatch(`/api/skills/${sk.id}`, { enabled: !sk.enabled });
		await loadProjectSkills();
	}

	async function deleteProjectSkill(sk) {
		if (!confirm(`Delete skill "${sk.name}"?`)) return;
		await apiDelete(`/api/skills/${sk.id}`);
		await loadProjectSkills();
	}
</script>

{#if loading}
	<div class="project-page">
		<p class="text-muted">Loading...</p>
	</div>
{:else if !project}
	<div class="project-page">
		<div class="card empty-state">
			<p>Project "{projectName}" not found.</p>
			<a href="/projects" class="btn btn-primary">Back to Projects</a>
		</div>
	</div>
{:else}
	<div class="project-page">
		<!-- Compact header: name + path inline -->
		<div class="project-header-bar">
			<div class="header-title-row">
				<a href="/projects" class="back-link" title="Back to Projects">&larr;</a>
				<h1 class="project-title">{project.name}</h1>
				<span class="header-path mono">{project.path}</span>
				{#if project.repo_url}
					<a href={project.repo_url} target="_blank" rel="noopener noreferrer" class="github-link" title="Open on GitHub">
						<svg class="github-icon" viewBox="0 0 16 16" fill="currentColor" width="16" height="16">
							<path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
						</svg>
					</a>
				{/if}
				{#if project.description}
					<span class="header-desc">— {project.description}</span>
				{/if}
			</div>
			<div class="header-status">
				{#if sheepName}
					<select class="provider-select" value={sheepProvider}
						onchange={(e) => changeProvider(e.target.value)}>
						{#each PROVIDER_OPTIONS as opt}
							{#if providerEnabled[opt.value] || sheepProvider === opt.value}
								<option value={opt.value}>{opt.label}{!providerEnabled[opt.value] ? ' (off)' : ''}</option>
							{/if}
						{/each}
					</select>
					{#if providerHasThinking}
						<label class="thinking-toggle" title="Enable reasoning for this project">
							<input
								type="checkbox"
								checked={thinkingChecked}
								onchange={toggleThinking}
							/>
							<span>Thinking</span>
						</label>
					{/if}
					{#if providerHasModel}
						<select class="provider-select" value={modelSelected} onchange={changeModel}
							title="Override model for this project">
							<option value="">Default</option>
							{#each activeModelOptions as opt}
								<option value={opt.id} title={opt.label}>{truncateModel(opt.label)}</option>
							{/each}
						</select>
					{/if}
					<span class="sheep-label">{sheepName}</span>
					<StatusBadge status={sheepStatus} taskId={runningTaskId} />
				{:else}
					<span class="no-sheep">No sheep</span>
				{/if}
			</div>
		</div>

		<!-- Tabs -->
		<div class="tabs">
			<button class="tab" class:active={activeTab === 'output'}
				onclick={() => switchTab('output')}>
				Live Output
			</button>
			<button class="tab" class:active={activeTab === 'history'}
				onclick={() => switchTab('history')}>
				Task History
			</button>
			<button class="tab" class:active={activeTab === 'files'}
				onclick={() => switchTab('files')}>
				Files
			</button>
			<button class="tab" class:active={activeTab === 'git'}
				onclick={() => switchTab('git')}>
				Git
			</button>
			<button class="tab" class:active={activeTab === 'schedules'}
				onclick={() => switchTab('schedules')}>
				Schedules
			</button>
			<button class="tab" class:active={activeTab === 'skills'}
				onclick={() => switchTab('skills')}>
				Skills
			</button>
			<button class="tab" class:active={activeTab === 'issues'}
				onclick={() => switchTab('issues')}>
				Issues
			</button>
			<button class="tab" class:active={activeTab === 'wiki'}
				onclick={() => switchTab('wiki')}>
				Wiki
			</button>
			<button class="tab" class:active={activeTab === 'settings'}
				onclick={() => switchTab('settings')}>
				Settings
			</button>
		</div>

		<!-- Content area -->
		<div class="content-area" class:has-input={activeTab === 'output'}>
			<!-- Live Output tab -->
			{#if activeTab === 'output'}
				<div class="output-fill">
					{#if sheepProvider === 'magi'}
						<MagiStreamPanel lines={liveOutput} maxHeight="none" />
					{:else}
						<OutputViewer lines={liveOutput} maxHeight="none" />
					{/if}
				</div>
			{/if}

			<!-- Task History tab -->
			{#if activeTab === 'history'}
				<div class="history-fill">
					<div class="task-search">
						<input
							class="input task-search-input"
							type="text"
							placeholder="Search tasks..."
							bind:value={taskSearchQuery}
							onkeydown={onTaskSearch}
						/>
					</div>
					{#if !tasksLoaded}
						<p class="text-muted">Loading tasks...</p>
					{:else if tasks.length === 0}
						<p class="text-muted">{taskSearchQuery ? 'No matching tasks' : 'No tasks yet'}</p>
					{:else}
						<div class="task-list">
							{#each tasks as t (t.id)}
								<a href="/tasks/{t.id}?from=project" class="card task-history-item">
									<div class="task-row">
										<span class="task-id mono">#{t.id}</span>
										<StatusBadge status={t.status} />
										<span class="task-prompt-text">{truncate(t.prompt, 80)}</span>
										{#if t.status === 'failed' || t.status === 'stopped'}
											<button class="retry-btn" onclick={(e) => retryTask(e, t.id)}
												disabled={retryingId === t.id}>
												{retryingId === t.id ? '...' : 'Retry'}
											</button>
											<button class="retry-from-btn" onclick={(e) => retryFromTask(e, t.id)}
												disabled={retryFromId === t.id}>
												{retryFromId === t.id ? '...' : 'Retry All'}
											</button>
										{/if}
										{#if t.duration_sec > 0}
											{@const dur = t.duration_sec}
											{@const hh = String(Math.floor(dur / 3600)).padStart(2, '0')}
											{@const mm = String(Math.floor((dur % 3600) / 60)).padStart(2, '0')}
											{@const ss = String(dur % 60).padStart(2, '0')}
											<span class="task-duration mono" title="Execution time">{hh}:{mm}:{ss}</span>
										{/if}
										{#if t.prompt_tokens > 0 || t.completion_tokens > 0}
											{@const totalTok = (t.prompt_tokens || 0) + (t.completion_tokens || 0)}
											<span class="task-tokens mono">{totalTok.toLocaleString()} tokens</span>
										{/if}
										{#if t.model}
											<span class="task-model mono" title={t.model}>{truncateModel(t.model)}</span>
										{/if}
										<span class="task-time mono">{formatTime(t.created_at)}</span>
									</div>
									{#if t.summary}
										<div class="task-summary-text">{truncate(t.summary, 120)}</div>
									{/if}
								</a>
							{/each}
						</div>
						{#if taskTotalPages > 1}
							<Pagination page={taskPage} totalPages={taskTotalPages}
								total={taskTotal} limit={taskLimit} onChange={onTaskPageChange} />
						{/if}
					{/if}
				</div>
			{/if}

			<!-- Files tab -->
			{#if activeTab === 'files'}
				<div class="files-fill">
					{#key filesKey}
						<FileBrowser {projectName} />
					{/key}
				</div>
			{/if}

			<!-- Git tab -->
			{#if activeTab === 'git'}
				<div class="git-fill">
					<GitGraph {projectName} />
				</div>
			{/if}

			<!-- Schedules tab -->
			{#if activeTab === 'schedules'}
				<div class="schedules-fill">
					<div class="schedules-header">
						<button class="btn btn-primary" onclick={openScheduleCreate}>+ New Schedule</button>
					</div>

					{#if !schedulesLoaded}
						<p class="text-muted">Loading schedules...</p>
					{:else if projectSchedules.length === 0 && !showScheduleForm}
						<p class="text-muted">No schedules for this project.</p>
					{:else}
						<div class="schedule-list-compact">
							{#each projectSchedules as sc (sc.id)}
								<div class="card schedule-card" class:disabled-card={!sc.enabled}>
									<div class="sc-row">
										<span class="sc-name">{sc.name}</span>
										<span class="sc-badge" class:cron-badge={sc.schedule_type === 'cron'}>{sc.schedule_type}</span>
										<span class="sc-expr mono">{formatScheduleExpr(sc)}</span>
										<div class="sc-actions">
											<button class="btn btn-xs" onclick={() => runScheduleNow(sc)}>Run</button>
											<button class="btn btn-xs" onclick={() => toggleSchedule(sc)}>{sc.enabled ? 'Off' : 'On'}</button>
											<button class="btn btn-xs" onclick={() => openScheduleEdit(sc)}>Edit</button>
											<button class="btn btn-xs btn-danger-xs" onclick={() => deleteProjectSchedule(sc)}>Del</button>
										</div>
									</div>
									<div class="sc-prompt">{truncate(sc.prompt, 100)}</div>
									<div class="sc-meta">
										{#if sc.next_run}<span>Next: {sc.next_run}</span>{/if}
										{#if sc.last_run}<span>Last: {sc.last_run}</span>{/if}
									</div>
								</div>
							{/each}
						</div>
					{/if}

					{#if showScheduleForm}
						<div class="modal-overlay" onclick={closeScheduleForm}>
							<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
								<div class="modal-hdr">
									<h2>{editingSchedule ? 'Edit Schedule' : 'New Schedule'}</h2>
									<button class="btn" onclick={closeScheduleForm}>Close</button>
								</div>
								<ScheduleForm
									schedule={editingSchedule}
									fixedProject={projectName}
									onSave={handleScheduleSave}
									onCancel={closeScheduleForm}
								/>
							</div>
						</div>
					{/if}
				</div>
			{/if}

			<!-- Issues tab -->
			{#if activeTab === 'issues'}
				<div class="issues-fill">
					<IssueTab {projectName} />
				</div>
			{/if}

			<!-- Wiki tab -->
			{#if activeTab === 'wiki'}
				<div class="wiki-fill">
					<div class="wiki-layout">
						<div class="wiki-sidebar">
							<div class="wiki-sidebar-hdr">
								<span class="wiki-sidebar-title">Pages</span>
							</div>
							{#if !wikiLoaded}
								<p class="text-muted">Loading...</p>
							{:else if wikiPages.length === 0}
								<p class="text-muted">No wiki pages yet.</p>
							{:else}
								{#each wikiPages as page (page.slug)}
									<button
										class="wiki-page-item"
										class:active={wikiSelectedPage && wikiSelectedPage.slug === page.slug}
										onclick={() => selectWikiPage(page)}
									>
										<span class="wiki-page-slug">{page.slug}</span>
										<span class="wiki-page-cat-badge">{page.category}</span>
									</button>
								{/each}
							{/if}
						</div>

						<div class="wiki-main">
							{#if !wikiSelectedPage}
								<p class="text-muted">Select a page to view.</p>
							{:else if wikiEditing}
								<div class="wiki-editor">
									<div class="wiki-editor-hdr">
										<input
											class="input wiki-title-input"
											type="text"
											bind:value={wikiEditTitle}
											placeholder="Page title"
										/>
										<div class="wiki-editor-actions">
											<button class="btn btn-sm" onclick={closeWikiEdit}>Cancel</button>
											<button class="btn btn-sm btn-primary" onclick={saveWikiPage}>Save</button>
										</div>
									</div>
									<textarea
										class="wiki-content-textarea"
										bind:value={wikiEditContent}
										placeholder="Write in Markdown..."
									></textarea>
								</div>
							{:else}
								<div class="wiki-viewer">
									<div class="wiki-viewer-hdr">
										<div>
											<h2 class="wiki-page-title">{wikiSelectedPage.title}</h2>
											<div class="wiki-page-meta">
												<span class="wiki-page-cat-badge">{wikiSelectedPage.category}</span>
												{#if wikiSelectedPage.tags?.length > 0}
													{#each wikiSelectedPage.tags as tag}
														<span class="wiki-tag">{tag}</span>
													{/each}
												{/if}
											</div>
										</div>
										<button class="btn btn-sm btn-primary" onclick={openWikiEdit}>Edit</button>
									</div>
									<div class="wiki-content-body">
										{wikiSelectedPage.content}
									</div>
								</div>
							{/if}
						</div>
					</div>
				</div>
			{/if}

			<!-- Skills tab -->
			{#if activeTab === 'skills'}
				<div class="schedules-fill">
					<div class="schedules-header">
						<button class="btn btn-primary" onclick={openSkillCreate}>+ New Skill</button>
					</div>

					{#if !skillsLoaded}
						<p class="text-muted">Loading skills...</p>
					{:else if projectSkills.length === 0 && !showSkillForm}
						<p class="text-muted">No skills for this project.</p>
					{:else}
						<div class="schedule-list-compact">
							{#each projectSkills as sk (sk.id)}
								<div class="card schedule-card" class:disabled-card={!sk.enabled}>
									<div class="sc-row">
										<span class="sc-name">{sk.name}</span>
										{#if sk.bundled}
											<span class="sc-badge" style="background:rgba(163,113,247,0.15);color:#a371f7">bundled</span>
										{/if}
										<div class="sc-actions">
											<button class="btn btn-xs" onclick={() => toggleSkill(sk)}>{sk.enabled ? 'Off' : 'On'}</button>
											<button class="btn btn-xs" onclick={() => openSkillEdit(sk)}>Edit</button>
											{#if !sk.bundled}
												<button class="btn btn-xs btn-danger-xs" onclick={() => deleteProjectSkill(sk)}>Del</button>
											{/if}
										</div>
									</div>
									{#if sk.description}
										<div class="sc-prompt">{sk.description}</div>
									{/if}
									<div class="sc-meta">
										{#if sk.tags?.length > 0}
											{#each sk.tags as tag}
												<span style="font-size:10px;padding:1px 6px;border-radius:8px;background:var(--bg-tertiary)">{tag}</span>
											{/each}
										{/if}
									</div>
								</div>
							{/each}
						</div>
					{/if}

					{#if showSkillForm}
						<div class="modal-overlay" onclick={closeSkillForm}>
							<div class="modal-content card" onclick={(e) => e.stopPropagation()}>
								<div class="modal-hdr">
									<h2>{editingSkillItem ? 'Edit Skill' : 'New Skill'}</h2>
									<button class="btn" onclick={closeSkillForm}>Close</button>
								</div>
								<SkillForm
									skill={editingSkillItem}
									fixedProject={projectName}
									fixedScope="project"
									onSave={handleSkillSave}
									onCancel={closeSkillForm}
								/>
							</div>
						</div>
					{/if}
				</div>
			{/if}

			<!-- Settings tab -->
			{#if activeTab === 'settings'}
				<div class="schedules-fill">
					<h3>Project Settings</h3>
					<ProjectSettings projectName={projectName} />
				</div>
			{/if}
		</div>
		{#if activeTab === 'output'}
			<div class="command-bar card">
				{#if sheepName}
		<CommandInput
					projectName={project.name}
					sheepName={sheepName}
					sheepStatus={sheepStatus}
					hasRunningTask={hasRunningTask}
					thinking={providerHasThinking ? thinkingChecked : null}
					model={providerHasModel ? (modelSelected || null) : null}
				/>
				{:else}
					<p class="text-muted command-disabled-text">Assign a sheep to this project to send tasks</p>
				{/if}
			</div>
		{/if}
	</div>
{/if}

<style>
	.project-page {
		display: flex;
		flex-direction: column;
		height: calc(100vh - 48px);
		overflow: hidden;
	}

	.text-muted { color: var(--text-secondary); }

	/* Compact header */
	.project-header-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 0;
		flex-shrink: 0;
		gap: 12px;
		min-height: 0;
	}

	.header-title-row {
		display: flex;
		align-items: baseline;
		gap: 8px;
		min-width: 0;
		flex: 1;
		overflow: hidden;
	}

	.back-link {
		color: var(--text-secondary);
		text-decoration: none;
		font-size: 16px;
		flex-shrink: 0;
	}

	.back-link:hover {
		color: var(--accent);
	}

	.project-title {
		font-size: 18px;
		font-weight: 600;
		margin: 0;
		flex-shrink: 0;
	}

	.header-path {
		font-size: 12px;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	.header-desc {
		font-size: 12px;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.github-link {
		flex-shrink: 0;
		color: var(--text-secondary);
		display: flex;
		align-items: center;
		transition: color 0.15s;
	}

	.github-link:hover {
		color: var(--text-primary);
	}

	.github-icon {
		width: 16px;
		height: 16px;
	}

	.header-status {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
	}

	.provider-select {
		padding: 2px 6px;
		font-size: 12px;
		border-radius: var(--radius);
		background: var(--bg-tertiary);
		color: var(--text-primary);
		border: 1px solid var(--border);
		cursor: pointer;
	}

	.provider-select:hover {
		border-color: var(--accent);
	}

	.thinking-toggle {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 2px 6px;
		font-size: 12px;
		border-radius: var(--radius);
		background: var(--bg-tertiary);
		border: 1px solid var(--border);
		cursor: pointer;
		user-select: none;
	}

	.thinking-toggle input {
		margin: 0;
		cursor: pointer;
	}

	.thinking-toggle:hover {
		border-color: var(--accent);
	}

		.sheep-label {
		font-weight: 500;
		font-size: 13px;
		color: var(--accent);
	}

	.no-sheep {
		font-size: 12px;
		color: var(--text-secondary);
	}

	/* Tabs */
	.tabs {
		display: flex;
		gap: 0;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
		overflow-x: auto;
		-webkit-overflow-scrolling: touch;
	}

	.tab {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 13px;
		padding: 6px 14px;
		cursor: pointer;
		border-bottom: 2px solid transparent;
		transition: color 0.15s, border-color 0.15s;
		white-space: nowrap;
		flex-shrink: 0;
	}

	.tab:hover {
		color: var(--text-primary);
	}

	.tab.active {
		color: var(--accent);
		border-bottom-color: var(--accent);
	}

	/* Content area: fills remaining space */
	.content-area {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.output-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.output-fill :global(.output-viewer) {
		flex: 1;
		max-height: none !important;
		min-height: 0;
	}

	.task-search {
		flex-shrink: 0;
		padding: 8px 0;
	}

	.task-search-input {
		width: 100%;
		font-size: 13px;
	}

	.history-fill {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 8px 0;
	}

	/* Task list */
	.task-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.task-history-item {
		text-decoration: none;
		color: inherit;
		transition: border-color 0.15s;
		padding: 10px 14px;
	}

	.task-history-item:hover {
		border-color: var(--accent);
		text-decoration: none;
	}

	.task-row {
		display: flex;
		align-items: center;
		gap: 10px;
		flex-wrap: wrap;
	}

	.task-id {
		font-size: 12px;
		color: var(--accent);
		font-weight: 600;
		flex-shrink: 0;
	}

	.task-prompt-text {
		flex: 1;
		font-size: 13px;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-duration {
		font-size: 11px;
		color: var(--text-secondary);
		flex-shrink: 0;
		background: rgba(230, 126, 34, 0.1);
		padding: 1px 6px;
		border-radius: 8px;
	}

	.task-tokens {
		font-size: 11px;
		color: var(--text-secondary);
		flex-shrink: 0;
		background: rgba(46, 204, 113, 0.1);
		padding: 1px 6px;
		border-radius: 8px;
	}

	.task-model {
		font-size: 11px;
		color: var(--text-secondary);
		flex-shrink: 0;
		background: rgba(155, 89, 182, 0.12);
		padding: 1px 6px;
		border-radius: 8px;
		max-width: 160px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-time {
		font-size: 11px;
		color: var(--text-secondary);
		flex-shrink: 0;
	}

	.task-summary-text {
		font-size: 12px;
		color: var(--text-secondary);
		margin-top: 4px;
		line-height: 1.4;
	}

	.retry-btn, .retry-from-btn {
		padding: 2px 8px;
		font-size: 11px;
		font-weight: 600;
		border: 1px solid var(--border-color, #444);
		border-radius: 4px;
		background: transparent;
		color: var(--accent);
		cursor: pointer;
		flex-shrink: 0;
		transition: background 0.15s, opacity 0.15s;
	}
	.retry-btn:hover, .retry-from-btn:hover {
		background: var(--bg-tertiary, #2a2a2a);
	}
	.retry-btn:disabled, .retry-from-btn:disabled {
		opacity: 0.5;
		cursor: default;
	}
	.retry-from-btn {
		color: var(--text-secondary, #888);
	}

	/* Command bar: fixed at bottom */
	.command-bar {
		flex-shrink: 0;
		margin-top: 0;
		border-top: 1px solid var(--border);
	}

	.command-disabled-text {
		text-align: center;
		margin: 0;
		font-size: 13px;
	}

	.empty-state {
		text-align: center;
		padding: 40px 20px;
	}

	/* Git tab */
	.git-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	/* Files tab */
	.files-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	@media (max-width: 768px) {
		.project-page {
			height: calc(100dvh - 12px);
			margin-top: -44px;
			overflow-x: hidden;
		}

		.project-header-bar {
			flex-wrap: wrap;
			gap: 4px;
			padding: 4px 0 4px 56px;
			box-sizing: border-box;
			width: 100%;
		}

		.header-title-row {
			align-items: center;
			flex-wrap: nowrap;
			gap: 6px;
			min-width: 0;
		}

		.project-title {
			font-size: 16px;
		}

		.header-path {
			display: none;
		}

		.header-desc {
			display: none;
		}

		.header-status {
			width: 100%;
		}

		.provider-select {
			padding: 6px 10px;
			font-size: 13px;
		}

		.tab {
			padding: 8px 10px;
			font-size: 12px;
		}

		.task-history-item {
			padding: 8px 10px;
		}
	}

	/* Schedules tab */
	.schedules-fill {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 8px 0;
	}

	.schedules-header {
		display: flex;
		justify-content: flex-end;
		margin-bottom: 12px;
	}

	.btn-primary {
		background: var(--accent);
		color: #fff;
		border-color: var(--accent);
	}

	.schedule-list-compact {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.schedule-card {
		transition: border-color 0.15s;
	}

	.schedule-card:hover {
		border-color: var(--accent);
	}

	.disabled-card {
		opacity: 0.5;
	}

	.sc-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-bottom: 4px;
		flex-wrap: wrap;
	}

	.sc-name {
		font-weight: 600;
		font-size: 13px;
	}

	.sc-badge {
		font-size: 10px;
		padding: 1px 5px;
		border-radius: 8px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		text-transform: uppercase;
		font-weight: 600;
	}

	.cron-badge {
		background: rgba(56, 139, 253, 0.15);
		color: var(--accent);
	}

	.sc-expr {
		font-size: 11px;
		color: var(--text-secondary);
	}

	.sc-actions {
		margin-left: auto;
		display: flex;
		gap: 3px;
	}

	.btn-xs {
		padding: 2px 6px;
		font-size: 10px;
	}

	.btn-danger-xs {
		color: var(--danger);
		border-color: var(--danger);
	}

	.sc-prompt {
		font-size: 12px;
		color: var(--text-primary);
		margin-bottom: 4px;
	}

	.sc-meta {
		display: flex;
		gap: 10px;
		font-size: 11px;
		color: var(--text-secondary);
	}

	.modal-overlay {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 100;
		padding: 20px;
	}

	.modal-content {
		width: 100%;
		max-width: 600px;
		max-height: 85vh;
		overflow-y: auto;
		padding: 24px;
	}

	.modal-hdr {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 16px;
	}

	.modal-hdr h2 {
		font-size: 16px;
	}

	/* Issues tab */
	.issues-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	/* Wiki tab */
	.wiki-fill {
		flex: 1;
		min-height: 0;
		overflow: hidden;
		display: flex;
		flex-direction: column;
	}

	.wiki-layout {
		display: flex;
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}

	.wiki-sidebar {
		width: 220px;
		flex-shrink: 0;
		border-right: 1px solid var(--border);
		display: flex;
		flex-direction: column;
		overflow-y: auto;
	}

	.wiki-sidebar-hdr {
		padding: 10px 12px 6px;
		border-bottom: 1px solid var(--border);
	}

	.wiki-sidebar-title {
		font-size: 11px;
		font-weight: 700;
		text-transform: uppercase;
		color: var(--text-secondary);
		letter-spacing: 0.05em;
	}

	.wiki-page-item {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 7px 12px;
		background: none;
		border: none;
		cursor: pointer;
		text-align: left;
		color: var(--text-primary);
		font-size: 12px;
		transition: background 0.15s;
		border-left: 2px solid transparent;
	}

	.wiki-page-item:hover {
		background: var(--bg-tertiary);
	}

	.wiki-page-item.active {
		background: rgba(56, 139, 253, 0.08);
		border-left-color: var(--accent);
		color: var(--accent);
		font-weight: 600;
	}

	.wiki-page-slug {
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.wiki-main {
		flex: 1;
		min-width: 0;
		overflow-y: auto;
		padding: 16px 20px;
	}

	.wiki-viewer-hdr {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		margin-bottom: 16px;
		gap: 12px;
	}

	.wiki-page-title {
		font-size: 20px;
		font-weight: 600;
		margin: 0;
	}

	.wiki-page-meta {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-top: 4px;
	}

	.wiki-page-cat-badge {
		font-size: 10px;
		padding: 1px 6px;
		border-radius: 8px;
		background: var(--bg-tertiary);
		color: var(--text-secondary);
		text-transform: uppercase;
		font-weight: 600;
	}

	.wiki-tag {
		font-size: 10px;
		padding: 1px 6px;
		border-radius: 8px;
		background: rgba(56, 139, 253, 0.12);
		color: var(--accent);
	}

	.wiki-content-body {
		font-size: 14px;
		line-height: 1.7;
		white-space: pre-wrap;
		white-space: break-spaces;
	}

	.wiki-editor {
		display: flex;
		flex-direction: column;
		min-height: 0;
	}

	.wiki-editor-hdr {
		display: flex;
		align-items: center;
		gap: 10px;
		margin-bottom: 10px;
	}

	.wiki-title-input {
		flex: 1;
		font-size: 16px;
		font-weight: 600;
	}

	.wiki-editor-actions {
		display: flex;
		gap: 6px;
	}

	.btn-sm {
		padding: 4px 10px;
		font-size: 12px;
	}

	.wiki-content-textarea {
		flex: 1;
		min-height: 300px;
		padding: 12px;
		font-family: var(--font-mono, 'Fira Code', 'JetBrains Mono', monospace);
		font-size: 13px;
		line-height: 1.6;
		border-radius: var(--radius);
		border: 1px solid var(--border);
		background: var(--bg-secondary);
		color: var(--text-primary);
		resize: vertical;
	}

	@media (max-width: 768px) {
		.wiki-sidebar {
			width: 160px;
		}
	}
</style>
