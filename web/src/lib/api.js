import { get } from 'svelte/store';
import { accessToken, refreshToken, username, isAuthenticated } from './stores.js';

const BASE = '';

/**
 * @param {string} url
 * @param {RequestInit} [opts]
 * @returns {Promise<any>}
 */
async function request(url, opts = {}) {
	const token = get(accessToken);
	const headers = {
		'Content-Type': 'application/json',
		...(token ? { Authorization: `Bearer ${token}` } : {}),
		...(opts.headers || {})
	};

	const res = await fetch(BASE + url, { ...opts, headers });

	if (res.status === 401) {
		const refreshed = await tryRefresh();
		if (refreshed) {
			// Retry with new token
			const newToken = get(accessToken);
			headers.Authorization = `Bearer ${newToken}`;
			const retry = await fetch(BASE + url, { ...opts, headers });
			return retry.json();
		}
		logout();
		return null;
	}

	return res.json();
}

/** @param {string} url */
export function apiGet(url) {
	return request(url);
}

/**
 * @param {string} url
 * @param {object} body
 */
export function apiPost(url, body) {
	return request(url, { method: 'POST', body: JSON.stringify(body) });
}

/**
 * @param {string} url
 * @param {object} body
 */
export function apiPatch(url, body) {
	return request(url, { method: 'PATCH', body: JSON.stringify(body) });
}

/** @param {string} url */
export function apiDelete(url) {
	return request(url, { method: 'DELETE' });
}

/**
 * Upload files via multipart/form-data.
 * @param {string} url
 * @param {FormData} formData
 * @returns {Promise<any>}
 */
export async function apiUpload(url, formData) {
	const token = get(accessToken);
	const headers = token ? { Authorization: `Bearer ${token}` } : {};

	const res = await fetch(BASE + url, {
		method: 'POST',
		headers,
		body: formData
	});

	if (res.status === 401) {
		const refreshed = await tryRefresh();
		if (refreshed) {
			const newToken = get(accessToken);
			const retry = await fetch(BASE + url, {
				method: 'POST',
				headers: { Authorization: `Bearer ${newToken}` },
				body: formData
			});
			return retry.json();
		}
		logout();
		return null;
	}

	return res.json();
}

async function tryRefresh() {
	const token = get(refreshToken);
	if (!token) return false;

	try {
		const res = await fetch(BASE + '/api/auth/refresh', {
			method: 'POST',
			headers: { Authorization: `Bearer ${token}` }
		});

		if (res.ok) {
			const data = await res.json();
			accessToken.set(data.access_token);
			refreshToken.set(data.refresh_token);
			return true;
		}
	} catch {
		// ignore
	}
	return false;
}

export function logout() {
	accessToken.set('');
	refreshToken.set('');
	username.set('');
	isAuthenticated.set(false);
	if (typeof window !== 'undefined') {
		window.location.href = '/login';
	}
}

/**
 * @param {string} id
 * @param {string} password
 * @returns {Promise<{success: boolean, error?: string}>}
 */
export async function login(id, password) {
	try {
		const res = await fetch(BASE + '/api/auth/login', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ username: id, password })
		});

		const data = await res.json();

		if (res.ok && data.access_token) {
			accessToken.set(data.access_token);
			refreshToken.set(data.refresh_token);
			username.set(data.username);
			isAuthenticated.set(true);
			return { success: true };
		}

		return { success: false, error: data.message || 'Login failed' };
	} catch (err) {
		return { success: false, error: 'Connection failed' };
	}
}
