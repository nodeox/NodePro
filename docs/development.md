# NodePro 2.0 开发指南

## 1. 目录结构

```text
NodePro/
├── api/
│   └── proto/          # gRPC 控制面协议定义 (.proto)
├── cmd/
│   ├── nodepass/       # 核心 Agent 程序入口
│   └── npctl/          # 命令行管理工具入口
├── docs/               # 技术文档
├── internal/
│   ├── agent/          # 核心调度引擎，负责组装组件、管理生命周期、提供 Admin API
│   ├── common/         # 公共结构、缓存池、限流器、流量嗅探 (Sniffer)、混淆器 (Obfs) 等
│   ├── control/        # Control Plane 客户端，处理与云端 gRPC 的通信及热更新
│   ├── inbound/        # 入站协议实现 (HTTP, SOCKS5, TCP/UDP Forward, Transparent Proxy)
│   ├── outbound/       # 出站协议实现 (Direct, NPChain, 以后可扩充 Shadowsocks 等)
│   ├── protocol/       # 底层自定义协议的编解码库 (如 NPChain 头部的序列化)
│   ├── routing/        # 核心路由器、Smart DNS 分流、GeoIP 匹配、Fake IP 池
│   └── transport/      # 传输层 (QUIC, TCP), 证书管理
├── scripts/            # 部署、构建及安装脚本
└── test/
    └── e2e/            # 端到端全链路集成测试
```

## 2. 核心接口规范

如果要为 NodePro 开发新的协议支持，主要涉及实现 `InboundHandler` 或 `OutboundHandler` 接口。

### 2.1 添加新的 Outbound (例如 Shadowsocks)

新出站必须实现 `common.OutboundHandler` 接口：

```go
type OutboundHandler interface {
	// 拨号并建立连接
	Dial(ctx context.Context, meta SessionMeta) (net.Conn, error)
	
	// 健康度检查打分 (0.0 故障 ~ 1.0 极佳)
	HealthCheck(ctx context.Context) float64
	
	Name() string
	Group() string
}
```

**开发步骤：**
1. 在 `internal/outbound/shadowsocks.go` 中实现该接口。
2. 在 `internal/agent/agent.go` 的 `applyConfigToRouter` 函数中增加对应的协议实例化分支 `case "shadowsocks":`。
3. 如果需要在建立连接后进行流量中转，直接返回包装了加解密逻辑的 `net.Conn`，NodePro 的核心 `Relay` 引擎会自动处理数据的零拷贝转发和限流计费。

### 2.2 添加新的 Inbound

新入站必须实现 `common.InboundHandler` 接口：

```go
type InboundHandler interface {
	Start(ctx context.Context, router Router) error
	Stop() error
	Addr() net.Addr
}
```

**开发指南：**
1. 在收到客户端连接后，构建 `common.SessionMeta`（包含来源、目标、网络类型、用户ID等）。
2. 调用 `router.Route(meta)` 获取最佳的 `OutboundHandler`。
3. 调用 `OutboundHandler.Dial(ctx, meta)` 建立与目标的通道。
4. 调用 `common.DualRelay(ctx, clientConn, targetConn, ...)` 启动双向流量转发。

## 3. 开发规范与提示

*   **流量嗅探**：对于无法从协议直接获取域名的流量（如透明代理），请在建立路由前调用 `common.SniffConn(conn)`，将嗅探出的域名注入到 `SessionMeta.Target`，以提高 DNS 和规则匹配的精确度。
*   **内存复用**：在转发数据时，必须使用 `common.GetBuf()` 和 `defer common.PutBuf(buf)` 以避免 GC 压力。
*   **热重载**：Agent 实现了对 `ConfigManager` 的订阅机制，任何模块如需感知配置变更，切勿自行监听文件，应从 `ConfigManager` 获取最新副本或注册监听回调。

## 4. 构建与测试

**编译：**
```bash
make build       # 编译 nodepass 核心
make build-ctl   # 编译 npctl CLI 工具
make build-all   # 交叉编译 (Linux amd64/arm64)
```

**测试：**
在提交代码前，请务必保证核心 E2E 测试全部通过。测试环境包含了 QUIC 隧道建立、DNS 解析、热更新和流量转发的完整流程。
```bash
go test -v ./test/e2e
```
