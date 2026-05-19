package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	client := &http.Client{
		Timeout: cfg.UpstreamTimeout,
	}

	proxy := NewProxy(cfg, client)
	server := NewServer(cfg, proxy)
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("proxy_doubao 已启动，监听 %s，转发到 %s/chat/completions", cfg.ListenAddr, cfg.BaseURL)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("服务启动失败: %v", err)
		os.Exit(1)
	}
}
