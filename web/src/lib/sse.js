import { get } from 'svelte/store';
import { accessToken } from './stores.js';

let eventSource = null;
let reconnectTimer = null;
let reconnectDelay = 1000;
// Tracks whether we have ever opened a connection. Used to distinguish the
// initial connect from a reconnect-after-drop so we can fire a synthetic
// 'reconnect' event — listeners use it to re-sync state that may have changed
// while disconnected (e.g. a task finished and its status_change was missed).
let hasConnected = false;

/** @type {Record<string, ((event: any) => void)[]>} */
const listeners = {};

export function connectSSE() {
	if (eventSource) eventSource.close();

	const token = get(accessToken);
	if (!token) return;

	eventSource = new EventSource(`/api/events?token=${token}`);

	const eventTypes = ['output', 'task_start', 'task_complete', 'task_fail', 'status_change', 'provider_change', 'schedule_created', 'schedule_updated', 'schedule_deleted', 'schedule_triggered', 'skill_created', 'skill_updated', 'skill_deleted'];
	for (const type of eventTypes) {
		eventSource.addEventListener(type, (e) => {
			try {
				emit(type, JSON.parse(e.data));
			} catch {
				// ignore parse errors
			}
		});
	}

	eventSource.onopen = () => {
		reconnectDelay = 1000;
		// Re-sync after a dropped connection: any events broadcast while we were
		// disconnected (EventSource has no replay here) are lost, so notify
		// listeners to refetch authoritative state. Skipped on the first connect.
		if (hasConnected) emit('reconnect', {});
		hasConnected = true;
	};

	eventSource.onerror = () => {
		eventSource.close();
		reconnectTimer = setTimeout(() => {
			reconnectDelay = Math.min(reconnectDelay * 2, 30000);
			connectSSE();
		}, reconnectDelay);
	};
}

export function disconnectSSE() {
	if (eventSource) {
		eventSource.close();
		eventSource = null;
	}
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
	// Next connectSSE() is a fresh start, not a reconnect.
	hasConnected = false;
}

/**
 * @param {string} type
 * @param {(data: any) => void} callback
 * @returns {() => void} unsubscribe function
 */
export function onSSE(type, callback) {
	if (!listeners[type]) listeners[type] = [];
	listeners[type].push(callback);
	return () => {
		listeners[type] = listeners[type].filter((cb) => cb !== callback);
	};
}

function emit(type, data) {
	(listeners[type] || []).forEach((cb) => cb(data));
}
