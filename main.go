// main.go —— proxy_doubao 启动入口
// 流程：加载配置 → 创建 HTTP Client → 创建核心代理 → 注册路由 → 启动 HTTP 服务
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"proxy_doubao/internal/config"
	"proxy_doubao/internal/proxy"
	"proxy_doubao/internal/server"
)

func main() {
	// 第一步：从环境变量 / .env 加载多提供者配置
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 第二步：创建带超时的 HTTP 客户端，用于向上游方舟 API 发送请求

	client := &http.Client{
		Timeout: cfg.UpstreamTimeout,
	}

	// 第三步：创建核心代理实例，负责 Codex → 上游格式转换与转发

	p := proxy.NewProxy(cfg, client)
	// 第四步：创建 HTTP Server，注册 /health 和 /v1/responses 路由
	srv := server.NewServer(cfg, p)
	// 第五步：配置 HTTP 服务器参数（ReadHeaderTimeout / IdleTimeout 防范慢速攻击）
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("proxy_doubao 已启动，监听 %s，转发到 %s/chat/completions", cfg.ListenAddr, cfg.BaseURL)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("服务启动失败: %v", err)
		os.Exit(1)
	}
}
