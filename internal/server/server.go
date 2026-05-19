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

// NewServer 创建 Server 实例，注册路由并应用日志中间件。
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

// Handler 返回配置好的 HTTP Handler。
func (s *Server) Handler() http.Handler {
	return s.handler
}

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

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(recorder, r)

		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, recorder.status, time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}