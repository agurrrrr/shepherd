import { writable } from 'svelte/store';

/**
 * localStorage에 영속 저장되는 writable store
 * @param {string} key
 * @param {any} initial
 */
function persisted(key, initial) {
	const stored = typeof localStorage !== 'undefined' ? localStorage.getItem(key) : null;
	const value = stored ? JSON.parse(stored) : initial;
	const store = writable(value);

	store.subscribe((v) => {
		if (typeof localStorage !== 'undefined') {
			localStorage.setItem(key, JSON.stringify(v));
		}
	});

	return store;
}

// Auth tokens (persisted to localStorage)
export const accessToken = persisted('shepherd_access_token', '');
export const refreshToken = persisted('shepherd_refresh_token', '');
export const username = persisted('shepherd_username', '');

// Wireframe preset
export const wireframePreset = persisted('shepherd_wf_preset', 'default');
export const wireframeOptions = persisted('shepherd_wf_options', { compact: false, showGrid: false });

// Per-project OpenCode thinking-mode override. Shape: { [projectName]: bool }.
// Absence of a project key means "follow the global default" — explicit per-
// project overrides are stored only when the user toggles them.
export const thinkingByProject = persisted('shepherd_thinking_by_project', {});

// Per-project OpenCode model override. Shape: { [projectName]: string|null }.
// null means "follow the global default"; a non-empty string overrides it.
export const modelByProject = persisted('shepherd_model_by_project', {});

// App state
export const isAuthenticated = writable(false);
export const sheep = writable([]);
export const projects = writable([]);
export const systemStatus = writable(null);

// DefaultUploadLimitBytes is the per-file upload limit before /api/config has
// answered. It matches config.DefaultMaxUploadMB on the server (10 MB), so the
// browser never lets through a file the daemon would reject.
export const DEFAULT_UPLOAD_LIMIT_BYTES = 10 * 1024 * 1024;

// Per-file upload limit in bytes, read from the daemon's `max_upload_mb`
// setting (Settings → Common). The layout writes it once per sign-in from
// GET /api/config, and the Settings page refreshes it right after a save so a
// new limit applies without a reload. Upload call sites (task attachments, the
// Files tab) check against this store instead of a hard-coded 10 MB.
export const uploadLimitBytes = writable(DEFAULT_UPLOAD_LIMIT_BYTES);

// Needs-attention count (#599) for the sidebar's Shepherd badge. Written by
// the layout's debounced SSE refresh and by the Shepherd page's AttentionPanel
// (immediate feedback after ack/actions).
export const shepherdAttention = writable(0);
