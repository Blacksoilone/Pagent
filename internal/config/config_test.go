package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Should_ReturnDefault_WhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("default base_url = %s", cfg.BaseURL)
	}
	if cfg.Model != "deepseek-chat" {
		t.Errorf("default model = %s", cfg.Model)
	}
	if cfg.TestMode {
		t.Error("default test_mode should be false")
	}
}

func TestSaveLoad_Should_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{BaseURL: "http://x/v1", Model: "m1", APIKey: "secret", TestMode: true}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != cfg {
		t.Errorf("round trip mismatch: %+v vs %+v", got, cfg)
	}
}

func TestLoad_Should_ReturnError_OnCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error on corrupt config")
	}
}
