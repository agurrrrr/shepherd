package browser

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// Navigate navigates to a URL.
func Navigate(sess *Session, pageName, url string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

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

	return page.NavigateBack()
}

// GoForward navigates forward.
func GoForward(sess *Session, pageName string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	return page.NavigateForward()
}

// Click clicks an element.
func Click(sess *Session, pageName, selector string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	return page.Mouse.Scroll(x, y, 1)
}

// ScrollToElement scrolls to an element.
func ScrollToElement(sess *Session, pageName, selector string) error {
	page := sess.GetPage(pageName)
	if page == nil {
		return fmt.Errorf("page '%s' not found", pageName)
	}

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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
		return page.HTML()
	}

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	info, err := page.Info()
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

	info, err := page.Info()
	if err != nil {
		return "", err
	}
	return info.Title, nil
}

// Eval executes JavaScript on the page.
func Eval(sess *Session, pageName, js string) (interface{}, error) {
	page := sess.GetPage(pageName)
	if page == nil {
		return nil, fmt.Errorf("page '%s' not found", pageName)
	}

	result, err := page.Eval(js)
	if err != nil {
		return nil, err
	}
	return result.Value.Val(), nil
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

	el, _ := page.Element(selector)
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

	return page.WaitLoad()
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

	var data []byte
	var err error

	if selector != "" {
		el, err := page.Timeout(DefaultTimeout).Element(selector)
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

	reader, err := page.PDF(req)
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
