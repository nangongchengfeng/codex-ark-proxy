// Package server 负责 HTTP 路由注册和请求日志中间件。
// 提供两个端点：GET /health（健康检查）和 POST /v1/responses（代理入口）。
package server

import (
	"log"
	"net/http"
	"time"

	"proxy_doubao/internal/config"
	"proxy_doubao/internal/proxy"
)

// Server 封装 HTTP 路由和中间件。
type Server struct {
	handler http.Handler
}

// NewServer 创建 Server 实例，注册 /health 和 /v1/responses 路由，并应用日志中间件。
func NewServer(_ config.Config, p *proxy.Proxy) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if p == nil {
			http.Error(w, "proxy not configured", http.StatusInternalServerError)
			return
		}
		p.HandleResponses(w, r)
	})

	return &Server{
		handler: loggingMiddleware(mux),
	}
}

// Handler 返回应用了日志中间件的 HTTP Handler。
func (s *Server) Handler() http.Handler {
	return s.handler
}

// healthHandler 处理 GET /health 请求，返回 {"status":"ok"} 表示服务正常。
func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", config.DefaultContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// loggingMiddleware 记录每个请求的方法、路径、响应状态码和耗时。
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 包装 ResponseWriter 以捕获 HTTP 状态码
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		// 执行实际处理逻辑

		next.ServeHTTP(recorder, r)

		// 请求完成后打印日志

		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, recorder.status, time.Since(start))
	})
}

// statusRecorder 包装 http.ResponseWriter，捕获 WriteHeader 调用的状态码。
// 同时透传 Flusher 接口以支持 SSE 流式响应。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader 拦截状态码记录
func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Flush 透传 Flush 调用，确保 SSE 流能正常写入客户端。
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
