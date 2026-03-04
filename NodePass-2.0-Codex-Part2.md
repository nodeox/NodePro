# NodePass 2.0 Codex 提示词 - 第2部分：Agent 核心引擎

## 目标
实现 Agent 的生命周期管理、配置热更新和协程调度。

---

## Agent 核心架构

```
Agent
├── Lifecycle Manager    # 生命周期管理
├── Config Manager       # 配置管理
├── Component Registry   # 组件注册表
└── Health Monitor       # 健康监控
```

---

## 实现步骤

### 1. Agent 结构定义

**internal/agent/agent.go**

```go
package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/yourusername/nodepass/internal/common"
	"github.com/yourusername/nodepass/internal/inbound"
	"github.com/yourusername/nodepass/internal/outbound"
	"github.com/yourusername/nodepass/internal/routing"
	"go.uber.org/zap"
)

// Agent 核心引擎
type Agent struct {
	config  atomic.Value // *common.Config
	logger  *zap.Logger
	router  common.Router
	
	// 组件
	inbounds  map[string]common.InboundHandler
	outbounds map[string]common.OutboundHandler
	
	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	
	// 状态
	state atomic.Int32 // 0: stopped, 1: starting, 2: running, 3: stopping
	
	mu sync.RWMutex
}

// 状态常量
const (
	StateStopped = iota
	StateStarting
	StateRunning
	StateStopping
)

// New 创建新的 Agent
func New(cfg *common.Config, logger *zap.Logger) (*Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	a := &Agent{
		logger:    logger,
		inbounds:  make(map[string]common.InboundHandler),
		outbounds: make(map[string]common.OutboundHandler),
	}
	
	a.config.Store(cfg)
	
	// 创建路由器
	a.router = routing.NewRouter(logger)
	
	return a, nil
}

// Start 启动 Agent
func (a *Agent) Start(ctx context.Context) error {
	// CAS 状态检查
	if !a.state.CompareAndSwap(StateStopped, StateStarting) {
		return fmt.Errorf("agent already started")
	}

	a.ctx, a.cancel = context.WithCancel(ctx)
	
	cfg := a.config.Load().(*common.Config)
	
	a.logger.Info("starting agent",
		zap.String("node_id", cfg.Node.ID),
		zap.String("node_type", cfg.Node.Type),
	)

	// 初始化出站
	if err := a.initOutbounds(cfg); err != nil {
		a.state.Store(StateStopped)
		return fmt.Errorf("failed to init outbounds: %w", err)
	}

	// 初始化入站
	if err := a.initInbounds(cfg); err != nil {
		a.state.Store(StateStopped)
		return fmt.Errorf("failed to init inbounds: %w", err)
	}

	// 启动健康检查
	a.wg.Add(1)
	go a.healthCheckLoop()

	// 启动配置监听
	a.wg.Add(1)
	go a.configWatchLoop()

	a.state.Store(StateRunning)
	a.logger.Info("agent started")

	return nil
}

// Stop 停止 Agent
func (a *Agent) Stop() error {
	// CAS 状态检查
	if !a.state.CompareAndSwap(StateRunning, StateStopping) {
		return fmt.Errorf("agent not running")
	}

	a.logger.Info("stopping agent")

	// 取消上下文
	a.cancel()

	// 停止所有入站（停止接收新连接）
	a.mu.Lock()
	for name, ib := range a.inbounds {
		a.logger.Info("stopping inbound", zap.String("name", name))
		if err := ib.Stop(); err != nil {
			a.logger.Error("failed to stop inbound",
				zap.String("name", name),
				zap.Error(err),
			)
		}
	}
	a.mu.Unlock()

	// 等待所有协程结束
	a.wg.Wait()

	a.state.Store(StateStopped)
	a.logger.Info("agent stopped")

	return nil
}

// initOutbounds 初始化出站处理器
func (a *Agent) initOutbounds(cfg *common.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, outCfg := range cfg.Outbounds {
		out, err := outbound.New(outCfg, a.logger)
		if err != nil {
			return fmt.Errorf("failed to create outbound %s: %w", outCfg.Name, err)
		}

		a.outbounds[outCfg.Name] = out
		a.router.AddOutbound(out)

		a.logger.Info("outbound initialized",
			zap.String("name", outCfg.Name),
			zap.String("protocol", outCfg.Protocol),
		)
	}

	return nil
}

// initInbounds 初始化入站处理器
func (a *Agent) initInbounds(cfg *common.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, inCfg := range cfg.Inbounds {
		ib, err := inbound.New(inCfg, a.logger)
		if err != nil {
			return fmt.Errorf("failed to create inbound %s: %w", inCfg.Protocol, err)
		}

		a.inbounds[inCfg.Listen] = ib

		// 启动入站处理器
		a.wg.Add(1)
		go func(ib common.InboundHandler, listen string) {
			defer a.wg.Done()

			if err := ib.Start(a.ctx, a.router); err != nil {
				a.logger.Error("inbound failed",
					zap.String("listen", listen),
					zap.Error(err),
				)
			}
		}(ib, inCfg.Listen)

		a.logger.Info("inbound initialized",
			zap.String("protocol", inCfg.Protocol),
			zap.String("listen", inCfg.Listen),
		)
	}

	return nil
}

// healthCheckLoop 健康检查循环
func (a *Agent) healthCheckLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.performHealthCheck()

		case <-a.ctx.Done():
			return
		}
	}
}

// performHealthCheck 执行健康检查
func (a *Agent) performHealthCheck() {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for name, out := range a.outbounds {
		score := out.HealthCheck(ctx)
		
		a.logger.Debug("health check",
			zap.String("outbound", name),
			zap.Float64("score", score),
		)

		// 更新指标
		healthScoreGauge.WithLabelValues(name).Set(score)
	}
}

// configWatchLoop 配置监听循环
func (a *Agent) configWatchLoop() {
	defer a.wg.Done()

	// TODO: 实现配置文件监听或从 Controller 拉取配置

	<-a.ctx.Done()
}

// ReloadConfig 重新加载配置
func (a *Agent) ReloadConfig(newCfg *common.Config) error {
	if err := newCfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	a.logger.Info("reloading config")

	// 原子替换配置
	oldCfg := a.config.Swap(newCfg).(*common.Config)

	// TODO: 实现增量更新逻辑
	// 1. 比较新旧配置
	// 2. 停止已删除的组件
	// 3. 启动新增的组件
	// 4. 更新已修改的组件

	_ = oldCfg // 避免未使用警告

	a.logger.Info("config reloaded")

	return nil
}

// GetState 获取当前状态
func (a *Agent) GetState() int32 {
	return a.state.Load()
}
```

---

### 2. 配置管理

**internal/agent/config.go**

```go
package agent

import (
	"fmt"
	"os"

	"github.com/yourusername/nodepass/internal/common"
	"gopkg.in/yaml.v3"
)

// LoadConfig 加载配置文件
func LoadConfig(path string) (*common.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg common.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// Validate 验证配置
func (c *common.Config) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("version is required")
	}

	if c.Node.ID == "" {
		return fmt.Errorf("node.id is required")
	}

	if c.Node.Type == "" {
		return fmt.Errorf("node.type is required")
	}

	// 验证节点类型
	switch c.Node.Type {
	case "ingress", "egress", "relay":
		// OK
	default:
		return fmt.Errorf("invalid node.type: %s", c.Node.Type)
	}

	// 检测 TCP-over-TCP
	if err := c.detectTCPoverTCP(); err != nil {
		return err
	}

	return nil
}

// detectTCPoverTCP 检测 TCP-over-TCP 配置
func (c *common.Config) detectTCPoverTCP() error {
	// 检查入站是否为 TCP
	hasTCPInbound := false
	for _, in := range c.Inbounds {
		if in.Protocol == "socks5" || in.Protocol == "http" {
			hasTCPInbound = true
			break
		}
	}

	if !hasTCPInbound {
		return nil
	}

	// 检查出站是否有 TCP 传输
	for _, out := range c.Outbounds {
		if out.Transport == "tcp" {
			return fmt.Errorf("TCP-over-TCP detected: inbound uses TCP and outbound %s uses TCP transport (use QUIC instead)", out.Name)
		}
	}

	return nil
}
```

---

### 3. 生命周期管理

**internal/agent/lifecycle.go**

```go
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Component 组件接口
type Component interface {
	Start(ctx context.Context) error
	Stop() error
	Name() string
}

// LifecycleManager 生命周期管理器
type LifecycleManager struct {
	components []Component
	mu         sync.RWMutex
}

// NewLifecycleManager 创建生命周期管理器
func NewLifecycleManager() *LifecycleManager {
	return &LifecycleManager{
		components: make([]Component, 0),
	}
}

// Register 注册组件
func (lm *LifecycleManager) Register(comp Component) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lm.components = append(lm.components, comp)
}

// StartAll 启动所有组件
func (lm *LifecycleManager) StartAll(ctx context.Context) error {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	for _, comp := range lm.components {
		if err := comp.Start(ctx); err != nil {
			return fmt.Errorf("failed to start %s: %w", comp.Name(), err)
		}
	}

	return nil
}

// StopAll 停止所有组件
func (lm *LifecycleManager) StopAll() error {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	// 反向停止
	for i := len(lm.components) - 1; i >= 0; i-- {
		comp := lm.components[i]
		if err := comp.Stop(); err != nil {
			return fmt.Errorf("failed to stop %s: %w", comp.Name(), err)
		}
	}

	return nil
}

// GracefulShutdown 优雅关闭
func (lm *LifecycleManager) GracefulShutdown(timeout time.Duration) error {
	done := make(chan error, 1)

	go func() {
		done <- lm.StopAll()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("shutdown timeout after %v", timeout)
	}
}
```

---

### 4. 配置热更新实现

**internal/agent/hotreload.go**

```go
package agent

import (
	"context"
	"fmt"
	"reflect"

	"github.com/yourusername/nodepass/internal/common"
	"go.uber.org/zap"
)

// ConfigDiff 配置差异
type ConfigDiff struct {
	AddedInbounds   []common.InboundConfig
	RemovedInbounds []string
	UpdatedInbounds []common.InboundConfig

	AddedOutbounds   []common.OutboundConfig
	RemovedOutbounds []string
	UpdatedOutbounds []common.OutboundConfig

	RoutingChanged bool
}

// DiffConfig 比较配置差异
func DiffConfig(old, new *common.Config) *ConfigDiff {
	diff := &ConfigDiff{}

	// 比较入站
	oldInMap := make(map[string]common.InboundConfig)
	for _, in := range old.Inbounds {
		oldInMap[in.Listen] = in
	}

	newInMap := make(map[string]common.InboundConfig)
	for _, in := range new.Inbounds {
		newInMap[in.Listen] = in
	}

	// 查找新增和更新的入站
	for listen, newIn := range newInMap {
		if oldIn, exists := oldInMap[listen]; exists {
			if !reflect.DeepEqual(oldIn, newIn) {
				diff.UpdatedInbounds = append(diff.UpdatedInbounds, newIn)
			}
		} else {
			diff.AddedInbounds = append(diff.AddedInbounds, newIn)
		}
	}

	// 查找删除的入站
	for listen := range oldInMap {
		if _, exists := newInMap[listen]; !exists {
			diff.RemovedInbounds = append(diff.RemovedInbounds, listen)
		}
	}

	// 比较出站（类似逻辑）
	// ...

	// 比较路由规则
	if !reflect.DeepEqual(old.Routing, new.Routing) {
		diff.RoutingChanged = true
	}

	return diff
}

// ApplyConfigDiff 应用配置差异
func (a *Agent) ApplyConfigDiff(diff *ConfigDiff) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. 停止已删除的入站
	for _, listen := range diff.RemovedInbounds {
		if ib, exists := a.inbounds[listen]; exists {
			a.logger.Info("stopping removed inbound", zap.String("listen", listen))
			if err := ib.Stop(); err != nil {
				a.logger.Error("failed to stop inbound",
					zap.String("listen", listen),
					zap.Error(err),
				)
			}
			delete(a.inbounds, listen)
		}
	}

	// 2. 启动新增的入站
	for _, inCfg := range diff.AddedInbounds {
		ib, err := inbound.New(inCfg, a.logger)
		if err != nil {
			return fmt.Errorf("failed to create inbound: %w", err)
		}

		a.inbounds[inCfg.Listen] = ib

		a.wg.Add(1)
		go func(ib common.InboundHandler, listen string) {
			defer a.wg.Done()

			if err := ib.Start(a.ctx, a.router); err != nil {
				a.logger.Error("inbound failed",
					zap.String("listen", listen),
					zap.Error(err),
				)
			}
		}(ib, inCfg.Listen)

		a.logger.Info("started new inbound", zap.String("listen", inCfg.Listen))
	}

	// 3. 更新路由规则
	if diff.RoutingChanged {
		cfg := a.config.Load().(*common.Config)
		a.router.UpdateRules(cfg.Routing.Rules)
		a.logger.Info("routing rules updated")
	}

	return nil
}
```

---

### 5. 指标定义

**internal/agent/metrics.go**

```go
package agent

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// 健康分值
	healthScoreGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "np_outbound_health_score",
			Help: "Outbound health score (0-1)",
		},
		[]string{"outbound"},
	)

	// Agent 状态
	agentStateGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "np_agent_state",
			Help: "Agent state (0=stopped, 1=starting, 2=running, 3=stopping)",
		},
	)

	// 配置重载次数
	configReloadCounter = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "np_config_reload_total",
			Help: "Total number of config reloads",
		},
	)

	// 配置重载失败次数
	configReloadErrorCounter = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "np_config_reload_errors_total",
			Help: "Total number of config reload errors",
		},
	)
)
```

---

## 使用示例

### 基本使用

```go
package main

import (
	"context"
	"log"

	"github.com/yourusername/nodepass/internal/agent"
	"go.uber.org/zap"
)

func main() {
	// 加载配置
	cfg, err := agent.LoadConfig("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// 创建日志
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// 创建 Agent
	ag, err := agent.New(cfg, logger)
	if err != nil {
		log.Fatal(err)
	}

	// 启动
	ctx := context.Background()
	if err := ag.Start(ctx); err != nil {
		log.Fatal(err)
	}

	// 等待信号...

	// 停止
	if err := ag.Stop(); err != nil {
		log.Fatal(err)
	}
}
```

### 配置热更新

```go
// 监听配置文件变化
watcher, _ := fsnotify.NewWatcher()
watcher.Add("config.yaml")

for {
	select {
	case event := <-watcher.Events:
		if event.Op&fsnotify.Write == fsnotify.Write {
			// 重新加载配置
			newCfg, err := agent.LoadConfig("config.yaml")
			if err != nil {
				logger.Error("failed to load config", zap.Error(err))
				continue
			}

			// 应用配置
			if err := ag.ReloadConfig(newCfg); err != nil {
				logger.Error("failed to reload config", zap.Error(err))
			}
		}
	}
}
```

---

## 测试

### 单元测试

```go
func TestAgentLifecycle(t *testing.T) {
	cfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "test-node",
			Type: "ingress",
		},
	}

	logger, _ := zap.NewDevelopment()
	ag, err := agent.New(cfg, logger)
	require.NoError(t, err)

	// 测试启动
	ctx := context.Background()
	err = ag.Start(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.StateRunning, ag.GetState())

	// 测试停止
	err = ag.Stop()
	require.NoError(t, err)
	assert.Equal(t, agent.StateStopped, ag.GetState())
}
```

---

## 下一步

继续阅读：
- **第3部分**: 传输层与多路复用实现
