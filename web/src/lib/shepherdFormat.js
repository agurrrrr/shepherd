// Small formatters shared by the shepherd screen sections (#621). The page,
// ShepherdTaskList, ShepherdPatrolLog and AttentionPanel all stamp the same
// relative-time / first-line / cost shapes, so the helpers live here once.
import { translate } from './i18n/index.js';

/** First line of a text blob, collapsed and truncated to 120 runes ("…" appended). */
export function firstLine(text) {
	if (!text) return '';
	const line = String(text).trim().split('\n')[0];
	const runes = [...line];
	return runes.length > 120 ? runes.slice(0, 120).join('') + '…' : line;
}

/**
 * Relative label for an API timestamp in the current locale (ko "5분 전",
 * en "5m ago"). Empty for missing/invalid input. translate() keeps it
 * reactive to locale changes when called from a template.
 */
export function timeAgo(dateStr) {
	if (!dateStr) return '';
	const date = new Date(String(dateStr).replace(' ', 'T'));
	if (Number.isNaN(date.getTime())) return '';
	const diff = Math.floor((Date.now() - date.getTime()) / 1000);
	if (diff < 60) return translate('time.justNow');
	if (diff < 3600) return translate('time.minutesAgo', { count: Math.floor(diff / 60) });
	if (diff < 86400) return translate('time.hoursAgo', { count: Math.floor(diff / 3600) });
	return translate('time.daysAgo', { count: Math.floor(diff / 86400) });
}

/** USD amount → "$1,234" at ≥100, else two decimals. */
export function fmtCost(n) {
	const v = Number(n) || 0;
	return v >= 100 ? `$${Math.round(v).toLocaleString()}` : `$${v.toFixed(2)}`;
}
