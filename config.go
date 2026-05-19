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
	ListenAddr         string
	BaseURL            string
	APIKey             string
	Model              string
	UpstreamTimeout    time.Duration
	DebugProxy         bool
	DebugProxyVerbose  bool
	ForceModelOverride bool
}

func LoadConfig() (Config, error) {
	_ = godotenv.Load()

	baseURL := strings.TrimRight(firstNonEmpty(os.Getenv("VOLCANO_BASE_URL"), os.Getenv("ARK_BASE_URL"), defaultBaseURL), "/")
	apiKey := firstNonEmpty(os.Getenv("VOLCANO_API_KEY"), os.Getenv("ARK_API_KEY"))
	model := firstNonEmpty(os.Getenv("VOLCANO_MODEL"), os.Getenv("ARK_MODEL"), defaultModel)

	if providerName := strings.TrimSpace(os.Getenv("PRIMARY_PROVIDER")); providerName != "" {
		selectedBaseURL, selectedAPIKey, selectedModel, err := loadPrimaryProvider(providerName)
		if err != nil {
			return Config{}, err
		}
		baseURL = selectedBaseURL
		apiKey = selectedAPIKey
		model = selectedModel
	}

	cfg := Config{
		ListenAddr:         normalizeListenAddr(firstNonEmpty(os.Getenv("PORT"), defaultListenAddr)),
		BaseURL:            baseURL,
		APIKey:             apiKey,
		Model:              model,
		UpstreamTimeout:    defaultTimeout,
		DebugProxy:         parseBoolEnv(os.Getenv("DEBUG_PROXY")),
		DebugProxyVerbose:  parseBoolEnv(os.Getenv("DEBUG_PROXY_VERBOSE")),
		ForceModelOverride: parseBoolEnv(firstNonEmpty(os.Getenv("FORCE_MODEL_OVERRIDE"), os.Getenv("VOLCANO_FORCE_MODEL_OVERRIDE"), os.Getenv("ARK_FORCE_MODEL_OVERRIDE"))),
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

func loadPrimaryProvider(providerName string) (string, string, string, error) {
	normalizedName := normalizeProviderName(providerName)
	prefix := "PROVIDER_" + normalizedName + "_"

	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv(prefix+"BASE_URL")), "/")
	apiKey := strings.TrimSpace(os.Getenv(prefix + "API_KEY"))
	model := strings.TrimSpace(os.Getenv(prefix + "MODEL"))

	missing := make([]string, 0, 3)
	if baseURL == "" {
		missing = append(missing, "BASE_URL")
	}
	if apiKey == "" {
		missing = append(missing, "API_KEY")
	}
	if model == "" {
		missing = append(missing, "MODEL")
	}
	if len(missing) > 0 {
		return "", "", "", fmt.Errorf("主提供者 %s 配置不完整：缺少 %s", strings.TrimSpace(providerName), strings.Join(missing, ", "))
	}

	return baseURL, apiKey, model, nil
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

func normalizeProviderName(value string) string {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return value
	}

	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}

	return builder.String()
}
