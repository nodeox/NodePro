package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// CertManager 负责动态加载和管理 TLS 证书
type CertManager struct {
	mu         sync.RWMutex
	cert       atomic.Value // 存储 *tls.Certificate
	caCertPool *x509.CertPool
	certFile   string
	keyFile    string
	caFile     string
	insecure   bool
}

func NewCertManager(certFile, keyFile, caFile string, insecure bool) (*CertManager, error) {
	cm := &CertManager{
		certFile: certFile,
		keyFile:  keyFile,
		caFile:   caFile,
		insecure: insecure,
	}
	if err := cm.Reload(); err != nil {
		return nil, err
	}
	return cm, nil
}

// Reload 从磁盘重新加载证书文件
func (cm *CertManager) Reload() error {
	cert, err := tls.LoadX509KeyPair(cm.certFile, cm.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load keypair: %w", err)
	}

	caCert, err := os.ReadFile(cm.caFile)
	if err != nil {
		return fmt.Errorf("failed to read CA: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)

	cm.mu.Lock()
	cm.cert.Store(&cert)
	cm.caCertPool = pool
	cm.mu.Unlock()

	return nil
}

// GetTLSConfigServer 返回用于服务端的动态 TLS 配置
func (cm *CertManager) GetTLSConfigServer() *tls.Config {
	return &tls.Config{
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return cm.cert.Load().(*tls.Certificate), nil
		},
		ClientCAs:  cm.caCertPool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"nodepass-2.0"},
	}
}

// GetTLSConfigClient 返回用于客户端的动态 TLS 配置
func (cm *CertManager) GetTLSConfigClient(serverName string) *tls.Config {
	return &tls.Config{
		GetClientCertificate: func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return cm.cert.Load().(*tls.Certificate), nil
		},
		RootCAs:            cm.caCertPool,
		ServerName:         serverName,
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: cm.insecure,
		NextProtos:         []string{"nodepass-2.0"},
	}
}
