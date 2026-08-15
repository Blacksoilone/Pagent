// Package config 管理 Pagent 的配置文件（工作区目录下 config.json）。
//
// 优先级：命令行 flag > 配置文件 > 环境变量 > 默认值。
// 配置文件示例（pagent init 自动生成）：
//
//	{
//	  "base_url": "https://api.deepseek.com/v1",
//	  "model": "deepseek-chat",
//	  "api_key": "",
//	  "test_mode": false
//	}
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 顶层配置。
type Config struct {
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	TestMode bool   `json:"test_mode"`
}

// Default 返回默认配置（不含 api_key）。
func Default() Config {
	return Config{
		BaseURL: "https://api.deepseek.com/v1",
		Model:   "deepseek-chat",
	}
}

// Path 返回工作区下的配置文件路径。
func Path(workDir string) string {
	return filepath.Join(workDir, "config.json")
}

// Load 读取配置文件；不存在时返回默认值（不报错）。
func Load(workDir string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(Path(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Save 写入配置文件（覆盖）。
func Save(workDir string, cfg Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(workDir), raw, 0o644)
}
