import { apiGet, apiPost } from './api.js';

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
		throw new Error('이 브라우저는 Web Push를 지원하지 않습니다. HTTPS로 접속했는지 확인해 주세요.');
	}
	if (isIOS() && !isStandaloneDisplay()) {
		throw new Error('iOS에서는 홈 화면에 추가한 뒤에만 푸시를 켤 수 있습니다.');
	}

	const status = await fetchPushStatus();
	const key = status?.vapid_public_key;
	if (!key) {
		throw new Error('서버 VAPID 공개키를 받지 못했습니다. 데몬을 재시작해 주세요.');
	}

	const perm = await Notification.requestPermission();
	if (perm !== 'granted') {
		throw new Error('알림 권한이 거부되었습니다.');
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
		throw new Error(res?.message || '구독 등록에 실패했습니다.');
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
		throw new Error(res?.message || '테스트 전송에 실패했습니다.');
	}
	return res.data;
}
