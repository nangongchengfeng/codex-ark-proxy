package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"proxy_doubao/internal/config"
	"proxy_doubao/internal/util"
)

// Proxy 是 Codex /v1/responses → 上游 /chat/completions 的核心代理。
// 负责请求翻译、SSE 流转发、错误透传。
type Proxy struct {
	cfg    config.Config
	client *http.Client
}

// NewProxy 创建代理实例，若未传入 client 则使用默认 HTTP Client。
func NewProxy(cfg config.Config, client *http.Client) *Proxy {
	if client == nil {
		client = &http.Client{}
	}
	return &Proxy{
		cfg:    cfg,
		client: client,
	}
}

// HandleResponses 处理 POST /v1/responses 请求。
//
// 处理流程：
//  1. 校验请求方法和代理状态
//  2. 读取并转换请求体（Codex → OpenAI 格式）
//  3. 构造上游请求，注入 Authorization 和 Content-Type 头
//  4. 发送请求，根据响应类型走流式或非流式回写
func (p *Proxy) HandleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if p == nil || p.client == nil {
		http.Error(w, "proxy not configured", http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body failed", http.StatusBadRequest)
		return
	}
	p.debugLogRequest("incoming", body)
	p.debugLogBody("incoming", body)

	payload, err := transformRequestPayload(body, p.cfg.Model, p.cfg.ForceModelOverride)
	if err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	p.debugLogRequest("transformed", payload)
	p.debugLogBody("transformed", payload)
	requestStreaming := payloadRequestsStreaming(payload)

	targetURL := p.cfg.BaseURL + "/chat/completions"
	reqContext := r.Context()
	var cancel context.CancelFunc
	if !requestStreaming && p.cfg.UpstreamTimeout > 0 {
		reqContext, cancel = context.WithTimeout(reqContext, p.cfg.UpstreamTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqContext, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		http.Error(w, "create upstream request failed", http.StatusInternalServerError)
		return
	}

	util.CopyRequestHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", config.DefaultContentType)
	}

	start := time.Now()
	resp, err := p.clientForRequest(requestStreaming).Do(req)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		log.Printf("upstream request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if p.cfg.DebugProxy {
		log.Printf("[proxy-debug] upstream-status=%d content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	streaming := isEventStream(resp.Header)
	var flusher http.Flusher
	if streaming {
		var ok bool
		flusher, ok = w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
	}

	util.CopyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	if streaming {
		if p.cfg.DebugProxy {
			log.Printf("[proxy-debug] stream-start timeout=%s target=%s", p.cfg.UpstreamTimeout, targetURL)
		}
		if err := streamResponse(w, resp.Body, flusher, p.cfg.DebugProxy, p.cfg.DebugProxyVerbose); err != nil {
			log.Printf("stream upstream response failed: %v", err)
			if p.cfg.DebugProxy {
				log.Printf("[proxy-debug] stream-end status=error err=%v", err)
			}
		} else if p.cfg.DebugProxy {
			log.Printf("[proxy-debug] stream-end status=ok")
		}
	} else {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Printf("copy upstream response failed: %v", readErr)
			return
		}
		p.debugLogResponse(resp.StatusCode, responseBody)
		if _, err := w.Write(responseBody); err != nil {
			log.Printf("copy upstream response failed: %v", err)
		}
	}

	log.Printf("upstream POST %s -> %d (%s)", targetURL, resp.StatusCode, time.Since(start))
}

func (p *Proxy) clientForRequest(streaming bool) *http.Client {
	if p == nil || p.client == nil {
		return http.DefaultClient
	}
	if !streaming || p.client.Timeout == 0 {
		return p.client
	}

	cloned := *p.client
	cloned.Timeout = 0
	return &cloned
}

func (p *Proxy) debugLogRequest(stage string, body []byte) {
	if !p.cfg.DebugProxy {
		return
	}
	log.Printf("[proxy-debug] stage=%s summary=%s", stage, summarizePayload(body))
}

func (p *Proxy) debugLogResponse(status int, body []byte) {
	if !p.cfg.DebugProxy {
		return
	}
	log.Printf("[proxy-debug] upstream-response status=%d body=%s", status, util.TruncateLogBody(string(body), 1200))
}

func (p *Proxy) debugLogBody(stage string, body []byte) {
	if !p.cfg.DebugProxyVerbose {
		return
	}
	log.Printf("[proxy-debug-body] stage=%s body=%s", stage, string(body))
}

func payloadRequestsStreaming(body []byte) bool {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	stream, ok := payload["stream"].(bool)
	return ok && stream
}

func isEventStream(headers http.Header) bool {
	return bytes.Contains([]byte(headers.Get("Content-Type")), []byte("text/event-stream"))
}