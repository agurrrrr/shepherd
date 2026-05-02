// Package llmproxy is a tiny loopback HTTP proxy that injects
// chat_template_kwargs.enable_thinking=true into OpenAI /chat/completions
// request bodies before forwarding them to a downstream llama.cpp-compatible
// server. We need this because opencode's @ai-sdk/openai-compatible adapter
// drops non-standard fields like chat_template_kwargs from the outbound body
// (verified empirically against opencode 1.3.17 — both options.extraBody and
// model.providerOptions are stripped). Running the proxy locally lets the
// user point opencode at a thinking-routed provider entry so reasoning is
// activated for selected tasks without forking opencode or bypassing it.
package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Hop-by-hop headers must not be forwarded — see RFC 7230 §6.1.
var hopByHop = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// Server is the running proxy. Start one with Run.
type Server struct {
	httpSrv *http.Server
	target  *url.URL
}

// Run starts the proxy bound to 127.0.0.1:port forwarding to target. Blocks
// until ctx is cancelled. target should be a full URL including scheme and
// path prefix (e.g. "http://127.0.0.1:8083/v1") — the request path on the
// proxy is concatenated onto it.
func Run(ctx context.Context, port int, target string) error {
	if target == "" {
		return errors.New("target URL is empty")
	}
	tu, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid target URL %q: %w", target, err)
	}
	if tu.Scheme == "" || tu.Host == "" {
		return fmt.Errorf("target URL must include scheme and host: %q", target)
	}

	s := &Server{target: tu}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)

	s.httpSrv = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
		// llama.cpp can take >60s to first-token on big prompts with reasoning.
		// We do not enforce read/write timeouts on the proxy itself; the upstream
		// client (opencode → ai-sdk) governs timeouts. Header timeout is short
		// to fail fast on misuse.
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("[llmproxy] listening on %s, forwarding to %s", s.httpSrv.Addr, tu.String())

	errCh := make(chan error, 1)
	go func() {
		err := s.httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	outURL := *s.target
	// Append the incoming path onto the target. The target may already have
	// a /v1 prefix; the incoming request from opencode also starts with /v1.
	// Stripping a duplicate /v1 keeps the URL clean when the user configures
	// opencode's baseURL as the proxy root.
	incoming := r.URL.Path
	if strings.HasPrefix(incoming, outURL.Path) {
		// path already prefix-matches target — no-op
		outURL.Path = incoming
	} else {
		outURL.Path = strings.TrimRight(outURL.Path, "/") + "/" + strings.TrimLeft(incoming, "/")
	}
	outURL.RawQuery = r.URL.RawQuery

	// Read & possibly mutate body. Only POST /chat/completions gets injected;
	// /models, /embeddings, GETs are passed through verbatim.
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}
	if r.Method == http.MethodPost && strings.HasSuffix(incoming, "/chat/completions") {
		if injected, ok := injectThinking(bodyBytes); ok {
			bodyBytes = injected
		}
	}

	out, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		http.Error(w, "proxy: build request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for k, vs := range r.Header {
		if _, drop := hopByHop[http.CanonicalHeaderKey(k)]; drop {
			continue
		}
		out.Header[k] = vs
	}
	out.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
	out.ContentLength = int64(len(bodyBytes))
	out.Host = s.target.Host

	// Long-lived streaming responses (SSE) require an unrestricted client.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(out)
	if err != nil {
		http.Error(w, "proxy: upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		if _, drop := hopByHop[http.CanonicalHeaderKey(k)]; drop {
			continue
		}
		w.Header()[k] = vs
	}
	w.WriteHeader(resp.StatusCode)

	// Chunk-by-chunk copy with explicit flush — required for SSE so opencode
	// receives streamed tokens as they arrive instead of buffered to EOF.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

// injectThinking parses an OpenAI chat completions body and ensures
// chat_template_kwargs.enable_thinking is true. Returns the new body and
// true on success; on parse failure returns the original input and false so
// the caller can pass through unmodified.
func injectThinking(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return body, false
	}
	kw, _ := m["chat_template_kwargs"].(map[string]interface{})
	if kw == nil {
		kw = map[string]interface{}{}
	}
	kw["enable_thinking"] = true
	m["chat_template_kwargs"] = kw
	out, err := json.Marshal(m)
	if err != nil {
		return body, false
	}
	return out, true
}

