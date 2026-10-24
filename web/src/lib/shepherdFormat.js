// Small formatters shared by the shepherd screen sections (#621). The page,
// ShepherdTaskList, ShepherdPatrolLog and AttentionPanel all stamp the same
// "n분 전" / first-line / cost shapes, so the helpers live here once.

/** First line of a text blob, collapsed and truncated to 120 runes ("…" appended). */
export function firstLine(text) {
	if (!text) return '';
	const line = String(text).trim().split('\n')[0];
	const runes = [...line];
	return runes.length > 120 ? runes.slice(0, 120).join('') + '…' : line;
}

/** Relative "방금/N분 전/N시간 전/N일 전" label for an API timestamp. Empty for missing/invalid input. */
export function timeAgo(dateStr) {
	if (!dateStr) return '';
	const date = new Date(String(dateStr).replace(' ', 'T'));
	if (Number.isNaN(date.getTime())) return '';
	const diff = Math.floor((Date.now() - date.getTime()) / 1000);
	if (diff < 60) return '방금';
	if (diff < 3600) return `${Math.floor(diff / 60)}분 전`;
	if (diff < 86400) return `${Math.floor(diff / 3600)}시간 전`;
	return `${Math.floor(diff / 86400)}일 전`;
}

/** USD amount → "$1,234" at ≥100, else two decimals. */
export function fmtCost(n) {
	const v = Number(n) || 0;
	return v >= 100 ? `$${Math.round(v).toLocaleString()}` : `$${v.toFixed(2)}`;
}
