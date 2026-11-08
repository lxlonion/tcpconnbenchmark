package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"tcpconnbenchmark/internal/i18n"
	"tcpconnbenchmark/internal/tester"
)

const maxDomainConfigBytes = 2 << 20

type domainConfig struct {
	Domains []string `json:"domains"`
}

func normalizeConfigDomains(domains []string) ([]string, error) {
	valid, invalid := tester.NormalizeDomains(domains)
	if len(invalid) != 0 {
		return nil, fmt.Errorf(i18n.T("存在无效域名：%s"), invalid[0])
	}
	if len(valid) == 0 || len(valid) > tester.MaxDomains {
		return nil, fmt.Errorf(i18n.T("有效域名须为 1–%d 个"), tester.MaxDomains)
	}
	return valid, nil
}

// loadDomainConfig returns builtins when the file does not exist; malformed
// or unreadable files are reported and never modified.
func loadDomainConfig(dir string, builtins []string) ([]string, error) {
	fallback := append([]string(nil), builtins...)
	if !filepath.IsAbs(dir) {
		return fallback, errors.New(i18n.T("应用目录不可用，无法读取域名配置"))
	}
	path, err := domainConfigPath(dir)
	if err != nil {
		return fallback, err
	}
	return loadDomainConfigFile(path, builtins, path == filepath.Join(dir, "domains.json"))
}

func loadDomainConfigFile(path string, builtins []string, allowMissing bool) ([]string, error) {
	fallback := append([]string(nil), builtins...)
	f, err := os.Open(path)
	if allowMissing && errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return fallback, fmt.Errorf(i18n.T("读取域名配置失败: %w"), err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDomainConfigBytes+1))
	if err != nil {
		return fallback, fmt.Errorf(i18n.T("读取域名配置失败: %w"), err)
	}
	if len(data) > maxDomainConfigBytes {
		return fallback, errors.New(i18n.T("域名配置超过大小限制"))
	}
	var cfg domainConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fallback, fmt.Errorf(i18n.T("域名配置格式错误: %w"), err)
	}
	valid, err := normalizeConfigDomains(cfg.Domains)
	if err != nil {
		return fallback, fmt.Errorf(i18n.T("域名配置无效: %w"), err)
	}
	return valid, nil
}

func saveDomainConfigFile(dir, path string, domains, builtins []string) ([]string, error) {
	if !filepath.IsAbs(dir) || !filepath.IsAbs(path) {
		return nil, errors.New(i18n.T("配置文件路径须为绝对路径"))
	}
	valid, err := normalizeConfigDomains(domains)
	if err != nil {
		return nil, err
	}
	defaults, err := normalizeConfigDomains(builtins)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("内置域名无效: %w"), err)
	}
	if path == filepath.Join(dir, "domains.json") && slices.Equal(valid, defaults) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(i18n.T("删除自定义域名配置失败: %w"), err)
		}
		return valid, nil
	}
	if old, err := loadDomainConfigFile(path, builtins, false); err == nil && slices.Equal(old, valid) {
		return valid, nil
	}
	data, err := json.MarshalIndent(domainConfig{Domains: valid}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf(i18n.T("编码域名配置失败: %w"), err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(path, data); err != nil {
		return nil, fmt.Errorf(i18n.T("保存域名配置失败: %w"), err)
	}
	return valid, nil
}

// domainConfigPath keeps the default portable layout unless a custom file was selected.
func domainConfigPath(dir string) (string, error) {
	defaultPath := filepath.Join(dir, "domains.json")
	data, err := os.ReadFile(filepath.Join(dir, "domain-config-path.json"))
	if errors.Is(err, os.ErrNotExist) {
		return defaultPath, nil
	}
	if err != nil {
		return defaultPath, err
	}
	var pref struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &pref); err != nil || !filepath.IsAbs(pref.Path) {
		return defaultPath, errors.New(i18n.T("域名配置路径设置无效"))
	}
	return filepath.Clean(pref.Path), nil
}

func saveDomainConfigPath(dir, path string) error {
	data, err := json.Marshal(struct {
		Path string `json:"path"`
	}{path})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "domain-config-path.json"), data)
}

// writeFileAtomic stages a replacement beside its destination so Rename stays
// on the same filesystem. Failed writes leave the destination untouched.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
