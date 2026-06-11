package browser

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// NavTimeout bounds page-load operations (navigate, reload, wait_load),
// which can legitimately take longer than element operations.
const NavTimeout = 60 * time.Second

// findElement resolves a selector to an element. Beyond plain CSS, it accepts
// convenience prefixes so callers (especially the LLM) can target elements
// reliably on the first try instead of guessing brittle CSS and eating a full
// timeout on every miss:
//   - "text=Foo"  → first interactive element (a/button/[role=button]/input)
//     whose visible label contains "Foo" (case-insensitive)
//   - "xpath=..." or a selector starting with "//" → XPath
//
// Anything else is treated as a CSS selector (unchanged behaviour).
func findElement(page *rod.Page, selector string) (*rod.Element, error) {
	switch {
	case strings.HasPrefix(selector, "text="):
		txt := strings.TrimSpace(strings.TrimPrefix(selector, "text="))
		lit := xpathLiteral(strings.ToLower(txt))
		// lower-case the node text so the match is case-insensitive.
		const lc = `translate(normalize-space(.),'ABCDEFGHIJKLMNOPQRSTUVWXYZ','abcdefghijklmnopqrstuvwxyz')`
		vlc := `translate(normalize-space(@value),'ABCDEFGHIJKLMNOPQRSTUVWXYZ','abcdefghijklmnopqrstuvwxyz')`
		xpath := fmt.Sprintf(
			`//a[contains(%s,%s)] | //button[contains(%s,%s)] | `+
				`//*[@role="button"][contains(%s,%s)] | `+
				`//input[(@type="submit" or @type="button") and contains(%s,%s)]`,
			lc, lit, lc, lit, lc, lit, vlc, lit)
		return page.ElementX(xpath)
	case strings.HasPrefix(selector, "xpath="):
		return page.ElementX(strings.TrimPrefix(selector, "xpath="))
	case strings.HasPrefix(selector, "//"):
		return page.ElementX(selector)
	default:
		return page.Element(selector)
	}
}

// xpathLiteral safely quotes a string for use as an XPath string literal,
// handling embedded single quotes via concat().
func xpathLiteral(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	parts := strings.Split(s, "'")
	var b strings.Builder
	b.WriteString("concat(")
	for i, p := range parts {
		if i > 0 {
			b.WriteString(`,"'",`)
		}
		b.WriteString("'" + p + "'")
	}
	b.WriteString(")")
	return b.String()
}

// Navigate navigates to a URL.
func Navigate(sess *Session, pageName, url string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	page = page.Timeout(NavTimeout)
	if err := page.Navigate(url); err != nil {
		return fmt.Errorf("failed to navigate: %w", err)
	}
	return page.WaitLoad()
}

// Reload reloads the page.
func Reload(sess *Session, pageName string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	page = page.Timeout(NavTimeout)
	if err := page.Reload(); err != nil {
		return fmt.Errorf("failed to reload: %w", err)
	}
	return page.WaitLoad()
}

// GoBack navigates back.
func GoBack(sess *Session, pageName string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	return page.Timeout(NavTimeout).NavigateBack()
}

// GoForward navigates forward.
func GoForward(sess *Session, pageName string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	return page.Timeout(NavTimeout).NavigateForward()
}

// Click clicks an element.
func Click(sess *Session, pageName, selector string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return fmt.Errorf("element not found '%s': %w", selector, err)
	}

	return el.Click(proto.InputMouseButtonLeft, 1)
}

// Type types text into an element.
func Type(sess *Session, pageName, selector, text string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return fmt.Errorf("element not found '%s': %w", selector, err)
	}

	if err := el.SelectAllText(); err != nil {
		return err
	}
	return el.Input(text)
}

// SelectOption selects a dropdown option.
func SelectOption(sess *Session, pageName, selector, value string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return fmt.Errorf("element not found '%s': %w", selector, err)
	}

	return el.Select([]string{value}, true, rod.SelectorTypeText)
}

// SetCheckbox sets a checkbox state.
func SetCheckbox(sess *Session, pageName, selector string, checked bool) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return fmt.Errorf("element not found '%s': %w", selector, err)
	}

	isChecked, err := el.Property("checked")
	if err != nil {
		return err
	}

	if isChecked.Bool() != checked {
		return el.Click(proto.InputMouseButtonLeft, 1)
	}
	return nil
}

// Hover hovers over an element.
func Hover(sess *Session, pageName, selector string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return fmt.Errorf("element not found '%s': %w", selector, err)
	}

	return el.Hover()
}

// Scroll scrolls the page.
func Scroll(sess *Session, pageName string, x, y float64) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	return page.Timeout(DefaultTimeout).Mouse.Scroll(x, y, 1)
}

// ScrollToElement scrolls to an element.
func ScrollToElement(sess *Session, pageName, selector string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return fmt.Errorf("element not found '%s': %w", selector, err)
	}

	return el.ScrollIntoView()
}

// GetText returns the text content of an element.
func GetText(sess *Session, pageName, selector string) (string, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return "", fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return "", fmt.Errorf("element not found '%s': %w", selector, err)
	}

	return el.Text()
}

// GetHTML returns the HTML content of an element or the full page.
func GetHTML(sess *Session, pageName, selector string) (string, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return "", fmt.Errorf("page '%s' not found", pageName)
	}

	if selector == "" {
		return page.Timeout(DefaultTimeout).HTML()
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return "", fmt.Errorf("element not found '%s': %w", selector, err)
	}

	return el.HTML()
}

// GetAttribute returns an element attribute value.
func GetAttribute(sess *Session, pageName, selector, attr string) (string, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return "", fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := findElement(page.Timeout(DefaultTimeout), selector)
	if err != nil {
		return "", fmt.Errorf("element not found '%s': %w", selector, err)
	}

	val, err := el.Attribute(attr)
	if err != nil {
		return "", err
	}
	if val == nil {
		return "", nil
	}
	return *val, nil
}

// GetURL returns the current page URL.
func GetURL(sess *Session, pageName string) (string, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return "", fmt.Errorf("page '%s' not found", pageName)
	}

	info, err := page.Timeout(DefaultTimeout).Info()
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

// GetTitle returns the page title.
func GetTitle(sess *Session, pageName string) (string, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return "", fmt.Errorf("page '%s' not found", pageName)
	}

	info, err := page.Timeout(DefaultTimeout).Info()
	if err != nil {
		return "", err
	}
	return info.Title, nil
}

// Eval executes JavaScript on the page using proto.RuntimeEvaluate.
//
// It first runs the code as a console-style expression, so plain expressions
// (`document.title`) and multi-statement snippets whose last statement is an
// expression both return their completion value, exactly like the DevTools
// console. If that fails with a SyntaxError — which happens when the snippet
// uses a top-level `return` (a very common LLM habit) — it transparently
// retries with the body wrapped in an IIFE so `return`, `var`, `forEach`, etc.
// work as written. SyntaxErrors are caught at parse time before any code runs,
// so the retry can never double-execute side effects.
func Eval(sess *Session, pageName, js string) (interface{}, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return nil, fmt.Errorf("page '%s' not found", pageName)
	}
	page = page.Timeout(DefaultTimeout)

	res, err := evalExpr(page, js)
	if err != nil {
		return nil, err
	}

	if isSyntaxError(res.ExceptionDetails) {
		// Retry as a function body so top-level `return`/`var` are legal.
		res, err = evalExpr(page, "(function(){\n"+js+"\n})()")
		if err != nil {
			return nil, err
		}
	}

	if res.ExceptionDetails != nil {
		return nil, fmt.Errorf("javascript error: %s (line %d, column %d)",
			res.ExceptionDetails.Text, res.ExceptionDetails.LineNumber, res.ExceptionDetails.ColumnNumber)
	}

	return res.Result.Value.Val(), nil
}

// evalExpr runs a single RuntimeEvaluate call and returns the raw result.
func evalExpr(page *rod.Page, js string) (*proto.RuntimeEvaluateResult, error) {
	res, err := (&proto.RuntimeEvaluate{
		Expression:    js,
		ReturnByValue: true,
		AwaitPromise:  false,
	}).Call(page)
	if err != nil {
		return nil, fmt.Errorf("javascript execution failed: %w", err)
	}
	return res, nil
}

// isSyntaxError reports whether the exception is a parse-time SyntaxError,
// which is safe to recover from by re-wrapping the code.
func isSyntaxError(det *proto.RuntimeExceptionDetails) bool {
	if det == nil {
		return false
	}
	if det.Exception != nil && det.Exception.ClassName == "SyntaxError" {
		return true
	}
	// Some V8 parse errors surface only in the Text field with no Exception obj.
	return det.Exception == nil && strings.Contains(det.Text, "SyntaxError")
}

// WaitSelector waits for an element to appear.
func WaitSelector(sess *Session, pageName, selector string, timeout time.Duration) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	if timeout == 0 {
		timeout = DefaultTimeout
	}

	_, err := page.Timeout(timeout).Element(selector)
	return err
}

// WaitHidden waits for an element to become hidden.
func WaitHidden(sess *Session, pageName, selector string, timeout time.Duration) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	if timeout == 0 {
		timeout = DefaultTimeout
	}

	_, err := page.Timeout(timeout).ElementR(selector, "")
	if err != nil {
		return nil
	}

	el, _ := page.Timeout(timeout).Element(selector)
	if el == nil {
		return nil
	}

	return el.WaitInvisible()
}

// WaitLoad waits for the page to finish loading.
func WaitLoad(sess *Session, pageName string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	return page.Timeout(NavTimeout).WaitLoad()
}

// WaitIdle waits for network requests to complete.
func WaitIdle(sess *Session, pageName string, timeout time.Duration) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	if timeout == 0 {
		timeout = DefaultTimeout
	}

	wait := page.Timeout(timeout).WaitRequestIdle(300*time.Millisecond, nil, nil, nil)
	wait()
	return nil
}

// Screenshot captures a screenshot.
func Screenshot(sess *Session, pageName, selector, path string) ([]byte, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return nil, fmt.Errorf("page '%s' not found", pageName)
	}

	// Bound every CDP call: a full-page screenshot taken mid-navigation can
	// otherwise block forever and freeze the calling agent loop (task #5985).
	page = page.Timeout(DefaultTimeout)

	var data []byte
	var err error

	if selector != "" {
		el, err := findElement(page.Timeout(DefaultTimeout), selector)
		if err != nil {
			return nil, fmt.Errorf("element not found '%s': %w", selector, err)
		}
		data, err = el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to capture element screenshot: %w", err)
		}
	} else {
		data, err = page.Screenshot(true, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to capture screenshot: %w", err)
		}
	}

	if path != "" {
		if err := writeFile(path, data); err != nil {
			return nil, fmt.Errorf("failed to save file: %w", err)
		}
	}

	return data, nil
}

// PDF generates a PDF of the page.
func PDF(sess *Session, pageName, path string) ([]byte, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return nil, fmt.Errorf("page '%s' not found", pageName)
	}

	req := &proto.PagePrintToPDF{
		Landscape:         false,
		PrintBackground:   true,
		PreferCSSPageSize: true,
	}

	reader, err := page.Timeout(DefaultTimeout).PDF(req)
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read PDF: %w", err)
	}

	if path != "" {
		if err := writeFile(path, data); err != nil {
			return nil, fmt.Errorf("failed to save file: %w", err)
		}
	}

	return data, nil
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
