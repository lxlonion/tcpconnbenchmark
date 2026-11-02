package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"tcpconnbenchmark/internal/tester"
)

const maxDomainConfigBytes = 2 << 20

type domainConfig struct {
	Domains []string `json:"domains"`
}

func normalizeConfigDomains(domains []string) ([]string, error) {
	valid, invalid := tester.NormalizeDomains(domains)
	if len(invalid) != 0 {
		return nil, fmt.Errorf("存在无效域名：%s", invalid[0])
	}
	if len(valid) == 0 || len(valid) > 5000 {
		return nil, fmt.Errorf("有效域名须为 1–5000 个")
	}
	return valid, nil
}

// loadDomainConfig returns builtins when the file does not exist; malformed
// or unreadable files are reported and never modified.
func loadDomainConfig(dir string, builtins []string) ([]string, error) {
	fallback := append([]string(nil), builtins...)
	if dir == "" || !filepath.IsAbs(dir) {
		return fallback, fmt.Errorf("应用目录不可用，无法读取域名配置")
	}
	path := filepath.Join(dir, "domains.json")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return fallback, fmt.Errorf("读取域名配置失败: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDomainConfigBytes+1))
	if err != nil {
		return fallback, fmt.Errorf("读取域名配置失败: %w", err)
	}
	if len(data) > maxDomainConfigBytes {
		return fallback, fmt.Errorf("域名配置超过大小限制")
	}
	var cfg domainConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fallback, fmt.Errorf("域名配置格式错误: %w", err)
	}
	valid, err := normalizeConfigDomains(cfg.Domains)
	if err != nil {
		return fallback, fmt.Errorf("域名配置无效: %w", err)
	}
	return valid, nil
}

func saveDomainConfig(dir string, domains, builtins []string) ([]string, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("应用目录不可用，无法保存域名配置")
	}
	valid, err := normalizeConfigDomains(domains)
	if err != nil {
		return nil, err
	}
	defaults, err := normalizeConfigDomains(builtins)
	if err != nil {
		return nil, fmt.Errorf("内置域名无效: %w", err)
	}
	path := filepath.Join(dir, "domains.json")
	if slices.Equal(valid, defaults) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("删除自定义域名配置失败: %w", err)
		}
		return valid, nil
	}
	if old, err := loadDomainConfig(dir, builtins); err == nil && slices.Equal(old, valid) {
		return valid, nil
	}
	data, err := json.MarshalIndent(domainConfig{Domains: valid}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("编码域名配置失败: %w", err)
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(dir, ".domains-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("创建域名配置临时文件失败: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("写入域名配置失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("同步域名配置失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("关闭域名配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("保存域名配置失败: %w", err)
	}
	return valid, nil
}
