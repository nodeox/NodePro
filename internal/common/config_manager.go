package common

import (
	"sync"
	"sync/atomic"
)

// ConfigManager 负责配置的并发安全管理和热更新
type ConfigManager struct {
	current atomic.Value // 存储 *Config
	mu      sync.Mutex
	subs    []func(*Config) // 订阅配置更新的回调
}

func NewConfigManager(initial *Config) *ConfigManager {
	cm := &ConfigManager{}
	cm.current.Store(initial)
	return cm
}

// Get 获取当前配置
func (cm *ConfigManager) Get() *Config {
	return cm.current.Load().(*Config)
}

// Update 更新配置并触发回调
func (cm *ConfigManager) Update(newCfg *Config) error {
	if err := newCfg.Validate(); err != nil {
		return err
	}

	cm.mu.Lock()
	cm.current.Store(newCfg)
	callbacks := make([]func(*Config), len(cm.subs))
	copy(callbacks, cm.subs)
	cm.mu.Unlock()

	// 异步触发回调，避免阻塞更新流程
	for _, cb := range callbacks {
		go cb(newCfg)
	}

	return nil
}

// Subscribe 订阅配置更新事件
func (cm *ConfigManager) Subscribe(cb func(*Config)) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.subs = append(cm.subs, cb)
}
