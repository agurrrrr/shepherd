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

// App state
export const isAuthenticated = writable(false);
export const sheep = writable([]);
export const projects = writable([]);
export const systemStatus = writable(null);
