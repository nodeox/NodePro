package common

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config 核心配置结构
type Config struct {
	Version       string               `yaml:"version"`
	Node          NodeConfig           `yaml:"node"`
	Controller    ControllerConfig     `yaml:"controller"`
	Inbounds      []InboundConfig      `yaml:"inbounds"`
	Outbounds     []OutboundConfig     `yaml:"outbounds"`
	Routing       RoutingConfig        `yaml:"routing"`
	Observability ObservabilityConfig `yaml:"observability"`
	Limits        LimitsConfig        `yaml:"limits"`
}

func (c *Config) Redacted() *Config {
	newCfg := *c
	newInbounds := make([]InboundConfig, len(c.Inbounds))
	copy(newInbounds, c.Inbounds)
	for i := range newInbounds {
		newInbounds[i].Auth.Users = nil 
	}
	newCfg.Inbounds = newInbounds
	return &newCfg
}

type InboundConfig struct {
	Protocol      string                 `yaml:"protocol"`
	Listen        string                 `yaml:"listen"`
	Transport     string                 `yaml:"transport"`
	ProxyProtocol bool                   `yaml:"proxy_protocol"`
	Obfuscation   ObfsConfig             `yaml:"obfuscation"` // 新增：混淆配置
	Auth          AuthConfig             `yaml:"auth"`
	WSPath        string                 `yaml:"ws_path"`
	WSHeader      map[string]string      `yaml:"ws_header"`
	Settings      map[string]interface{} `yaml:"settings"`
}

type OutboundConfig struct {
	Name        string                 `yaml:"name"`
	Protocol    string                 `yaml:"protocol"`
	Group       string                 `yaml:"group"`
	Address     string                 `yaml:"address"`
	Transport   string                 `yaml:"transport"`
	Obfuscation ObfsConfig             `yaml:"obfuscation"`
	MultiPath   bool                   `yaml:"multipath"` // 新增：是否开启 QUIC 多路径 (如果底层库支持)
	TLSSNI      string                 `yaml:"tls_sni"`
	WSPath      string                 `yaml:"ws_path"`
	WSHeader    map[string]string      `yaml:"ws_header"`
	Settings    map[string]interface{} `yaml:"settings"`
}

type ObfsConfig struct {
	Type      string `yaml:"type"`       // "none", "padding"
	MaxPad    int    `yaml:"max_pad"`     // 最大填充长度
	Interval  int    `yaml:"interval_ms"` // 定时混淆/心跳间隔 (毫秒)
	DummySize int    `yaml:"dummy_size"`  // 伪造包大小 (字节)
}

// 其余结构保持不变 (Limits, Node, Controller, Routing, Observability 等)
type LimitsConfig struct {
	MaxBandwidthMBps        int   `yaml:"max_bandwidth_mbps"`
	PerUserBandwidthMBps    int   `yaml:"per_user_bandwidth_mbps"`
	SessionTrafficThreshold int64 `yaml:"session_traffic_threshold_mb"`
}

type NodeConfig struct {
	ID        string            `yaml:"id"`
	Type      string            `yaml:"type"`
	AdminAddr string            `yaml:"admin_addr"` // 新增：管理接口监听地址，如 "127.0.0.1:8081"
	Tags      map[string]string `yaml:"tags"`
}

type ControllerConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Address  string `yaml:"address"`
	Insecure bool   `yaml:"insecure"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

type AuthConfig struct {
	Enabled bool       `yaml:"enabled"`
	Users   []UserAuth `yaml:"users"`
}

type UserAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"` 
}

type RoutingConfig struct {
	Rules                 []RoutingRuleConfig `yaml:"rules"`
	DNSUpstreams          []string            `yaml:"dns_upstreams"`
	DNSIsolationThreshold int                 `yaml:"dns_isolation_threshold"`
	FakeIP                FakeIPConfig        `yaml:"fake_ip"`
}

type FakeIPConfig struct {
	Enabled bool   `yaml:"enabled"`
	Range   string `yaml:"range"` // 默认 "198.18.0.0/16"
}

type RoutingRuleConfig struct {
	Type          string `yaml:"type"`
	Pattern       string `yaml:"pattern"`
	Outbound      string `yaml:"outbound"`
	OutboundGroup string `yaml:"outbound_group"`
	Strategy      string `yaml:"strategy"`
}

type ObservabilityConfig struct {
	Tracing TracingConfig `yaml:"tracing"`
	Metrics MetricsConfig `yaml:"metrics"`
	Logging LoggingConfig `yaml:"logging"`
}

type TracingConfig struct {
	Enabled    bool    `yaml:"enabled"`
	Endpoint   string  `yaml:"endpoint"`
	SampleRate float64 `yaml:"sample_rate"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	Format string `yaml:"format"`
	Path   string `yaml:"path"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil { return nil, err }
	var cfg Config
	yaml.Unmarshal(data, &cfg)
	return &cfg, nil
}

func (c *Config) Save(path string) error {
	data, _ := yaml.Marshal(c)
	tmpFile := path + ".tmp"
	os.WriteFile(tmpFile, data, 0600)
	os.MkdirAll(filepath.Dir(path), 0755)
	return os.Rename(tmpFile, path)
}

func (c *Config) Validate() error {
	if c.Node.ID == "" { return fmt.Errorf("node.id required") }
	return nil
}
