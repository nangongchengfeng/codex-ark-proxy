package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultListenAddr  = ":8080"
	defaultBaseURL     = "https://ark.cn-beijing.volces.com/api/plan/v3"
	defaultModel       = "glm-5.1"
	defaultTimeout     = 60 * time.Second
	defaultContentType = "application/json"
)

type Config struct {
	ListenAddr      string
	BaseURL         string
	APIKey          string
	Model           string
	UpstreamTimeout time.Duration
	DebugProxy      bool
}

func LoadConfig() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		ListenAddr:      normalizeListenAddr(firstNonEmpty(os.Getenv("PORT"), defaultListenAddr)),
		BaseURL:         strings.TrimRight(firstNonEmpty(os.Getenv("VOLCANO_BASE_URL"), os.Getenv("ARK_BASE_URL"), defaultBaseURL), "/"),
		APIKey:          firstNonEmpty(os.Getenv("VOLCANO_API_KEY"), os.Getenv("ARK_API_KEY")),
		Model:           firstNonEmpty(os.Getenv("VOLCANO_MODEL"), os.Getenv("ARK_MODEL"), defaultModel),
		UpstreamTimeout: defaultTimeout,
		DebugProxy:      parseBoolEnv(os.Getenv("DEBUG_PROXY")),
	}

	if cfg.APIKey == "" {
		return Config{}, errors.New("缺少环境变量 VOLCANO_API_KEY 或 ARK_API_KEY")
	}

	timeoutText := firstNonEmpty(os.Getenv("UPSTREAM_TIMEOUT"), os.Getenv("VOLCANO_TIMEOUT"))
	if timeoutText != "" {
		timeout, err := time.ParseDuration(timeoutText)
		if err != nil {
			return Config{}, fmt.Errorf("解析超时时间失败: %w", err)
		}
		cfg.UpstreamTimeout = timeout
	}

	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeListenAddr(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return defaultListenAddr
	case strings.HasPrefix(value, ":"):
		return value
	case strings.Contains(value, ":"):
		return value
	default:
		return ":" + value
	}
}

func parseBoolEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
