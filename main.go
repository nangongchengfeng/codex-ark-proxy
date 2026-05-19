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
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	client := &http.Client{
		Timeout: cfg.UpstreamTimeout,
	}

	p := proxy.NewProxy(cfg, client)
	srv := server.NewServer(cfg, p)
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
