package transport

import (
	"crypto/tls"
	"golang.org/x/crypto/acme/autocert"
	"os"
)

// NewACMECertManager 为指定域名创建自动签发证书管理器
func NewACMECertManager(cacheDir string, domains ...string) *autocert.Manager {
	if cacheDir == "" {
		cacheDir = "configs/certs/acme"
	}
	os.MkdirAll(cacheDir, 0700)

	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domains...),
		Cache:      autocert.DirCache(cacheDir),
	}
}

// GetACMETLSConfig 返回支持 ACME 自动签发的 TLS 配置
func GetACMETLSConfig(manager *autocert.Manager) *tls.Config {
	return manager.TLSConfig()
}
