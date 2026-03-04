# NodePass 2.0 架构设计文档

**版本**: 2.0.0
**日期**: 2026-03-04
**状态**: Production-Ready Design

---

## 1. 项目概述

### 1.1 项目愿景

将 NodePass 从单一的点对点隧道重构为**生产级分布式边缘网络 SDN Agent**。

### 1.2 核心目标

| 目标 | 描述 | 验收标准 |
|------|------|----------|
| 单一二进制 | 跨平台静态链接，动态角色切换 | 单个可执行文件 < 20MB |
| 极致并发 | 全协程调度，废除进程隔离 | 单节点支持 10K+ 并发会话 |
| 智能拓扑 | 多跳、多入多出、带宽聚合 | 支持 8 跳链路，延迟增量 < 10% |
| 生产就绪 | 全链路追踪、故障熔断、安全审计 | 99.9% 可用性 |

### 1.3 技术栈

```
语言:     Go 1.22+
传输:     QUIC (quic-go), TCP, WebSocket, HTTP/2
复用:     yamux
追踪:     OpenTelemetry
日志:     zap
指标:     Prometheus
测试:     toxiproxy, goleak, pprof
```

---

## 2. 系统架构

### 2.1 分层模型

```
┌──────────────────────────────────────────────────────┐
│                  Control Plane                        │
│  配置下发 | 状态上报 | 用户鉴权 | 拓扑管理              │
└──────────────────────────────────────────────────────┘
                         ↕
┌──────────────────────────────────────────────────────┐
│                  Protocol Layer                       │
│  SOCKS5 | HTTP | Shadowsocks | Trojan | VLESS        │
└──────────────────────────────────────────────────────┘
                         ↕
┌──────────────────────────────────────────────────────┐
│                  Routing Layer                        │
│  标签路由 | 延迟路由 | 用户路由 | 节点组管理            │
└──────────────────────────────────────────────────────┘
                         ↕
┌──────────────────────────────────────────────────────┐
│                  Mux/Agg Layer                        │
│  多路复用 | 分片重组 | 背压流控 | 缓冲池管理            │
└──────────────────────────────────────────────────────┘
                         ↕
┌──────────────────────────────────────────────────────┐
│                  Transport Layer                      │
│  QUIC Pool | TCP Pool | WS Pool | H2 Pool            │
└──────────────────────────────────────────────────────┘
```

### 2.2 角色定义

#### Ingress (入口节点)
- **职责**: 接收外部流量，协议解析，注入会话元数据
- **支持协议**: SOCKS5, HTTP Proxy, Transparent Proxy
- **关键指标**: `np_ingress_connections_total`, `np_ingress_bytes_received`

#### Egress (出口节点)
- **职责**: 执行最终拨号或封装发送至下一跳
- **支持协议**: Direct, SOCKS5, Shadowsocks, Trojan, VLESS
- **关键指标**: `np_egress_dial_duration_ms`, `np_egress_success_rate`

#### Relay (中继节点)
- **职责**: 解析 NP-Chain 协议，剥离/添加包头，执行中转
- **特性**: 无状态转发，支持热链路切换
- **关键指标**: `np_relay_hops_total`, `np_relay_latency_ms`

#### Controller (控制平面)
- **职责**: 策略下发、用户管理、统计聚合、拓扑视图
- **接口**: gRPC + RESTful API
- **存储**: etcd (分布式) 或 bbolt (单机)

---

## 3. 核心数据结构

```go
// 会话元数据：全链路追踪的核心
type SessionMeta struct {
    ID        string            // 唯一会话 ID (UUID)
    TraceID   string            // OpenTelemetry Trace ID
    UserID    string            // 租户/用户 ID
    Source    net.Addr          // 来源地址
    Target    string            // 最终目标地址 (host:port)
    HopChain  []string          // 多跳链路列表
    RouteTags map[string]string // 路由标签
    CreatedAt time.Time         // 创建时间
}

// 入站处理器接口
type InboundHandler interface {
    Start(ctx context.Context, router Router) error
    Stop() error
    Addr() net.Addr
}

// 出站处理器接口
type OutboundHandler interface {
    Dial(ctx context.Context, meta SessionMeta) (net.Conn, error)
    HealthCheck(ctx context.Context) float64 // 返回 0-1 的健康分值
    Name() string
    Group() string
}

// 路由器接口
type Router interface {
    Route(meta SessionMeta) (OutboundHandler, error)
    AddOutbound(out OutboundHandler)
    RemoveOutbound(name string)
    UpdateRules(rules []RoutingRule)
}

// 流控接口
type FlowController interface {
    ShouldPause() bool      // 是否应该暂停读取
    OnConsumed(n int)       // 通知已消费 n 字节
    WindowSize() int        // 获取当前窗口大小
    Reset()                 // 重置窗口
}
```

---

## 4. 关键技术决策

### 4.1 QUIC-First 策略

**决策**: 节点间通信强制使用 QUIC/UDP 传输

**理由**:
- 避免 TCP-over-TCP 拥塞叠加效应
- 原生支持多路复用，无需额外 mux 层
- 0-RTT 连接建立，降低多跳延迟

**实施**:
```go
// 配置检测：发现 TCP-over-TCP 时告警
func (c *Config) Validate() error {
    for _, hop := range c.HopChain {
        if hop.Transport == "tcp" && c.Ingress.Transport == "tcp" {
            return fmt.Errorf("TCP-over-TCP detected: performance degradation expected")
        }
    }
    return nil
}
```

### 4.2 背压流控机制

**问题**: 带宽聚合时，接收端重组缓冲区可能无限增长

**解决方案**: 实现窗口式背压

```go
type WindowFlowController struct {
    maxWindow int64         // 最大窗口 16MB
    consumed  atomic.Int64  // 已消费字节数
    buffered  atomic.Int64  // 缓冲区字节数
}

func (w *WindowFlowController) ShouldPause() bool {
    return w.buffered.Load() > w.maxWindow
}

func (w *WindowFlowController) OnConsumed(n int) {
    w.consumed.Add(int64(n))
    w.buffered.Add(-int64(n))
}
```

**触发策略**:
- 缓冲区 > 16MB: 暂停所有子连接的读取
- 缓冲区 < 8MB: 恢复读取
- 使用 `conn.SetReadDeadline(time.Now().Add(100ms))` 实现软暂停

### 4.3 健康检查多维度评分

```go
type HealthScore struct {
    Latency     float64 // RTT 延迟 (ms)
    SuccessRate float64 // 成功率 (0-1)
    Load        float64 // 负载 (0-1)
}

func (h *HealthScore) Score() float64 {
    // 延迟权重 30%，成功率权重 50%，负载权重 20%
    latencyScore := math.Max(0, 1.0 - h.Latency/1000.0)
    return latencyScore*0.3 + h.SuccessRate*0.5 + (1.0-h.Load)*0.2
}
```

---

## 5. 并发模型

### 5.1 协程生命周期管理

```go
type Agent struct {
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

func (a *Agent) Start() error {
    a.ctx, a.cancel = context.WithCancel(context.Background())

    // 启动各个组件
    a.wg.Add(1)
    go a.runIngress()

    a.wg.Add(1)
    go a.runHealthCheck()

    return nil
}

func (a *Agent) Stop() error {
    a.cancel()           // 通知所有协程退出
    a.wg.Wait()          // 等待所有协程结束
    return nil
}
```

### 5.2 配置热更新

```go
type ConfigManager struct {
    current atomic.Value // *Config
    mu      sync.RWMutex
}

func (cm *ConfigManager) Update(newCfg *Config) error {
    // 1. 验证新配置
    if err := newCfg.Validate(); err != nil {
        return err
    }

    // 2. 预热新资源
    if err := newCfg.Prepare(); err != nil {
        return err
    }

    // 3. 原子切换
    old := cm.current.Swap(newCfg)

    // 4. 异步清理旧资源
    go func() {
        time.Sleep(30 * time.Second) // 等待旧会话结束
        old.(*Config).Cleanup()
    }()

    return nil
}
```

---

## 6. 安全模型

### 6.1 节点间通信

**强制 mTLS**:
```go
func NewTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
    cert, err := tls.LoadX509KeyPair(certFile, keyFile)
    if err != nil {
        return nil, err
    }

    caCert, err := os.ReadFile(caFile)
    if err != nil {
        return nil, err
    }

    caCertPool := x509.NewCertPool()
    caCertPool.AppendCertsFromPEM(caCert)

    return &tls.Config{
        Certificates: []tls.Certificate{cert},
        ClientCAs:    caCertPool,
        ClientAuth:   tls.RequireAndVerifyClientCert, // 双向认证
        MinVersion:   tls.VersionTLS13,
    }, nil
}
```

### 6.2 配置加密

```go
func EncryptConfig(plaintext []byte, machineID string) ([]byte, error) {
    // 使用机器 ID 派生密钥
    key := argon2.IDKey([]byte(machineID), []byte("nodepass-salt"), 1, 64*1024, 4, 32)

    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    nonce := make([]byte, gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return nil, err
    }

    return gcm.Seal(nonce, nonce, plaintext, nil), nil
}
```

---

## 7. 可观测性

### 7.1 OpenTelemetry 集成

```go
func (i *Ingress) HandleConnection(conn net.Conn) {
    // 创建 Span
    ctx, span := tracer.Start(context.Background(), "ingress.handle")
    defer span.End()

    // 生成会话 ID
    sessionID := uuid.New().String()
    span.SetAttributes(
        attribute.String("session.id", sessionID),
        attribute.String("source.addr", conn.RemoteAddr().String()),
    )

    // 注入 TraceID 到 SessionMeta
    meta := SessionMeta{
        ID:      sessionID,
        TraceID: span.SpanContext().TraceID().String(),
    }

    // 路由并转发
    outbound, err := i.router.Route(meta)
    if err != nil {
        span.RecordError(err)
        return
    }

    // 创建子 Span
    ctx, dialSpan := tracer.Start(ctx, "egress.dial")
    targetConn, err := outbound.Dial(ctx, meta)
    dialSpan.End()

    // 双向转发
    relay(conn, targetConn, span)
}
```

### 7.2 关键指标

```go
var (
    sessionActiveTotal = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "np_session_active_total",
        Help: "当前活跃会话数",
    })

    nodeLatencyMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "np_node_latency_ms",
        Help:    "节点延迟分布",
        Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000},
    }, []string{"node", "group"})

    bufferPoolHitRate = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "np_buffer_pool_hit_rate",
        Help: "缓冲区池命中率",
    })
)
```

---

## 8. 容量规划

### 8.1 资源估算

| 指标 | 单会话 | 10K 会话 |
|------|--------|----------|
| 内存 | 64 KB | 640 MB |
| Goroutine | 2 个 | 20K 个 |
| 文件描述符 | 2 个 | 20K 个 |

### 8.2 系统调优

```bash
# 增加文件描述符限制
ulimit -n 65535

# 设置 GOMAXPROCS
export GOMAXPROCS=8

# 启用 CPU Profile
nodepass agent --cpuprofile=cpu.prof

# 启用内存 Profile
nodepass agent --memprofile=mem.prof
```

---

## 9. 部署架构

### 9.1 单节点模式

```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │
       ↓
┌─────────────┐
│   Ingress   │
│   + Egress  │
└─────────────┘
```

### 9.2 多跳模式

```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │
       ↓
┌─────────────┐
│  Ingress 1  │
└──────┬──────┘
       │ QUIC
       ↓
┌─────────────┐
│   Relay 1   │
└──────┬──────┘
       │ QUIC
       ↓
┌─────────────┐
│   Relay 2   │
└──────┬──────┘
       │ QUIC
       ↓
┌─────────────┐
│   Egress    │
└──────┬──────┘
       │
       ↓
┌─────────────┐
│   Target    │
└─────────────┘
```

### 9.3 带宽聚合模式

```
┌─────────────┐
│   Ingress   │
└──────┬──────┘
       │
       ├─────────┬─────────┬─────────┐
       │ QUIC 1  │ QUIC 2  │ QUIC 3  │
       ↓         ↓         ↓         ↓
┌──────────────────────────────────────┐
│            Egress (Mux)              │
└──────────────────────────────────────┘
```

---

## 10. 下一步

参见:
- [协议规范文档](./NodePass-2.0-Protocol.md)
- [实施路线图](./NodePass-2.0-Roadmap.md)
- [运维手册](./NodePass-2.0-Operations.md)
