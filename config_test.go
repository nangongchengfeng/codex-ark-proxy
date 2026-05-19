package main

import "testing"

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("VOLCANO_BASE_URL", "")
	t.Setenv("VOLCANO_API_KEY", "test-key")
	t.Setenv("VOLCANO_MODEL", "")
	t.Setenv("ARK_BASE_URL", "")
	t.Setenv("ARK_API_KEY", "")
	t.Setenv("ARK_MODEL", "")
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
	t.Setenv("PORT", "9090")
	t.Setenv("VOLCANO_BASE_URL", "")
	t.Setenv("VOLCANO_API_KEY", "")
	t.Setenv("VOLCANO_MODEL", "")
	t.Setenv("ARK_BASE_URL", "https://example.com/api")
	t.Setenv("ARK_API_KEY", "legacy-key")
	t.Setenv("ARK_MODEL", "legacy-model")

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
