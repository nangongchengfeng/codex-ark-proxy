package config

import "testing"

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("PRIMARY_PROVIDER", "")
	t.Setenv("PORT", "")
	t.Setenv("VOLCANO_BASE_URL", "")
	t.Setenv("VOLCANO_API_KEY", "test-key")
	t.Setenv("VOLCANO_MODEL", "")
	t.Setenv("ARK_BASE_URL", "")
	t.Setenv("ARK_API_KEY", "")
	t.Setenv("ARK_MODEL", "")
	t.Setenv("PROVIDER_ARK_V3_BASE_URL", "")
	t.Setenv("PROVIDER_ARK_V3_API_KEY", "")
	t.Setenv("PROVIDER_ARK_V3_MODEL", "")
	t.Setenv("PROVIDER_ARK_PLAN_BASE_URL", "")
	t.Setenv("PROVIDER_ARK_PLAN_API_KEY", "")
	t.Setenv("PROVIDER_ARK_PLAN_MODEL", "")
	t.Setenv("UPSTREAM_TIMEOUT", "")
	t.Setenv("VOLCANO_TIMEOUT", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Fatalf("expected :8080, got %s", cfg.ListenAddr)
	}
	if cfg.BaseURL != defaultBaseURL {
		t.Fatalf("unexpected base url: %s", cfg.BaseURL)
	}
	if cfg.APIKey != "test-key" {
		t.Fatalf("unexpected api key: %s", cfg.APIKey)
	}
	if cfg.Model != defaultModel {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
}

func TestLoadConfigSupportsLegacyArkEnv(t *testing.T) {
	t.Setenv("PRIMARY_PROVIDER", "")
	t.Setenv("PORT", "9090")
	t.Setenv("VOLCANO_BASE_URL", "")
	t.Setenv("VOLCANO_API_KEY", "")
	t.Setenv("VOLCANO_MODEL", "")
	t.Setenv("ARK_BASE_URL", "https://example.com/api")
	t.Setenv("ARK_API_KEY", "legacy-key")
	t.Setenv("ARK_MODEL", "legacy-model")
	t.Setenv("PROVIDER_ARK_V3_BASE_URL", "")
	t.Setenv("PROVIDER_ARK_V3_API_KEY", "")
	t.Setenv("PROVIDER_ARK_V3_MODEL", "")
	t.Setenv("PROVIDER_ARK_PLAN_BASE_URL", "")
	t.Setenv("PROVIDER_ARK_PLAN_API_KEY", "")
	t.Setenv("PROVIDER_ARK_PLAN_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.ListenAddr != ":9090" {
		t.Fatalf("expected :9090, got %s", cfg.ListenAddr)
	}
	if cfg.BaseURL != "https://example.com/api" {
		t.Fatalf("unexpected base url: %s", cfg.BaseURL)
	}
	if cfg.APIKey != "legacy-key" {
		t.Fatalf("unexpected api key: %s", cfg.APIKey)
	}
	if cfg.Model != "legacy-model" {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
}

func TestLoadConfigForceModelOverride(t *testing.T) {
	t.Setenv("PRIMARY_PROVIDER", "")
	t.Setenv("VOLCANO_API_KEY", "test-key")
	t.Setenv("FORCE_MODEL_OVERRIDE", "1")
	t.Setenv("PROVIDER_ARK_V3_BASE_URL", "")
	t.Setenv("PROVIDER_ARK_V3_API_KEY", "")
	t.Setenv("PROVIDER_ARK_V3_MODEL", "")
	t.Setenv("PROVIDER_ARK_PLAN_BASE_URL", "")
	t.Setenv("PROVIDER_ARK_PLAN_API_KEY", "")
	t.Setenv("PROVIDER_ARK_PLAN_MODEL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if !cfg.ForceModelOverride {
		t.Fatalf("expected ForceModelOverride to be true")
	}
}

func TestLoadConfigSelectsPrimaryProvider(t *testing.T) {
	t.Setenv("PRIMARY_PROVIDER", "ark_plan")
	t.Setenv("PORT", "")
	t.Setenv("VOLCANO_BASE_URL", "")
	t.Setenv("VOLCANO_API_KEY", "")
	t.Setenv("VOLCANO_MODEL", "")
	t.Setenv("ARK_BASE_URL", "")
	t.Setenv("ARK_API_KEY", "")
	t.Setenv("ARK_MODEL", "")
	t.Setenv("PROVIDER_ARK_PLAN_BASE_URL", "https://ark.cn-beijing.volces.com/api/plan/v3")
	t.Setenv("PROVIDER_ARK_PLAN_API_KEY", "plan-key")
	t.Setenv("PROVIDER_ARK_PLAN_MODEL", "glm-5.1")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.BaseURL != "https://ark.cn-beijing.volces.com/api/plan/v3" {
		t.Fatalf("unexpected base url: %s", cfg.BaseURL)
	}
	if cfg.APIKey != "plan-key" {
		t.Fatalf("unexpected api key: %s", cfg.APIKey)
	}
	if cfg.Model != "glm-5.1" {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
}

func TestLoadConfigFallsBackWithoutPrimaryProvider(t *testing.T) {
	t.Setenv("PRIMARY_PROVIDER", "")
	t.Setenv("VOLCANO_BASE_URL", "")
	t.Setenv("VOLCANO_API_KEY", "")
	t.Setenv("VOLCANO_MODEL", "")
	t.Setenv("ARK_BASE_URL", "https://example.com/api")
	t.Setenv("ARK_API_KEY", "legacy-key")
	t.Setenv("ARK_MODEL", "legacy-model")
	t.Setenv("PROVIDER_ARK_PLAN_BASE_URL", "https://ark.cn-beijing.volces.com/api/plan/v3")
	t.Setenv("PROVIDER_ARK_PLAN_API_KEY", "plan-key")
	t.Setenv("PROVIDER_ARK_PLAN_MODEL", "glm-5.1")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.BaseURL != "https://example.com/api" {
		t.Fatalf("unexpected base url: %s", cfg.BaseURL)
	}
	if cfg.APIKey != "legacy-key" {
		t.Fatalf("unexpected api key: %s", cfg.APIKey)
	}
	if cfg.Model != "legacy-model" {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
}

func TestLoadConfigRejectsIncompletePrimaryProvider(t *testing.T) {
	t.Setenv("PRIMARY_PROVIDER", "ark_v3")
	t.Setenv("VOLCANO_BASE_URL", "")
	t.Setenv("VOLCANO_API_KEY", "")
	t.Setenv("VOLCANO_MODEL", "")
	t.Setenv("ARK_BASE_URL", "")
	t.Setenv("ARK_API_KEY", "")
	t.Setenv("ARK_MODEL", "")
	t.Setenv("PROVIDER_ARK_V3_BASE_URL", "https://ark.cn-beijing.volces.com/api/v3")
	t.Setenv("PROVIDER_ARK_V3_API_KEY", "")
	t.Setenv("PROVIDER_ARK_V3_MODEL", "doubao-seed-2-0-code-preview-260215")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for incomplete primary provider")
	}
	if err.Error() != "主提供者 ark_v3 配置不完整：缺少 API_KEY" {
		t.Fatalf("unexpected error: %v", err)
	}
}