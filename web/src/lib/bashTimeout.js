// Bash-tool timeout plumbing for the `bash_timeout` setting.
//
// The daemon owns the default time one shell (bash) command may run before it is
// killed when the model does not pass its own `timeout` argument. It stores the
// value in seconds (internal/config: bash_timeout) and enforces it in execBash.
// The browser only needs the number to render the Settings field and its hint,
// so this module just mirrors the two server constants and normalizes what the
// form sends. Unlike max_upload_mb there is no upload call site that reads a
// store — the shell runs on the daemon — so there is no store here.
//
// BASH_TIMEOUT_CEIL_SECONDS mirrors config.BashTimeoutHardCeilSeconds and
// BASH_TIMEOUT_DEFAULT_SECONDS mirrors config.DefaultBashTimeoutSeconds. The
// daemon rejects an out-of-range or unparseable value with 400; the form sends a
// trimmed string (seconds or a duration like "5m") and the daemon normalizes it.

/** Hard ceiling for `bash_timeout`, in seconds (config.BashTimeoutHardCeilSeconds). */
export const BASH_TIMEOUT_CEIL_SECONDS = 3600;

/** Fallback for `bash_timeout`, in seconds (config.DefaultBashTimeoutSeconds). */
export const BASH_TIMEOUT_DEFAULT_SECONDS = 600;

/**
 * Normalize what the settings form sends for `bash_timeout`. The daemon accepts
 * whole seconds or a duration ("5m"), so a free-form text field is fine here —
 * the daemon validates and returns 400 on a typo. An empty field falls back to
 * the default number, mirroring how the Task Timeout field defaults to "4h", so
 * clearing the box does not send "" (which the daemon would reject).
 * @param {unknown} value raw field value (number or string)
 * @returns {string|number} a trimmed value, or the default when blank
 */
export function normalizeBashTimeoutInput(value) {
	if (value === null || value === undefined) {
		return BASH_TIMEOUT_DEFAULT_SECONDS;
	}
	const trimmed = String(value).trim();
	if (trimmed === '') {
		return BASH_TIMEOUT_DEFAULT_SECONDS;
	}
	return trimmed;
}
