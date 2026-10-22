<script>
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import { apiGet, apiPost, apiPatch, apiPut, apiDelete } from '$lib/api.js';
	import { onSSE } from '$lib/sse.js';
	import { appendLiveOutput } from '$lib/liveOutput.js';
	import { thinkingByProject, modelByProject } from '$lib/stores.js';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import OutputViewer from '$lib/components/OutputViewer.svelte';
	import MagiStreamPanel from '$lib/components/MagiStreamPanel.svelte';
	import SubagentStreamPanel from '$lib/components/SubagentStreamPanel.svelte';
	import CommandInput from '$lib/components/CommandInput.svelte';
	import Pagination from '$lib/components/Pagination.svelte';
	import GitGraph from '$lib/components/GitGraph.svelte';
	import ScheduleForm from '$lib/components/ScheduleForm.svelte';
	import SkillForm from '$lib/components/SkillForm.svelte';
	import FileBrowser from '$lib/components/FileBrowser.svelte';
	import IssueTab from '$lib/components/IssueTab.svelte';
	import ProjectSettings from '$lib/components/ProjectSettings.svelte';
	import MarkdownBody from '$lib/components/MarkdownBody.svelte';
	import Icon from '$lib/components/Icon.svelte';

	let projectName = $state('');
	let project = $state(null);
	let loading = $state(true);
	let activeTab = $state('output');

	const PROJECT_TABS = [
		{ id: 'output', label: 'Live Output', icon: 'terminal' },
		{ id: 'history', label: 'Task History', icon: 'clock' },
		{ id: 'files', label: 'Files', icon: 'folder' },
		{ id: 'git', label: 'Git', icon: 'git-branch' },
		{ id: 'schedules', label: 'Schedules', icon: 'alarm-clock' },
		{ id: 'skills', label: 'Skills', icon: 'book-open' },
		{ id: 'issues', label: 'Issues', icon: 'list-checks' },
		{ id: 'wiki', label: 'Wiki', icon: 'file-text' },
		{ id: 'settings', label: 'Settings', icon: 'settings' }
	];

	// Underline that slides to the active tab.
	let tabsEl = $state(null);
	let tabInk = $state({ x: 0, w: 0, ready: false });
	$effect(() => {
		activeTab;
		if (!tabsEl) return;
		const measure = () => {
			const el = tabsEl?.querySelector('.tab.active');
			if (el) tabInk = { x: el.offsetLeft, w: el.offsetWidth, ready: true };
		};
		requestAnimationFrame(measure);
		const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(measure) : null;
		ro?.observe(tabsEl);
		return () => ro?.disconnect();
	});

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

	// Detect subagent demux lines: any [SUB:… prefix → SubagentStreamPanel.
	// Mirrors tasks/[id]/+page.svelte; project Live Output used to skip this
	// and render raw prefixes through OutputViewer (#7575 wall-of-text).
	let hasSubagentOutput = $derived(
		liveOutput.some((line) => /^\[SUB:/.test(line ?? ''))
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
	let wikiNavCollapsed = $state(false);
	const WIKI_NAV_KEY = 'shepherd_wiki_nav_collapsed';

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
		wikiPages = [];
		wikiLoaded = false;
		wikiSelectedPage = null;
		wikiEditing = false;
		wikiEditContent = '';
		wikiEditTitle = '';
		activeTab = 'output';
		loadProject();
		if (initialTab && initialTab !== 'output' && VALID_TABS.includes(initialTab)) {
			switchTab(initialTab);
		}
	}

	onMount(() => {
		try {
			wikiNavCollapsed = localStorage.getItem(WIKI_NAV_KEY) === '1';
		} catch {
			/* ignore */
		}

		// SSE: live output (5000줄 버퍼). The server-side LineCoalescer
		// already emits complete lines (task #7209), so appendLiveOutput
		// is now a thin wrapper that pushes lines.
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
				if (data.task_id === runningTaskId) {
					hasRunningTask = false;
					runningTaskId = null;
				}
				if (tasksLoaded) loadTasks();
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
		// Prefer the in-flight task. created_at DESC would pick a later
		// pending item and hide the running live buffer (#8165/#8166).
		const running = await apiGet(`/api/tasks?project=${encodeURIComponent(projectName)}&status=running&limit=1`);
		let id = running?.data?.[0]?.id;
		if (!id) {
			const res = await apiGet(`/api/tasks?project=${encodeURIComponent(projectName)}&limit=1`);
			id = res?.data?.[0]?.id;
		}
		if (!id) return;
		const taskRes = await apiGet(`/api/tasks/${id}`);
		if (taskRes?.data?.output) {
			liveOutput = taskRes.data.output;
			// Historical lines are complete entries; do not treat last as open.
			liveOutputOpen = false;
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
	let cancellingId = $state(null);
	let deletingId = $state(null);

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

	async function cancelPendingTask(e, taskId) {
		e.preventDefault();
		e.stopPropagation();
		if (cancellingId) return;
		if (!confirm(`Cancel pending task #${taskId}?`)) return;
		cancellingId = taskId;
		try {
			const res = await apiPost(`/api/tasks/${taskId}/cancel`, {});
			if (res?.success) {
				loadTasks();
			} else {
				alert(res?.message || 'Failed to cancel task');
			}
		} catch (err) {
			alert(err?.message || 'Failed to cancel task');
		} finally {
			cancellingId = null;
		}
	}

	async function deleteTask(e, taskId) {
		e.preventDefault();
		e.stopPropagation();
		if (deletingId) return;
		if (!confirm(`Delete task #${taskId}?`)) return;
		deletingId = taskId;
		try {
			const res = await apiDelete(`/api/tasks/${taskId}`);
			if (res?.success) {
				loadTasks();
			} else {
				alert(res?.message || 'Failed to delete task');
			}
		} catch (err) {
			alert(err?.message || 'Failed to delete task');
		} finally {
			deletingId = null;
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
			// 왼쪽 페이지 목록: API가 최근 작성순(created_at DESC)으로 반환한다.
			wikiPages = res.data;
			if (wikiPages.length > 0 && !wikiSelectedPage) {
				selectWikiPage(wikiPages[0]);
			}
		}
		wikiLoaded = true;
	}

	function selectWikiPage(page) {
		wikiSelectedPage = page;
		wikiEditing = false;
	}

	function toggleWikiNav() {
		wikiNavCollapsed = !wikiNavCollapsed;
		try {
			localStorage.setItem(WIKI_NAV_KEY, wikiNavCollapsed ? '1' : '0');
		} catch {
			/* ignore */
		}
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
		const slug = wikiSelectedPage.slug;
		const res = await apiPut(`/api/wiki/pages/${encodeURIComponent(slug)}?project=${encodeURIComponent(projectName)}`, {
			content: wikiEditContent,
			title: wikiEditTitle
		});
		if (res?.success) {
			wikiEditing = false;
			await loadWikiPages();
			const updated = wikiPages.find((p) => p.slug === slug);
			if (updated) selectWikiPage(updated);
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

	// Compact, fixed-width stamp for list columns: "22 Oct 23:26".
	function formatTime(ts) {
		if (!ts) return '';
		const d = new Date(String(ts).replace(' ', 'T'));
		if (isNaN(d)) return ts;
		const sameYear = d.getFullYear() === new Date().getFullYear();
		const date = d.toLocaleDateString('en-GB', sameYear ? { day: '2-digit', month: 'short' } : { day: '2-digit', month: 'short', year: '2-digit' });
		const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
		return `${date} ${time}`;
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
		<!-- Header: breadcrumb, serif name, path · sheep + provider controls -->
		<header class="project-head">
			<div class="ph-main">
				<a href="/projects" class="crumb">
					<Icon name="arrow-left" size={13} />
					<span>Projects</span>
				</a>
				<div class="ph-title-row">
					<h1 class="project-title">{project.name}</h1>
					{#if project.repo_url}
						<a href={project.repo_url} target="_blank" rel="noopener noreferrer" class="icon-link" title="Open on GitHub" aria-label="Open on GitHub">
							<Icon name="github" size={15} />
						</a>
					{/if}
				</div>
				<p class="ph-meta">
					<span class="header-path mono" title={project.path}>{project.path}</span>
					{#if project.description}
						<span class="header-desc" title={project.description}>{project.description}</span>
					{/if}
				</p>
			</div>
			<div class="header-status">
				{#if sheepName}
					<div class="sheep-pill" class:working={sheepStatus === 'working'}>
						<span class="sheep-ava" aria-hidden="true">{[...sheepName][0]}</span>
						<span class="sheep-label">{sheepName}</span>
						<StatusBadge status={sheepStatus} taskId={runningTaskId} />
					</div>
					<div class="controls">
						<label class="ctl" title="Provider">
							<span class="ctl-label">Provider</span>
							<select class="provider-select" value={sheepProvider}
								onchange={(e) => changeProvider(e.target.value)}>
								{#each PROVIDER_OPTIONS as opt}
									{#if providerEnabled[opt.value] || sheepProvider === opt.value}
										<option value={opt.value}>{opt.label}{!providerEnabled[opt.value] ? ' (off)' : ''}</option>
									{/if}
								{/each}
							</select>
						</label>
						{#if providerHasModel}
							<label class="ctl" title="Override model for this project">
								<span class="ctl-label">Model</span>
								<select class="provider-select model-select" value={modelSelected} onchange={changeModel}>
									<option value="">Default</option>
									{#each activeModelOptions as opt}
										<option value={opt.id} title={opt.label}>{truncateModel(opt.label)}</option>
									{/each}
								</select>
							</label>
						{/if}
						{#if providerHasThinking}
							<label class="thinking-toggle" title="Enable reasoning for this project">
								<input
									type="checkbox"
									class="switch"
									checked={thinkingChecked}
									onchange={toggleThinking}
								/>
								<span>Thinking</span>
							</label>
						{/if}
					</div>
				{:else}
					<span class="no-sheep">No sheep assigned</span>
				{/if}
			</div>
		</header>

		<!-- Tabs -->
		<nav class="tabs" role="tablist" aria-label="Project sections" bind:this={tabsEl}>
			<span class="tab-ink" style="--x: {tabInk.x}px; --w: {tabInk.w}px" class:ready={tabInk.ready} aria-hidden="true"></span>
			{#each PROJECT_TABS as t (t.id)}
				<button class="tab" class:active={activeTab === t.id}
					role="tab" aria-selected={activeTab === t.id}
					onclick={() => switchTab(t.id)}>
					<Icon name={t.icon} size={14} stroke={1.9} />
					<span>{t.label}</span>
					{#if t.id === 'output' && sheepStatus === 'working'}
						<span class="live-dot tab-live" aria-label="live"></span>
					{/if}
				</button>
			{/each}
		</nav>

		<!-- Content area -->
		<div class="content-area" class:has-input={activeTab === 'output'}>
			<!-- Live Output tab -->
			{#if activeTab === 'output'}
				<div class="output-fill">
					{#if sheepProvider === 'magi'}
						<MagiStreamPanel lines={liveOutput} maxHeight="none" />
					{:else if hasSubagentOutput}
						<!-- Same routing as tasks/[id]: [SUB:] → demux panel.
						     Without this, OutputViewer treats every prefixed
						     🔧/💭 line as plain text → markdown wall (#7575). -->
						<SubagentStreamPanel lines={liveOutput} maxHeight="none" />
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
							class="input input-search task-search-input"
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
								{@const canRetry = t.status === 'failed' || t.status === 'stopped'}
								<a href="/tasks/{t.id}?from=project" class="task-history-item">
									<div class="task-row">
										<span class="task-id mono num">#{t.id}</span>
										<span class="task-status"><StatusBadge status={t.status} /></span>
										<span class="task-prompt-text">{truncate(t.prompt, 120)}</span>
										<span class="task-duration mono num" title="Execution time">
											{#if t.duration_sec > 0}
												{@const dur = t.duration_sec}
												{String(Math.floor(dur / 3600)).padStart(2, '0')}:{String(Math.floor((dur % 3600) / 60)).padStart(2, '0')}:{String(dur % 60).padStart(2, '0')}
											{/if}
										</span>
										<span class="task-tokens mono num" title="Tokens">
											{#if t.prompt_tokens > 0 || t.completion_tokens > 0}
												{((t.prompt_tokens || 0) + (t.completion_tokens || 0)).toLocaleString()}
											{/if}
										</span>
										<span class="task-model-cell">
											{#if t.model}
												<span class="task-model mono" title={t.model}>{truncateModel(t.model)}</span>
											{/if}
										</span>
										<span class="task-time mono num" title={t.created_at}>{formatTime(t.created_at)}</span>
									</div>
									{#if t.summary || canRetry || t.status === 'pending'}
										<div class="task-sub">
											<span class="task-summary-text">{t.summary ? truncate(t.summary, 160) : ''}</span>
											<span class="task-actions">
												{#if t.status === 'pending'}
													<button class="cancel-btn" onclick={(e) => cancelPendingTask(e, t.id)}
														disabled={cancellingId === t.id}>
														{cancellingId === t.id ? '…' : 'Cancel'}
													</button>
												{/if}
												{#if canRetry}
													<span class="retry-group" class:busy={retryingId === t.id || retryFromId === t.id || deletingId === t.id}>
														<button class="retry-btn" onclick={(e) => retryTask(e, t.id)}
															disabled={retryingId === t.id}>
															<Icon name="rotate-ccw" size={11} stroke={2.2} />
															{retryingId === t.id ? '…' : 'Retry'}
														</button>
														<button class="retry-from-btn" onclick={(e) => retryFromTask(e, t.id)}
															disabled={retryFromId === t.id}>
															{retryFromId === t.id ? '…' : 'Retry All'}
														</button>
														<button class="delete-btn" onclick={(e) => deleteTask(e, t.id)}
															disabled={deletingId === t.id} title="Delete task" aria-label="Delete task #{t.id}">
															{#if deletingId === t.id}…{:else}<Icon name="trash" size={12} />{/if}
														</button>
													</span>
												{/if}
											</span>
										</div>
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
						<!-- Form modal — only Close/Cancel closes (no outside-click dismiss) -->
						<div class="modal-overlay">
							<div class="modal-content card">
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
					<div class="wiki-layout" class:nav-collapsed={wikiNavCollapsed}>
						<nav class="wiki-sidebar" aria-label="Wiki pages">
							<div class="wiki-sidebar-hdr">
								{#if !wikiNavCollapsed}
									<span class="wiki-sidebar-title">Pages</span>
								{/if}
								<button
									type="button"
									class="wiki-nav-toggle"
									onclick={toggleWikiNav}
									title={wikiNavCollapsed ? '페이지 목록 펼치기' : '페이지 목록 접기'}
									aria-label={wikiNavCollapsed ? '페이지 목록 펼치기' : '페이지 목록 접기'}
									aria-expanded={!wikiNavCollapsed}
								>
									<Icon name={wikiNavCollapsed ? 'chevron-right' : 'chevron-left'} size={16} />
								</button>
							</div>
							{#if !wikiNavCollapsed}
								{#if !wikiLoaded}
									<p class="text-muted wiki-sidebar-empty">Loading...</p>
								{:else if wikiPages.length === 0}
									<p class="text-muted wiki-sidebar-empty">No wiki pages yet.</p>
								{:else}
									{#each wikiPages as page (page.slug)}
										<button
											type="button"
											class="wiki-page-item"
											class:active={wikiSelectedPage && wikiSelectedPage.slug === page.slug}
											onclick={() => selectWikiPage(page)}
										>
											<span class="wiki-page-slug">{page.title || page.slug}</span>
											<span class="wiki-page-cat-badge">{page.category}</span>
										</button>
									{/each}
								{/if}
							{/if}
						</nav>

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
										<MarkdownBody text={wikiSelectedPage.content || ''} />
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
											<span class="sc-badge sc-bundled">bundled</span>
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
												<span class="sc-tag">{tag}</span>
											{/each}
										{/if}
									</div>
								</div>
							{/each}
						</div>
					{/if}

					{#if showSkillForm}
						<!-- Form modal — only Close/Cancel closes (no outside-click dismiss) -->
						<div class="modal-overlay">
							<div class="modal-content card">
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
	/* Full-height app page: header + tabs + scroll pane + command bar.
	   The layout shrinks its own padding for this route (see +layout). */
	.project-page {
		display: flex;
		flex-direction: column;
		height: calc(100dvh - var(--page-pad-top) - var(--page-pad-bottom));
		min-height: 420px;
		overflow: hidden;
	}

	/* ---------- Header ---------- */
	.project-head {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: 12px 24px;
		flex-shrink: 0;
		padding-bottom: 18px;
		animation: rise-in var(--dur-3) var(--ease-out) backwards;
	}

	.ph-main {
		display: flex;
		flex-direction: column;
		gap: 8px;
		min-width: 0;
		flex: 1;
	}

	.crumb {
		align-self: flex-start;
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		letter-spacing: var(--tracking-caps);
		text-transform: uppercase;
		color: var(--text-tertiary);
		text-decoration: none;
		transition: color var(--dur-1) ease, gap var(--dur-2) var(--ease-out);
	}

	.crumb:hover {
		color: var(--accent);
		text-decoration: none;
		gap: 9px;
	}

	.ph-title-row {
		display: flex;
		align-items: center;
		gap: 12px;
		min-width: 0;
	}

	.project-title {
		font-family: var(--font-display);
		font-size: clamp(34px, 3.4vw, 46px);
		font-weight: 400;
		line-height: 0.95;
		letter-spacing: -0.02em;
		margin: 0;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.icon-link {
		flex-shrink: 0;
		display: grid;
		place-items: center;
		width: 30px;
		height: 30px;
		border-radius: 50%;
		color: var(--text-tertiary);
		box-shadow: inset 0 0 0 1px var(--border);
		transition: color var(--dur-1) ease, box-shadow var(--dur-1) ease, background var(--dur-1) ease;
	}

	.icon-link:hover {
		color: var(--text-primary);
		background: var(--bg-hover);
		box-shadow: inset 0 0 0 1px var(--border-strong);
	}

	.ph-meta {
		display: flex;
		align-items: baseline;
		gap: 10px;
		min-width: 0;
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
	}

	.header-path {
		flex-shrink: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--fs-2xs);
	}

	.header-desc {
		flex-shrink: 2;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--text-secondary);
	}

	.header-desc::before {
		content: '';
		display: inline-block;
		width: 3px;
		height: 3px;
		margin: 0 10px 2px 0;
		border-radius: 50%;
		background: var(--text-tertiary);
		vertical-align: middle;
	}

	.header-status {
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 10px;
		flex-shrink: 0;
	}

	.sheep-pill {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		padding: 3px 4px 3px 3px;
		border-radius: var(--radius-full);
		background: rgba(237, 230, 218, 0.03);
		box-shadow: inset 0 0 0 1px var(--border);
	}

	.sheep-pill.working {
		box-shadow: inset 0 0 0 1px var(--live-line), 0 0 24px -8px var(--live-glow);
	}

	.sheep-ava {
		display: grid;
		place-items: center;
		width: 22px;
		height: 22px;
		border-radius: 50%;
		background: var(--wool-soft);
		color: var(--wool);
		font-size: var(--fs-2xs);
		font-weight: var(--fw-semibold);
	}

	.sheep-pill.working .sheep-ava {
		background: var(--live-soft);
		color: var(--live);
	}

	.sheep-label {
		font-weight: var(--fw-medium);
		font-size: var(--fs-sm);
		color: var(--text-primary);
	}

	.controls {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.ctl {
		position: relative;
		display: inline-flex;
		align-items: center;
	}

	.ctl-label {
		position: absolute;
		left: 10px;
		top: 50%;
		transform: translateY(-50%);
		font-family: var(--font-mono);
		font-size: 9.5px;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--text-tertiary);
		pointer-events: none;
	}

	.provider-select {
		height: 30px;
		padding: 0 28px 0 72px;
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		border-radius: 9px;
		background-color: rgba(237, 230, 218, 0.03);
		background-position: right 9px center;
		max-width: 260px;
	}

	.model-select {
		padding-left: 58px;
	}

	.thinking-toggle {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		height: 30px;
		padding: 0 10px;
		font-size: var(--fs-xs);
		font-weight: var(--fw-medium);
		color: var(--text-secondary);
		border-radius: 9px;
		box-shadow: inset 0 0 0 1px var(--border);
		background: rgba(237, 230, 218, 0.03);
		cursor: pointer;
		user-select: none;
		transition: box-shadow var(--dur-1) ease, color var(--dur-1) ease;
	}

	.thinking-toggle:hover {
		color: var(--text-primary);
		box-shadow: inset 0 0 0 1px var(--border-strong);
	}

	.no-sheep {
		font-size: var(--fs-xs);
		color: var(--text-tertiary);
		font-style: italic;
	}

	/* ---------- Tabs ---------- */
	.tabs {
		position: relative;
		display: flex;
		gap: 2px;
		flex-shrink: 0;
		margin-bottom: 14px;
		border-bottom: 1px solid var(--border);
		overflow-x: auto;
		scrollbar-width: none;
		-webkit-overflow-scrolling: touch;
	}

	.tabs::-webkit-scrollbar {
		display: none;
	}

	.tab-ink {
		position: absolute;
		left: 0;
		bottom: 0;
		height: 2px;
		width: var(--w);
		transform: translateX(var(--x));
		border-radius: 2px 2px 0 0;
		background: var(--accent);
		box-shadow: 0 0 12px var(--accent-glow);
		pointer-events: none;
	}

	.tab-ink.ready {
		transition: transform 380ms var(--ease-out), width 380ms var(--ease-out);
	}

	.tab {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		padding: 9px 12px 11px;
		border: none;
		background: none;
		color: var(--text-tertiary);
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
		white-space: nowrap;
		flex-shrink: 0;
		cursor: pointer;
		border-radius: var(--radius) var(--radius) 0 0;
		transition: color var(--dur-1) ease, background var(--dur-1) ease;
	}

	.tab:hover {
		color: var(--text-primary);
		background: rgba(237, 230, 218, 0.03);
	}

	.tab.active {
		color: var(--text-primary);
	}

	.tab.active :global(.icon) {
		color: var(--accent);
	}

	.tab-live {
		width: 5px;
		height: 5px;
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
		padding: 2px 0 12px;
	}

	.task-search-input {
		width: 100%;
		max-width: 420px;
		font-size: var(--fs-sm);
		padding-top: 7px;
		padding-bottom: 7px;
	}

	.history-fill {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 2px 2px 8px;
	}

	/* Task list — one quiet surface, rows divided by hairlines */
	.task-list {
		display: flex;
		flex-direction: column;
		padding: 4px;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-2);
	}

	.task-history-item {
		display: block;
		padding: 11px 14px;
		border: none;
		border-radius: var(--radius);
		background: transparent;
		box-shadow: none;
		color: inherit;
		text-decoration: none;
		transition: background var(--dur-1) ease;
	}

	.task-history-item + .task-history-item {
		box-shadow: 0 -1px 0 var(--border-subtle);
	}

	.task-history-item:hover {
		background: rgba(237, 230, 218, 0.035);
		color: inherit;
		text-decoration: none;
	}

	.task-row {
		display: grid;
		grid-template-columns: 52px 100px minmax(0, 1fr) 62px 88px 150px 96px;
		align-items: center;
		gap: 12px;
	}

	.task-id {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.task-status {
		display: flex;
	}

	.task-duration,
	.task-tokens,
	.task-time {
		text-align: right;
	}

	.task-model-cell {
		display: flex;
		justify-content: flex-end;
		min-width: 0;
	}

	.task-sub {
		display: flex;
		align-items: center;
		gap: 12px;
		margin: 6px 0 0 164px;
		min-width: 0;
	}

	.task-actions {
		display: inline-flex;
		gap: 4px;
		margin-left: auto;
		flex-shrink: 0;
	}

	.task-prompt-text {
		flex: 1;
		font-size: var(--fs-sm);
		color: var(--text-primary);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-duration,
	.task-tokens,
	.task-model,
	.task-time {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.task-model {
		max-width: 160px;
		padding: 0 7px;
		line-height: 18px;
		border-radius: var(--radius-full);
		background: rgba(237, 230, 218, 0.04);
		box-shadow: inset 0 0 0 1px var(--border-subtle);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.task-summary-text {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--fs-xs);
		line-height: 1.45;
		color: var(--text-secondary);
	}

	/* Retry controls repeat on every failed row, so on pointer devices they stay
	   out of the way until the row is hovered or focused. */
	.retry-group {
		display: inline-flex;
		gap: 4px;
	}

	@media (hover: hover) {
		.retry-group {
			opacity: 0;
			transform: translateX(4px);
			transition: opacity var(--dur-2) ease, transform var(--dur-2) var(--ease-out);
		}

		.task-history-item:hover .retry-group,
		.task-history-item:focus-within .retry-group,
		.retry-group.busy {
			opacity: 1;
			transform: none;
		}
	}

	.retry-btn, .retry-from-btn, .cancel-btn, .delete-btn {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		height: 22px;
		padding: 0 8px;
		font-size: var(--fs-2xs);
		font-weight: var(--fw-medium);
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-3);
		color: var(--text-primary);
		cursor: pointer;
		flex-shrink: 0;
		transition: background var(--dur-1) ease, border-color var(--dur-1) ease, color var(--dur-1) ease;
	}
	.retry-btn:hover, .retry-from-btn:hover {
		background: var(--bg-4);
		border-color: var(--border-strong);
	}
	.retry-btn {
		color: var(--accent);
		border-color: var(--accent-line);
		background: var(--accent-soft);
	}
	.retry-btn:disabled, .retry-from-btn:disabled, .cancel-btn:disabled, .delete-btn:disabled {
		opacity: 0.5;
		cursor: default;
	}
	.retry-from-btn {
		color: var(--text-secondary);
	}
	.cancel-btn,
	.delete-btn {
		color: var(--danger);
		border-color: var(--danger-line);
		background: transparent;
	}

	.delete-btn {
		padding: 0 6px;
	}
	.cancel-btn:hover,
	.delete-btn:hover {
		background: var(--danger-soft);
	}

	/* Command bar: pinned under the output */
	.command-bar {
		flex-shrink: 0;
		margin-top: 12px;
		padding: 8px;
		border-radius: var(--radius-lg);
		border-color: var(--border);
		background:
			linear-gradient(180deg, rgba(255, 250, 240, 0.03), rgba(255, 250, 240, 0.01)),
			var(--bg-2);
		box-shadow: var(--sheen), var(--shadow-2);
		transition: border-color var(--dur-2) ease, box-shadow var(--dur-3) ease;
	}

	.command-bar:focus-within {
		border-color: var(--accent-line);
		box-shadow: var(--sheen), var(--shadow-2), 0 0 0 4px rgba(244, 184, 96, 0.06);
	}

	.command-disabled-text {
		text-align: center;
		margin: 0;
		padding: 10px;
		font-size: var(--fs-sm);
	}

	.empty-state {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 16px;
		text-align: center;
		padding: 56px 20px;
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

	@media (max-width: 1200px) {
		.task-row {
			grid-template-columns: 52px 100px minmax(0, 1fr) 62px 88px 96px;
		}

		.task-model-cell {
			display: none;
		}
	}

	@media (max-width: 960px) {
		.project-head {
			flex-direction: column;
			align-items: stretch;
		}

		.header-status {
			flex-direction: row;
			flex-wrap: wrap;
			align-items: center;
			justify-content: space-between;
		}
	}

	@media (max-width: 768px) {
		.project-page {
			height: calc(100dvh - var(--mobile-topbar-h) - var(--page-pad-top) - var(--page-pad-bottom));
			min-height: 360px;
			overflow-x: hidden;
		}

		.project-head {
			gap: 10px;
			padding-bottom: 12px;
		}

		.project-title {
			font-size: 32px;
		}

		.header-desc {
			display: none;
		}

		.controls {
			flex-wrap: wrap;
		}

		.provider-select {
			height: 34px;
			max-width: 46vw;
		}

		.tabs {
			margin-bottom: 10px;
		}

		.tab {
			padding: 8px 10px 10px;
			font-size: var(--fs-xs);
		}

		.task-history-item {
			padding: 10px;
		}

		.task-row {
			grid-template-columns: auto auto minmax(0, 1fr);
			gap: 8px;
		}

		.task-row .task-prompt-text {
			grid-column: 1 / -1;
			white-space: normal;
			display: -webkit-box;
			-webkit-line-clamp: 2;
			-webkit-box-orient: vertical;
		}

		.task-duration,
		.task-tokens,
		.task-model-cell {
			display: none;
		}

		.task-time {
			grid-row: 1;
			grid-column: 3;
		}

		.task-sub {
			margin-left: 0;
			flex-wrap: wrap;
		}

		.command-bar {
			margin-top: 8px;
			padding: 6px;
		}
	}

	/* Schedules tab */
	.schedules-fill {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 2px 2px 8px;
	}

	.schedules-fill > h3 {
		margin: 4px 0 16px;
		font-family: var(--font-display);
		font-size: 28px;
		font-weight: 400;
	}

	.schedules-header {
		display: flex;
		justify-content: flex-end;
		margin-bottom: 12px;
	}

	.schedule-list-compact {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.schedule-card {
		padding: 14px 16px;
		transition: border-color var(--dur-1) ease, opacity var(--dur-2) ease;
	}

	.schedule-card:hover {
		border-color: var(--border-strong);
	}

	.disabled-card {
		opacity: 0.55;
	}

	.sc-row {
		display: flex;
		align-items: center;
		gap: 10px;
		margin-bottom: 6px;
		flex-wrap: wrap;
	}

	.sc-name {
		font-weight: var(--fw-semibold);
		font-size: var(--fs-sm);
	}

	.sc-badge {
		font-family: var(--font-mono);
		font-size: 9.5px;
		padding: 0 7px;
		line-height: 17px;
		border-radius: var(--radius-full);
		background: rgba(237, 230, 218, 0.05);
		box-shadow: inset 0 0 0 1px var(--border-subtle);
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.08em;
	}

	.cron-badge {
		background: var(--info-soft);
		box-shadow: inset 0 0 0 1px rgba(143, 193, 255, 0.2);
		color: var(--info);
	}

	.sc-bundled {
		background: var(--queued-soft);
		box-shadow: inset 0 0 0 1px var(--queued-line);
		color: var(--queued);
	}

	.sc-expr {
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.sc-actions {
		margin-left: auto;
		display: flex;
		gap: 4px;
	}

	.btn-danger-xs {
		color: var(--danger);
		border-color: var(--danger-line);
		background: transparent;
	}

	.btn-danger-xs:hover {
		color: var(--danger-hover);
		border-color: var(--danger);
		background: var(--danger-soft);
	}

	.sc-prompt {
		font-size: var(--fs-xs);
		line-height: 1.5;
		color: var(--text-secondary);
		margin-bottom: 6px;
	}

	.sc-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 6px 14px;
		font-family: var(--font-mono);
		font-size: var(--fs-2xs);
		color: var(--text-tertiary);
	}

	.sc-tag {
		font-family: var(--font-sans);
		padding: 0 7px;
		line-height: 17px;
		border-radius: var(--radius-full);
		background: rgba(237, 230, 218, 0.05);
		color: var(--text-secondary);
	}

	.modal-hdr {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 16px;
	}

	.modal-hdr h2 {
		font-family: var(--font-display);
		font-size: 28px;
		font-weight: 400;
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
		transition: width 0.15s ease;
	}

	.wiki-layout.nav-collapsed .wiki-sidebar {
		width: 36px;
		overflow: hidden;
	}

	.wiki-sidebar-hdr {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 8px 8px 8px 12px;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
	}

	.wiki-layout.nav-collapsed .wiki-sidebar-hdr {
		justify-content: center;
		padding: 8px 0;
	}

	.wiki-sidebar-title {
		font-size: 11px;
		font-weight: 700;
		text-transform: uppercase;
		color: var(--text-secondary);
		letter-spacing: 0.05em;
	}

	.wiki-nav-toggle {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 24px;
		height: 24px;
		padding: 0;
		background: none;
		border: none;
		border-radius: 4px;
		color: var(--text-secondary);
		cursor: pointer;
		flex-shrink: 0;
	}

	.wiki-nav-toggle:hover {
		color: var(--text-primary);
		background: var(--bg-tertiary);
	}

	.wiki-sidebar-empty {
		padding: 10px 12px;
		font-size: 12px;
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
		background: var(--accent-soft);
		border-left-color: var(--accent);
		color: var(--text-primary);
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
		font-family: var(--font-display);
		font-size: 34px;
		font-weight: 400;
		line-height: 1.05;
		letter-spacing: -0.015em;
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
		padding: 1px 7px;
		border-radius: var(--radius-full);
		background: var(--accent-soft);
		color: var(--accent);
	}

	.wiki-content-body {
		font-size: 14px;
		line-height: 1.7;
		min-width: 0;
	}

	/* MarkdownBody defaults are compact (task summaries). Wiki pages are documents. */
	.wiki-content-body :global(.markdown-body) {
		font-size: 14px;
		line-height: 1.7;
	}

	.wiki-content-body :global(.markdown-body h1) {
		font-size: 1.55em;
		margin: 1.1em 0 0.45em;
		padding-bottom: 0.25em;
		border-bottom: 1px solid var(--border);
	}

	.wiki-content-body :global(.markdown-body h2) {
		font-size: 1.3em;
		margin: 1em 0 0.4em;
	}

	.wiki-content-body :global(.markdown-body h3) {
		font-size: 1.12em;
		margin: 0.9em 0 0.3em;
	}

	.wiki-content-body :global(.markdown-body h4) {
		font-size: 1.02em;
		margin: 0.8em 0 0.25em;
	}

	.wiki-content-body :global(.markdown-body p) {
		margin: 0.55em 0;
	}

	.wiki-content-body :global(.markdown-body ul),
	.wiki-content-body :global(.markdown-body ol) {
		margin: 0.45em 0;
		padding-left: 1.4em;
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

		.wiki-layout.nav-collapsed .wiki-sidebar {
			width: 36px;
		}
	}
</style>
