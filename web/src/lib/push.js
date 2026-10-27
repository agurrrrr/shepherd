import { apiGet, apiPost } from './api.js';
import { translate } from './i18n/index.js';

/**
 * @param {string} base64String
 * @returns {Uint8Array}
 */
export function urlBase64ToUint8Array(base64String) {
	const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
	const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
	const raw = atob(base64);
	const out = new Uint8Array(raw.length);
	for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
	return out;
}

export function isSecureContextNow() {
	return typeof window !== 'undefined' && !!window.isSecureContext;
}

export function isPushSupported() {
	return (
		typeof window !== 'undefined' &&
		isSecureContextNow() &&
		'serviceWorker' in navigator &&
		'PushManager' in window &&
		'Notification' in window
	);
}

export function isStandaloneDisplay() {
	if (typeof window === 'undefined') return false;
	const mq = window.matchMedia?.('(display-mode: standalone)')?.matches;
	return !!(mq || window.navigator.standalone);
}

export function isIOS() {
	if (typeof navigator === 'undefined') return false;
	const ua = navigator.userAgent || '';
	return /iPad|iPhone|iPod/.test(ua) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}

/**
 * @returns {Promise<{enabled: boolean, vapid_public_key: string, subscription_count: number, notify_on_complete: boolean, notify_on_fail: boolean}|null>}
 */
export async function fetchPushStatus() {
	const res = await apiGet('/api/push/status');
	return res?.data || null;
}

export async function getCurrentSubscription() {
	if (!isPushSupported()) return null;
	const reg = await navigator.serviceWorker.ready;
	return reg.pushManager.getSubscription();
}

/**
 * Request permission, subscribe, and register the endpoint with the daemon.
 * Must be called from a user gesture (iOS).
 */
export async function enablePush() {
	if (!isPushSupported()) {
		throw new Error(translate('push.unsupported'));
	}
	if (isIOS() && !isStandaloneDisplay()) {
		throw new Error(translate('push.iosHomeScreen'));
	}

	const status = await fetchPushStatus();
	const key = status?.vapid_public_key;
	if (!key) {
		throw new Error(translate('push.noVapidKey'));
	}

	const perm = await Notification.requestPermission();
	if (perm !== 'granted') {
		throw new Error(translate('push.permissionDenied'));
	}

	const reg = await navigator.serviceWorker.ready;
	let sub = await reg.pushManager.getSubscription();
	if (!sub) {
		sub = await reg.pushManager.subscribe({
			userVisibleOnly: true,
			applicationServerKey: urlBase64ToUint8Array(key)
		});
	}

	const json = sub.toJSON();
	const res = await apiPost('/api/push/subscribe', {
		endpoint: json.endpoint,
		keys: json.keys,
		user_agent: navigator.userAgent
	});
	if (!res?.success) {
		throw new Error(res?.message || translate('push.subscribeFailed'));
	}
	return sub;
}

export async function disablePush() {
	const sub = await getCurrentSubscription();
	if (sub) {
		const json = sub.toJSON();
		await apiPost('/api/push/unsubscribe', { endpoint: json.endpoint });
		try {
			await sub.unsubscribe();
		} catch {
			// ignore
		}
	}
}

/**
 * If the OS already granted notification permission, re-register the
 * current PushSubscription so a rebuilt daemon store still receives pushes.
 */
export async function syncPushSubscription() {
	if (!isPushSupported()) return;
	if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;

	const status = await fetchPushStatus();
	const key = status?.vapid_public_key;
	if (!key) return;

	const reg = await navigator.serviceWorker.ready;
	let sub = await reg.pushManager.getSubscription();
	if (!sub) {
		try {
			sub = await reg.pushManager.subscribe({
				userVisibleOnly: true,
				applicationServerKey: urlBase64ToUint8Array(key)
			});
		} catch {
			return;
		}
	}
	const json = sub.toJSON();
	await apiPost('/api/push/subscribe', {
		endpoint: json.endpoint,
		keys: json.keys,
		user_agent: navigator.userAgent
	});
}

export async function sendTestPush() {
	const sub = await getCurrentSubscription();
	const res = await apiPost('/api/push/test', sub ? { endpoint: sub.endpoint } : {});
	if (!res?.success) {
		throw new Error(res?.message || translate('push.testFailed'));
	}
	return res.data;
}
