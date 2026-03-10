<script>
	import { onMount, tick } from 'svelte';
	import { apiGet, apiDownload } from '$lib/api.js';
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';
	import '$lib/style/github-markdown.css';
	import hljs from 'highlight.js/lib/common';
	import 'highlight.js/styles/github-dark.css';

	const carta = new Carta({ sanitizer: DOMPurify.sanitize });

	let { projectName } = $props();

	let currentPath = $state('');
	let entries = $state([]);
	let loading = $state(false);
	let error = $state('');

	let selectedFile = $state(null);
	let fileContent = $state('');
	let renderedMarkdown = $state('');
	let fileLoading = $state(false);
	let fileMeta = $state(null); // for binary/too-large info

	let breadcrumbs = $derived.by(() => {
		const crumbs = [{ name: 'root', path: '' }];
		if (!currentPath) return crumbs;
		const parts = currentPath.split('/');
		for (let i = 0; i < parts.length; i++) {
			crumbs.push({
				name: parts[i],
				path: parts.slice(0, i + 1).join('/')
			});
		}
		return crumbs;
	});

	onMount(() => {
		navigateTo('');
	});

	async function navigateTo(dirPath) {
		loading = true;
		error = '';
		currentPath = dirPath;
		selectedFile = null;
		fileContent = '';
		renderedMarkdown = '';
		fileMeta = null;

		const res = await apiGet(
			`/api/projects/${encodeURIComponent(projectName)}/files?path=${encodeURIComponent(dirPath)}`
		);
		if (res?.data) {
			entries = res.data;
		} else {
			entries = [];
			if (res?.message) error = res.message;
		}
		loading = false;
	}

	async function openFile(entry) {
		fileLoading = true;
		selectedFile = entry;
		fileContent = '';
		renderedMarkdown = '';
		fileMeta = null;

		const res = await apiGet(
			`/api/projects/${encodeURIComponent(projectName)}/files/content/${entry.path}`
		);
		if (res?.data) {
			fileMeta = res.data;
			if (res.data.is_binary || res.data.is_too_large) {
				// no content to render
			} else if (res.data.language === 'markdown') {
				fileContent = res.data.content;
				renderedMarkdown = await carta.render(fileContent);
			} else {
				fileContent = res.data.content;
				await tick();
				const el = document.querySelector('.fb-code code');
				if (el) {
					el.removeAttribute('data-highlighted');
					hljs.highlightElement(el);
				}
			}
		}
		fileLoading = false;
	}

	function closeFile() {
		selectedFile = null;
		fileContent = '';
		renderedMarkdown = '';
		fileMeta = null;
	}

	function handleEntryClick(entry) {
		if (entry.is_dir) {
			navigateTo(entry.path);
		} else {
			openFile(entry);
		}
	}

	async function downloadFile(entry) {
		const url = `/api/projects/${encodeURIComponent(projectName)}/files/download/${entry.path}`;
		const blob = await apiDownload(url);
		if (!blob) return;
		const a = document.createElement('a');
		a.href = URL.createObjectURL(blob);
		a.download = entry.name || entry.path.split('/').pop();
		a.click();
		URL.revokeObjectURL(a.href);
	}

	function formatFileSize(bytes) {
		if (!bytes) return '';
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
		return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	}

	function fileIcon(entry) {
		if (entry.is_dir) return '\uD83D\uDCC1';
		const ext = entry.name.includes('.') ? entry.name.split('.').pop().toLowerCase() : '';
		const iconMap = {
			go: '\uD83D\uDC39', py: '\uD83D\uDC0D', js: '\uD83D\uDFE8', ts: '\uD83D\uDD35',
			md: '\uD83D\uDCDD', json: '{}', yaml: '\u2699\uFE0F', yml: '\u2699\uFE0F',
			svelte: '\uD83D\uDD36', html: '\uD83C\uDF10', css: '\uD83C\uDFA8',
			sh: '\uD83D\uDCBB', sql: '\uD83D\uDDC4\uFE0F', rs: '\uD83E\uDD80',
		};
		return iconMap[ext] || '\uD83D\uDCC4';
	}
</script>

<div class="fb">
	{#if selectedFile}
		<!-- File viewer -->
		<div class="fb-viewer">
			<div class="fb-viewer-header">
				<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
				<span class="fb-back" onclick={closeFile}>&larr; Back</span>
				<span class="fb-viewer-title mono">{selectedFile.path}</span>
				<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
				<span class="fb-dl-btn" onclick={() => downloadFile(selectedFile)} title="Download">
					<svg viewBox="0 0 16 16" fill="currentColor" width="14" height="14"><path d="M2.75 14A1.75 1.75 0 011 12.25v-2.5a.75.75 0 011.5 0v2.5c0 .138.112.25.25.25h10.5a.25.25 0 00.25-.25v-2.5a.75.75 0 011.5 0v2.5A1.75 1.75 0 0113.25 14H2.75z"/><path d="M7.25 7.689V2a.75.75 0 011.5 0v5.689l1.97-1.969a.749.749 0 111.06 1.06l-3.25 3.25a.749.749 0 01-1.06 0L4.22 6.78a.749.749 0 111.06-1.06l1.97 1.969z"/></svg>
				</span>
			</div>
			<div class="fb-viewer-content">
				{#if fileLoading}
					<p class="text-muted">Loading...</p>
				{:else if fileMeta?.is_binary}
					<div class="fb-placeholder">
						<p>Binary file ({formatFileSize(fileMeta.size)})</p>
						<p class="text-muted">{fileMeta.mime_type}</p>
						<button class="btn btn-sm" onclick={() => downloadFile(selectedFile)}>Download</button>
					</div>
				{:else if fileMeta?.is_too_large}
					<div class="fb-placeholder">
						<p>File too large to display ({formatFileSize(fileMeta.size)})</p>
						<button class="btn btn-sm" onclick={() => downloadFile(selectedFile)}>Download</button>
					</div>
				{:else if renderedMarkdown}
					<div class="markdown-body">{@html renderedMarkdown}</div>
				{:else if fileContent}
					<pre class="fb-code"><code class={fileMeta?.language ? `language-${fileMeta.language}` : ''}>{fileContent}</code></pre>
				{:else}
					<p class="text-muted">Empty file</p>
				{/if}
			</div>
		</div>
	{:else}
		<!-- Directory listing -->
		<div class="fb-breadcrumb">
			{#each breadcrumbs as crumb, i}
				{#if i > 0}<span class="fb-sep">/</span>{/if}
				{#if i < breadcrumbs.length - 1}
					<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
					<span class="fb-crumb-link" onclick={() => navigateTo(crumb.path)}>{crumb.name}</span>
				{:else}
					<span class="fb-crumb-current">{crumb.name}</span>
				{/if}
			{/each}
		</div>

		{#if loading}
			<p class="text-muted">Loading...</p>
		{:else if error}
			<p class="text-muted">{error}</p>
		{:else if entries.length === 0}
			<p class="text-muted">Empty directory</p>
		{:else}
			<div class="fb-list">
				{#each entries as entry}
					<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
					<div class="fb-entry" onclick={() => handleEntryClick(entry)}>
						<span class="fb-icon">{fileIcon(entry)}</span>
						<span class="fb-name" class:fb-dir={entry.is_dir}>{entry.name}</span>
						<span class="fb-size text-muted">{entry.is_dir ? '' : formatFileSize(entry.size)}</span>
						<span class="fb-time text-muted">{entry.modified_at || ''}</span>
					</div>
				{/each}
			</div>
		{/if}
	{/if}
</div>

<style>
	.fb {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
	}

	/* Breadcrumb */
	.fb-breadcrumb {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 8px 12px;
		font-size: 13px;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
		flex-wrap: wrap;
	}
	.fb-sep { color: var(--text-secondary); }
	.fb-crumb-link {
		color: var(--accent);
		cursor: pointer;
	}
	.fb-crumb-link:hover { text-decoration: underline; }
	.fb-crumb-current {
		color: var(--text-primary);
		font-weight: 600;
	}

	/* Directory listing */
	.fb-list {
		flex: 1;
		overflow-y: auto;
	}
	.fb-entry {
		display: grid;
		grid-template-columns: 28px 1fr auto auto;
		align-items: center;
		gap: 6px;
		padding: 6px 12px;
		cursor: pointer;
		border-bottom: 1px solid var(--border-light, rgba(255,255,255,0.04));
		font-size: 13px;
		transition: background 0.1s;
	}
	.fb-entry:hover { background: var(--bg-hover, rgba(255,255,255,0.04)); }
	.fb-icon { font-size: 15px; text-align: center; }
	.fb-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.fb-dir { font-weight: 600; }
	.fb-size { font-size: 12px; min-width: 60px; text-align: right; }
	.fb-time { font-size: 12px; min-width: 130px; text-align: right; }

	/* File viewer */
	.fb-viewer {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
	}
	.fb-viewer-header {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 8px 12px;
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
	}
	.fb-back {
		color: var(--accent);
		cursor: pointer;
		font-size: 13px;
		font-weight: 600;
		white-space: nowrap;
	}
	.fb-back:hover { text-decoration: underline; }
	.fb-viewer-title {
		flex: 1;
		font-size: 13px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--text-secondary);
	}
	.fb-dl-btn {
		cursor: pointer;
		color: var(--text-secondary);
		padding: 4px;
		border-radius: 4px;
		transition: color 0.15s;
	}
	.fb-dl-btn:hover { color: var(--accent); }
	.fb-viewer-content {
		flex: 1;
		overflow: auto;
		padding: 16px;
	}

	/* Code block */
	.fb-code {
		margin: 0;
		padding: 0;
		background: transparent;
		font-size: 13px;
		line-height: 1.5;
		tab-size: 4;
		white-space: pre-wrap;
		word-break: break-all;
	}
	.fb-code code {
		font-family: 'SF Mono', 'Fira Code', 'JetBrains Mono', monospace;
	}

	/* Placeholder for binary / too-large */
	.fb-placeholder {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 8px;
		height: 200px;
		color: var(--text-secondary);
		font-size: 14px;
	}
	.fb-placeholder .btn {
		margin-top: 8px;
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
		transition: opacity 0.15s;
	}
	.btn-sm:hover { opacity: 0.85; }

	.text-muted { color: var(--text-secondary); }
	.mono { font-family: 'SF Mono', 'Fira Code', monospace; }

	/* Markdown body */
	.fb-viewer-content :global(.markdown-body) {
		background: transparent;
		color: var(--text-primary);
		font-size: 14px;
		line-height: 1.6;
	}

	/* highlight.js theme overrides for our dark bg */
	.fb-viewer-content :global(.hljs) {
		background: transparent;
		padding: 0;
	}

	@media (max-width: 768px) {
		.fb-entry {
			grid-template-columns: 28px 1fr auto;
		}
		.fb-time { display: none; }
	}
</style>
