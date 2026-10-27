<script>
	import Icon from './Icon.svelte';
	import { onMount } from 'svelte';
	import { apiGet, apiUpload } from '$lib/api.js';
	import { t, translate } from '$lib/i18n/index.js';
	import { accessToken } from '$lib/stores.js';
	import { get } from 'svelte/store';
	import { Carta } from 'carta-md';
	import DOMPurify from 'isomorphic-dompurify';
	import '$lib/style/github-markdown.css';
	// highlight.js core classes are themed in app.css layer 6 (PICO palette on a
	// black well) — no third-party theme stylesheet is imported.
	import hljs from 'highlight.js/lib/common';
	import html2canvas from 'html2canvas';
	import { jsPDF } from 'jspdf';

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
	let pdfExporting = $state(false);
	let mdBodyEl = $state(null);
	let codeEl = $state(null);

	let uploading = $state(false);
	let uploadMsg = $state(null); // { type: 'ok' | 'err', text }
	let dragging = $state(false);
	let fileInputEl = $state(null);
	let dragDepth = 0;

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

	$effect(() => {
		if (codeEl && fileContent && !renderedMarkdown) {
			codeEl.removeAttribute('data-highlighted');
			hljs.highlightElement(codeEl);
		}
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

	let imageUrl = $state('');

	async function openFile(entry) {
		fileLoading = true;
		selectedFile = entry;
		fileContent = '';
		renderedMarkdown = '';
		fileMeta = null;
		imageUrl = '';

		if (isImageFile(entry.name)) {
			imageUrl = getFileUrl(entry);
			fileLoading = false;
			return;
		}

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
			}
		}
		fileLoading = false;
	}

	function closeFile() {
		selectedFile = null;
		fileContent = '';
		renderedMarkdown = '';
		fileMeta = null;
		imageUrl = '';
	}

	function handleEntryClick(entry) {
		if (entry.is_dir) {
			navigateTo(entry.path);
		} else {
			openFile(entry);
		}
	}

	function triggerUpload() {
		fileInputEl?.click();
	}

	function handleUploadSelect(e) {
		const files = Array.from(e.target.files || []);
		e.target.value = '';
		if (files.length) uploadFiles(files);
	}

	async function uploadFiles(fileList) {
		const files = Array.from(fileList || []);
		if (files.length === 0 || uploading) return;

		const tooBig = files.find((f) => f.size > 10 * 1024 * 1024);
		if (tooBig) {
			uploadMsg = { type: 'err', text: `${tooBig.name} exceeds 10MB limit` };
			return;
		}

		uploading = true;
		uploadMsg = null;
		try {
			const formData = new FormData();
			for (const f of files) formData.append('files', f, f.name);
			const res = await apiUpload(
				`/api/projects/${encodeURIComponent(projectName)}/files/upload?path=${encodeURIComponent(currentPath)}`,
				formData
			);
			if (res?.success) {
				uploadMsg = {
					type: 'ok',
					text:
						files.length > 1
							? translate('files.uploadedMany', { count: files.length })
							: translate('files.uploadedOne', { name: files[0].name })
				};
				await navigateTo(currentPath);
			} else {
				uploadMsg = { type: 'err', text: res?.message || 'Upload failed' };
			}
		} catch (err) {
			uploadMsg = { type: 'err', text: err.message || 'Upload failed' };
		} finally {
			uploading = false;
		}
	}

	function handleDragEnter(e) {
		if (!e.dataTransfer?.types?.includes('Files')) return;
		dragDepth++;
		dragging = true;
	}

	function handleDragOver(e) {
		if (!e.dataTransfer?.types?.includes('Files')) return;
		e.preventDefault();
		e.dataTransfer.dropEffect = 'copy';
	}

	function handleDragLeave() {
		dragDepth = Math.max(0, dragDepth - 1);
		if (dragDepth === 0) dragging = false;
	}

	function handleDrop(e) {
		if (!e.dataTransfer?.files?.length) return;
		e.preventDefault();
		dragDepth = 0;
		dragging = false;
		uploadFiles(e.dataTransfer.files);
	}

	function downloadFile(entry) {
		const token = get(accessToken);
		const url = `/api/projects/${encodeURIComponent(projectName)}/files/download/${entry.path}?token=${encodeURIComponent(token)}`;
		const a = document.createElement('a');
		a.href = url;
		a.download = entry.name || entry.path.split('/').pop();
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
	}

	async function downloadPDF() {
		if (!mdBodyEl || !selectedFile || pdfExporting) return;
		pdfExporting = true;
		try {
			// Temporarily expand scroll containers so html2canvas captures full content
			const scrollParent = mdBodyEl.closest('.fb-viewer-content');
			const viewer = mdBodyEl.closest('.fb-viewer');
			const savedStyles = [];
			for (const el of [scrollParent, viewer]) {
				if (!el) continue;
				savedStyles.push({ el, overflow: el.style.overflow, height: el.style.height, maxHeight: el.style.maxHeight });
				el.style.overflow = 'visible';
				el.style.height = 'auto';
				el.style.maxHeight = 'none';
			}

			const canvas = await html2canvas(mdBodyEl, {
				scale: 2,
				useCORS: true,
				backgroundColor: '#18181b'
			});

			// Restore original styles
			for (const s of savedStyles) {
				s.el.style.overflow = s.overflow;
				s.el.style.height = s.height;
				s.el.style.maxHeight = s.maxHeight;
			}

			const imgWidth = 210;
			const pageHeight = 297;
			const margin = 10;
			const contentWidth = imgWidth - margin * 2;
			const imgHeight = (canvas.height * contentWidth) / canvas.width;

			const pdf = new jsPDF('p', 'mm', 'a4');
			let y = margin;
			let remaining = imgHeight;
			const pageContentHeight = pageHeight - margin * 2;

			while (remaining > 0) {
				if (y !== margin) pdf.addPage();
				pdf.addImage(
					canvas.toDataURL('image/png'),
					'PNG',
					margin,
					y === margin ? margin : margin - (imgHeight - remaining),
					contentWidth,
					imgHeight
				);
				remaining -= pageContentHeight;
				y = margin;
			}

			const name = selectedFile.name.replace(/\.md$/, '');
			pdf.save(`${name}.pdf`);
		} catch (e) {
			console.error('PDF export failed:', e);
		} finally {
			pdfExporting = false;
		}
	}

	function formatFileSize(bytes) {
		if (!bytes) return '';
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
		return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	}

	const imageExts = new Set(['png', 'jpg', 'jpeg', 'gif', 'bmp', 'webp', 'svg', 'ico', 'avif']);

	function isImageFile(name) {
		const ext = name.includes('.') ? name.split('.').pop().toLowerCase() : '';
		return imageExts.has(ext);
	}

	function getFileUrl(entry) {
		const token = get(accessToken);
		return `/api/projects/${encodeURIComponent(projectName)}/files/download/${entry.path}?token=${encodeURIComponent(token)}`;
	}

	// File kind → line icon + tint class (fb-k-*). Kinds stay coarse on purpose:
	// the listing should read as one calm column, not a sticker sheet.
	const KIND_BY_EXT = {
		go: 'code', py: 'code', js: 'code', ts: 'code', svelte: 'code', html: 'code',
		css: 'code', sh: 'code', sql: 'code', rs: 'code', java: 'code', c: 'code',
		cpp: 'code', h: 'code', rb: 'code', kt: 'code', swift: 'code',
		md: 'doc', txt: 'doc', pdf: 'doc',
		json: 'config', yaml: 'config', yml: 'config', toml: 'config', ini: 'config', xml: 'config', env: 'config',
		png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', webp: 'image', svg: 'image',
		bmp: 'image', ico: 'image', avif: 'image',
	};
	const ICON_BY_KIND = { dir: 'folder', code: 'code', doc: 'file-text', config: 'braces', image: 'image', file: 'file' };

	function fileKind(entry) {
		if (entry.is_dir) return 'dir';
		const ext = entry.name.includes('.') ? entry.name.split('.').pop().toLowerCase() : '';
		return KIND_BY_EXT[ext] || 'file';
	}
</script>

<div class="fb">
	{#if selectedFile}
		<!-- File viewer -->
		<div class="fb-viewer">
			<div class="fb-viewer-header">
				<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
				<span class="fb-back" onclick={closeFile}><Icon name="arrow-left" size={13} /> Back</span>
				<span class="fb-viewer-title mono">{selectedFile.path}</span>
				{#if renderedMarkdown}
					<button class="fb-pdf-btn" onclick={downloadPDF} disabled={pdfExporting}>
						{pdfExporting ? 'Exporting...' : 'PDF'}
					</button>
				{/if}
				<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
				<span class="fb-dl-btn" onclick={() => downloadFile(selectedFile)} title="Download">
					<Icon name="download" size={15} />
				</span>
			</div>
			<div class="fb-viewer-content">
				{#if fileLoading}
					<p class="text-muted">Loading...</p>
				{:else if imageUrl}
					<div class="fb-image-preview">
						<img src={imageUrl} alt={selectedFile.name} />
					</div>
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
					<div class="markdown-body" bind:this={mdBodyEl}>{@html renderedMarkdown}</div>
				{:else if fileContent}
					<pre class="fb-code"><code bind:this={codeEl} class={fileMeta?.language ? `language-${fileMeta.language}` : ''}>{fileContent}</code></pre>
				{:else}
					<p class="text-muted">Empty file</p>
				{/if}
			</div>
		</div>
	{:else}
		<!-- Directory listing -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="fb-dirlisting"
			class:fb-dragging={dragging}
			ondragenter={handleDragEnter}
			ondragover={handleDragOver}
			ondragleave={handleDragLeave}
			ondrop={handleDrop}
		>
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
				<button class="btn btn-sm fb-upload-btn" onclick={triggerUpload} disabled={uploading}>
					<Icon name="upload" size={13} />
					{uploading ? 'Uploading…' : 'Upload'}
				</button>
				<input bind:this={fileInputEl} type="file" multiple hidden onchange={handleUploadSelect} />
			</div>

			{#if uploadMsg}
				<div class="fb-upload-msg" class:fb-err={uploadMsg.type === 'err'}>{uploadMsg.text}</div>
			{/if}

			{#if loading}
				<p class="text-muted fb-pad">Loading...</p>
			{:else if error}
				<p class="text-muted fb-pad">{error}</p>
			{:else if entries.length === 0}
				<p class="text-muted fb-pad">Empty directory</p>
			{:else}
				<div class="fb-list">
					{#each entries as entry}
						{@const kind = fileKind(entry)}
						<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
						<div class="fb-entry" onclick={() => handleEntryClick(entry)}>
							<span class="fb-icon fb-k-{kind}" aria-hidden="true"><Icon name={ICON_BY_KIND[kind]} size={15} /></span>
							<span class="fb-name" class:fb-dir={entry.is_dir}>{entry.name}</span>
							<span class="fb-size text-muted">{entry.is_dir ? '' : formatFileSize(entry.size)}</span>
							<span class="fb-time text-muted">{entry.modified_at || ''}</span>
						</div>
					{/each}
				</div>
			{/if}

			{#if dragging}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div class="fb-drop-overlay">{$t('files.dropHere')}</div>
			{/if}
		</div>
	{/if}
</div>

<style>
	.fb {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
	}

	/* Directory listing wrapper (drop target) */
	.fb-dirlisting {
		position: relative;
		display: flex;
		flex-direction: column;
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}
	.fb-dirlisting.fb-dragging {
		background: color-mix(in srgb, var(--accent) 8%, transparent);
	}
	.fb-drop-overlay {
		position: absolute;
		inset: 8px;
		display: flex;
		align-items: center;
		justify-content: center;
		border: 2px dashed var(--accent);
		border-radius: 0;
		background: color-mix(in srgb, var(--bg-primary) 70%, transparent);
		color: var(--accent);
		font-size: var(--fs-base);
		font-weight: 600;
		pointer-events: none;
		z-index: 5;
	}
	.fb-pad { padding: 8px 12px; }
	.fb-upload-btn {
		margin-left: auto;
	}
	.fb-upload-msg {
		padding: 6px 12px;
		font-size: var(--fs-xs);
		color: var(--accent);
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
	}
	.fb-upload-msg.fb-err { color: var(--danger); }

	/* Breadcrumb */
	.fb-breadcrumb {
		display: flex;
		align-items: center;
		gap: 4px;
		min-height: 46px;
		padding: 8px 12px 8px 16px;
		font-family: var(--font-mono);
		font-size: var(--fs-xs);
		border-bottom: 1px solid var(--border);
		flex-shrink: 0;
		flex-wrap: wrap;
	}
	.fb-sep { color: var(--text-tertiary); padding: 0 2px; }
	.fb-crumb-link {
		color: var(--text-secondary);
		cursor: pointer;
		transition: color var(--dur-1) steps(2);
	}
	.fb-crumb-link:hover { color: var(--accent); }
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
		gap: 10px;
		padding: 7px 16px;
		cursor: pointer;
		border-bottom: 1px solid var(--border-subtle);
		font-size: var(--fs-sm);
		transition: background var(--dur-1) steps(2);
	}
	.fb-entry:hover { background: var(--bg-hover); }
	.fb-entry:hover .fb-name { color: var(--text-primary); }
	.fb-icon {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border-radius: 0;
		color: var(--text-tertiary);
	}
	.fb-k-dir { color: var(--accent); background: var(--accent-soft); }
	.fb-k-code { color: var(--live); }
	.fb-k-doc { color: var(--wool); }
	.fb-k-config { color: var(--queued-text); }
	.fb-k-image { color: var(--info); }
	.fb-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-secondary); transition: color var(--dur-1) steps(2); }
	.fb-dir { font-weight: var(--fw-medium); color: var(--text-primary); }
	.fb-size, .fb-time { font-family: var(--font-mono); font-size: var(--fs-2xs); color: var(--text-tertiary); font-variant-numeric: tabular-nums; text-align: right; }
	.fb-size { min-width: 64px; }
	.fb-time { min-width: 140px; }

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
		display: inline-flex;
		align-items: center;
		gap: 6px;
		color: var(--text-secondary);
		cursor: pointer;
		font-size: var(--fs-sm);
		font-weight: var(--fw-medium);
		white-space: nowrap;
		transition: color var(--dur-1) steps(2);
	}
	.fb-back:hover { color: var(--accent); }
	.fb-viewer-title {
		flex: 1;
		font-size: var(--fs-sm);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--text-secondary);
	}
	.fb-dl-btn {
		cursor: pointer;
		color: var(--text-secondary);
		padding: 4px;
		border-radius: 0;
		transition: color 0.15s;
	}
	.fb-dl-btn:hover { color: var(--accent); }
	.fb-pdf-btn {
		padding: 2px 10px;
		font-size: var(--fs-2xs);
		font-weight: 600;
		background: var(--accent);
		color: var(--accent-ink);
		border: none;
		border-radius: 0;
		cursor: pointer;
		white-space: nowrap;
		transition: opacity 0.15s;
	}
	.fb-pdf-btn:hover { opacity: 0.85; }
	.fb-pdf-btn:disabled { opacity: 0.5; cursor: default; }
	.fb-viewer-content {
		flex: 1;
		overflow: auto;
		padding: 16px;
	}

	/* Code block: black well */
	.fb-code {
		margin: 0;
		padding: 12px;
		background: #000;
		font-size: var(--fs-sm);
		line-height: 1.5;
		tab-size: 4;
		white-space: pre-wrap;
		word-break: break-all;
	}
	.fb-code code {
		font-family: var(--font-mono);
		background: transparent;
		padding: 0;
	}

	/* Image preview */
	.fb-image-preview {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 200px;
	}
	.fb-image-preview img {
		max-width: 100%;
		max-height: 80vh;
		border-radius: 0;
		object-fit: contain;
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
		font-size: var(--fs-base);
	}
	.fb-placeholder .btn {
		margin-top: 8px;
	}


	.text-muted { color: var(--text-secondary); }
	.mono { font-family: var(--font-mono); }

	/* Markdown body */
	.fb-viewer-content :global(.markdown-body) {
		background: transparent;
		color: var(--text-primary);
		font-size: var(--fs-base);
		line-height: 1.6;
	}

	/* hljs colours come from app.css layer 6; the black well is the pre itself. */
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
