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

// Redacted 返回脱敏后的配置副本，用于 API 输出
func (c *Config) Redacted() *Config {
	newCfg := *c
	newInbounds := make([]InboundConfig, len(c.Inbounds))
	copy(newInbounds, c.Inbounds)
	for i := range newInbounds {
		newInbounds[i].Auth.Users = nil // 隐藏用户信息
	}
	newCfg.Inbounds = newInbounds
	return &newCfg
}

type LimitsConfig struct {
	MaxBandwidthMBps        int   `yaml:"max_bandwidth_mbps"`
	PerUserBandwidthMBps    int   `yaml:"per_user_bandwidth_mbps"`
	SessionTrafficThreshold int64 `yaml:"session_traffic_threshold_mb"`
}

type NodeConfig struct {
	ID   string            `yaml:"id"`
	Type string            `yaml:"type"`
	Tags map[string]string `yaml:"tags"`
}

type ControllerConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Address  string `yaml:"address"`
	Insecure bool   `yaml:"insecure"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

type InboundConfig struct {
	Protocol  string                 `yaml:"protocol"`
	Listen    string                 `yaml:"listen"`
	Transport string                 `yaml:"transport"`
	Auth      AuthConfig             `yaml:"auth"`
	Settings  map[string]interface{} `yaml:"settings"`
}

type AuthConfig struct {
	Enabled bool       `yaml:"enabled"`
	Users   []UserAuth `yaml:"users"`
}

type UserAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"` // 生产环境必须存储 Bcrypt 哈希值
}

type OutboundConfig struct {
	Name      string                 `yaml:"name"`
	Protocol  string                 `yaml:"protocol"`
	Group     string                 `yaml:"group"`
	Address   string                 `yaml:"address"`
	Transport string                 `yaml:"transport"`
	Settings  map[string]interface{} `yaml:"settings"`
}

type RoutingConfig struct {
	Rules                 []RoutingRuleConfig `yaml:"rules"`
	DNSUpstreams          []string            `yaml:"dns_upstreams"`
	DNSIsolationThreshold int                 `yaml:"dns_isolation_threshold"`
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
	Path   string `yaml:"path"` // 日志路径可配置
}

// LoadConfig 从文件加载配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// Save 实现原子写入配置
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return err
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.Rename(tmpFile, path)
}

func (c *Config) Validate() error {
	if c.Node.ID == "" {
		return fmt.Errorf("node.id is required")
	}
	return nil
}
