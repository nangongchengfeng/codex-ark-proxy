package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"proxy_doubao/internal/util"

	"github.com/joho/godotenv"
)

const (
	defaultListenAddr  = ":8080"
	defaultBaseURL     = "https://ark.cn-beijing.volces.com/api/plan/v3"
	defaultModel       = "glm-5.1"
	defaultTimeout     = 60 * time.Second
	DefaultContentType = "application/json"
)

// Config 聚合所有代理运行所需的配置项。
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

// LoadConfig 从环境变量和 .env 文件加载配置。
//
// 优先级：PRIMARY_PROVIDER > 旧版 ARK_*/VOLCANO_* 变量 > 默认值。
// 多提供者配置使用 PROVIDER_<NAME>_BASE_URL/API_KEY/MODEL 命名约定。
func LoadConfig() (Config, error) {
	_ = godotenv.Load()

	baseURL := strings.TrimRight(util.FirstNonEmpty(os.Getenv("VOLCANO_BASE_URL"), os.Getenv("ARK_BASE_URL"), defaultBaseURL), "/")
	apiKey := util.FirstNonEmpty(os.Getenv("VOLCANO_API_KEY"), os.Getenv("ARK_API_KEY"))
	model := util.FirstNonEmpty(os.Getenv("VOLCANO_MODEL"), os.Getenv("ARK_MODEL"), defaultModel)

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
		ListenAddr:         normalizeListenAddr(util.FirstNonEmpty(os.Getenv("PORT"), defaultListenAddr)),
		BaseURL:            baseURL,
		APIKey:             apiKey,
		Model:              model,
		UpstreamTimeout:    defaultTimeout,
		DebugProxy:         parseBoolEnv(os.Getenv("DEBUG_PROXY")),
		DebugProxyVerbose:  parseBoolEnv(os.Getenv("DEBUG_PROXY_VERBOSE")),
		ForceModelOverride: parseBoolEnv(util.FirstNonEmpty(os.Getenv("FORCE_MODEL_OVERRIDE"), os.Getenv("VOLCANO_FORCE_MODEL_OVERRIDE"), os.Getenv("ARK_FORCE_MODEL_OVERRIDE"))),
	}

	if cfg.APIKey == "" {
		return Config{}, errors.New("缺少环境变量 VOLCANO_API_KEY 或 ARK_API_KEY")
	}

	timeoutText := util.FirstNonEmpty(os.Getenv("UPSTREAM_TIMEOUT"), os.Getenv("VOLCANO_TIMEOUT"))
	if timeoutText != "" {
		timeout, err := time.ParseDuration(timeoutText)
		if err != nil {
			return Config{}, fmt.Errorf("解析超时时间失败: %w", err)
		}
		cfg.UpstreamTimeout = timeout
	}

	return cfg, nil
}

// loadPrimaryProvider 根据 PRIMARY_PROVIDER 环境变量加载指定提供者的配置。
// 环境变量命名约定：PROVIDER_<NAME>_BASE_URL, PROVIDER_<NAME>_API_KEY, PROVIDER_<NAME>_MODEL
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

// normalizeListenAddr 规范化监听地址，自动补全端口前缀。
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

// parseBoolEnv 将环境变量字符串解析为布尔值。
func parseBoolEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// normalizeProviderName 将提供者名称规范化为大写 + 下划线格式。
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