import { apiGet } from './api.js';
import { shepherdAttention } from './stores.js';

// Shared "needs attention" fetch (#599). One in-flight request is shared:
// the layout badge and the Shepherd page's panel call this on the same SSE
// debounce, and concurrent callers should not double-hit the endpoint.
// Returns the items (null on failure) and pushes the count into the store.

let inflight = null;

export function refreshShepherdAttention() {
	if (inflight) return inflight;
	inflight = apiGet('/api/shepherd/attention')
		.then((res) => {
			const items = res?.success && Array.isArray(res.data?.items) ? res.data.items : null;
			shepherdAttention.set(items ? items.length : 0);
			return items;
		})
		.catch(() => null)
		.finally(() => {
			inflight = null;
		});
	return inflight;
}
