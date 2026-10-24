import { apiGet } from './api.js';
import { shepherdAttention } from './stores.js';

// Shared "needs attention" fetch (#599, request count folded in by #623).
// One in-flight request is shared: the layout badge and the Shepherd page's
// panel call this on the same SSE debounce, and concurrent callers should not
// double-hit the endpoints.
//
// The badge number is items + pending shepherd requests. CollectAttention
// (internal/manager/attention.go) reports failures/warnings only — a pending
// user-confirmation request (#601) is NOT one of its kinds — so the count is
// assembled here instead, in one place, which keeps the sidebar badge and the
// 확인 필요 tab badge showing the same number. Resolves to
// { items, requests }; items is null when the attention endpoint failed.

let inflight = null;

export function refreshShepherdAttention() {
	if (inflight) return inflight;
	inflight = Promise.all([fetchItems(), fetchPendingRequests()])
		.then(([items, requests]) => {
			shepherdAttention.set(items === null ? 0 : items.length + requests.length);
			return { items, requests };
		})
		.finally(() => {
			inflight = null;
		});
	return inflight;
}

function fetchItems() {
	return apiGet('/api/shepherd/attention')
		.then((res) => (res?.success && Array.isArray(res.data?.items) ? res.data.items : null))
		.catch(() => null);
}

export function fetchPendingRequests() {
	return apiGet('/api/shepherd/requests?status=pending')
		.then((res) => (res?.success && Array.isArray(res.data?.requests) ? res.data.requests : []))
		.catch(() => []);
}
