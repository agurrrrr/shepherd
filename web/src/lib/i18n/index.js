// Web UI i18n (#661, #662). A small module on top of svelte/store — no extra
// npm dependency.
//
// Usage
//   Components:  import { t } from '$lib/i18n/index.js';
//                {$t('issue.status.todo')}  {$t('time.minutesAgo', { count: 5 })}
//   JS modules:  import { translate } from './i18n/index.js';
//                throw new Error(translate('push.unsupported'));
//
// Keys are `area.item` (issue.status.todo, time.minutesAgo, push.unsupported).
// ko.js and en.js hold the same key set; `npm run check:i18n` checks it.
//
// Lookup order: the current locale's dictionary, then en (the base locale),
// then the key itself. A missing translation shows the English text; a key
// that no dictionary has shows up on screen as the key.
//
// Placeholders: `{name}` is replaced with String(params.name). A placeholder
// with no value in params stays as written. Do not build a sentence from
// translated fragments — word order differs between languages; put the
// variable part in a placeholder. Values are not formatted for you: format
// numbers and dates with formatNumber/formatDate (Intl with the current locale)
// before you pass them in, so ids like #11124 stay as they are.
//
// Plurals: give the key `.one` / `.other` variants and pass `count` in params.
// The form comes from Intl.PluralRules for the dictionary's locale; when that
// form is missing, `<key>.other` and then `<key>` are used. Korean only has
// "other", but ko.js still defines the same `.one`/`.other` keys as en.js
// (the key sets must match), with the same text in both. A string that reads
// the same for every count (e.g. "{count}m ago") keeps one plain key.
//
// Reactivity: `$t` changes when the locale changes, so `{$t(...)}` re-renders
// without a reload. translate() reads the locale through fromStore, so it is
// reactive too when it runs while a component renders (template expressions,
// $derived) — that is how timeAgo() in a template follows the locale. A string
// kept from earlier (a thrown Error message, a value set in onMount) stays in
// the language it was made in. In legacy `$:` statements use $t, because only
// the names in the statement are tracked there.
//
// Name clash: `$t` needs the imported store `t` in scope. A block variable
// named `t` (`{#each tasks as t}`) hides it, and any `$t` inside that block
// fails to compile (store_invalid_scoped_subscription). Rename the loop
// variable (e.g. `task`) instead of aliasing the store.
import { writable, derived, fromStore } from 'svelte/store';
import en from './en.js';
import ko from './ko.js';

export const LOCALES = ['ko', 'en'];
export const BASE_LOCALE = 'en';

/** @type {Record<string, Record<string, string>>} */
const dictionaries = { en, ko };

// Last locale the app used. The login screen cannot read /api/config before
// sign-in, and the first paint should not flash the other language while the
// config request is in flight.
const STORAGE_KEY = 'shepherd_locale';

/**
 * Supported locale for a value such as 'ko', 'EN' or 'ko-KR'; null otherwise.
 * @param {unknown} value
 * @returns {'ko'|'en'|null}
 */
export function normalizeLocale(value) {
	if (typeof value !== 'string') return null;
	const base = value.trim().toLowerCase().split(/[-_]/)[0];
	return base === 'ko' || base === 'en' ? base : null;
}

/**
 * Locale for a first visit with nothing stored: ko for a Korean browser, else en.
 * @param {string} [language]
 */
export function detectLocale(language = typeof navigator !== 'undefined' ? navigator.language : '') {
	return normalizeLocale(language) === 'ko' ? 'ko' : 'en';
}

function readStoredLocale() {
	if (typeof window === 'undefined') return null;
	try {
		return normalizeLocale(window.localStorage.getItem(STORAGE_KEY));
	} catch {
		return null; // storage disabled
	}
}

/** Current UI locale: 'ko' | 'en'. The layout sets it from the server `language` setting. */
export const locale = writable(readStoredLocale() ?? detectLocale());

locale.subscribe((value) => {
	if (typeof window === 'undefined') return;
	try {
		window.localStorage.setItem(STORAGE_KEY, value);
	} catch {
		// storage disabled — the locale still applies for this page load
	}
	document.documentElement.lang = value;
});

/**
 * Switch the UI locale. An unsupported value is ignored.
 * @param {unknown} value
 * @returns {'ko'|'en'|null} the locale applied, or null when ignored
 */
export function setLocale(value) {
	const next = normalizeLocale(value);
	if (next) locale.set(next);
	return next;
}

/** @type {Record<string, Intl.PluralRules>} */
const pluralRules = {};

function pluralForm(loc, count) {
	try {
		pluralRules[loc] ??= new Intl.PluralRules(loc);
		return pluralRules[loc].select(count);
	} catch {
		return 'other';
	}
}

function lookup(loc, key, count) {
	const dict = dictionaries[loc];
	if (!dict) return undefined;
	if (typeof count === 'number' && Number.isFinite(count)) {
		const form = dict[`${key}.${pluralForm(loc, count)}`] ?? dict[`${key}.other`];
		if (typeof form === 'string') return form;
	}
	const text = dict[key];
	return typeof text === 'string' ? text : undefined;
}

function interpolate(text, params) {
	if (!params) return text;
	return text.replace(/\{(\w+)\}/g, (match, name) =>
		Object.hasOwn(params, name) && params[name] != null ? String(params[name]) : match
	);
}

/**
 * @param {string} loc
 * @param {string} key
 * @param {Record<string, unknown>} [params]
 */
function translateIn(loc, key, params) {
	const count = params?.count;
	let text = lookup(loc, key, count);
	if (text === undefined && loc !== BASE_LOCALE) text = lookup(BASE_LOCALE, key, count);
	return text === undefined ? key : interpolate(text, params);
}

const currentLocale = fromStore(locale);

/**
 * Text for `key` in the current locale (falls back to en, then to the key).
 * @param {string} key
 * @param {Record<string, unknown>} [params] values for `{name}` placeholders; `count` also picks a plural form
 */
export function translate(key, params) {
	return translateIn(currentLocale.current, key, params);
}

/** Store of the translate function for the current locale. Components call `$t(key, params)`. */
export const t = derived(locale, ($locale) =>
	/** @param {string} key @param {Record<string, unknown>} [params] */
	(key, params) => translateIn($locale, key, params)
);

/**
 * Label for an enum value kept under `${prefix}.${value}` (issue.status.todo,
 * issue.type.bug). A value with no entry comes back unchanged rather than as
 * a raw key. Components pass `$t` so the label follows the locale.
 * @param {(key: string, params?: Record<string, unknown>) => string} tr
 * @param {string} prefix
 * @param {unknown} value
 */
export function enumLabel(tr, prefix, value) {
	if (value == null || value === '') return '';
	const key = `${prefix}.${value}`;
	const text = tr(key);
	return text === key ? String(value) : text;
}

/**
 * Number formatted for the current locale.
 * @param {number} value
 * @param {Intl.NumberFormatOptions} [options]
 */
export function formatNumber(value, options) {
	return new Intl.NumberFormat(currentLocale.current, options).format(value);
}

/**
 * Date formatted for the current locale. Accepts a Date, a timestamp or an
 * API string ("2026-10-27 22:20:06"); returns '' for an invalid value.
 * @param {Date|number|string} value
 * @param {Intl.DateTimeFormatOptions} [options]
 */
export function formatDate(value, options) {
	const date = value instanceof Date
		? value
		: new Date(typeof value === 'string' ? value.replace(' ', 'T') : value);
	if (Number.isNaN(date.getTime())) return '';
	return new Intl.DateTimeFormat(currentLocale.current, options).format(date);
}
