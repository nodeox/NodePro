# NodePass 2.0 Codex 提示词 - 第1部分：项目初始化

## 目标
从零开始搭建 NodePass 2.0 项目结构，建立开发基础设施。

---

## 项目结构

```
nodepass/
├── cmd/
│   └── nodepass/
│       └── main.go              # 主入口
├── internal/
│   ├── agent/                   # Agent 核心引擎
│   │   ├── agent.go
│   │   ├── lifecycle.go
│   │   └── config.go
│   ├── transport/               # 传输层
│   │   ├── quic.go
│   │   ├── tcp.go
│   │   └── pool.go
│   ├── mux/                     # 多路复用层
│   │   ├── aggregator.go
│   │   ├── reassembler.go
│   │   └── flowcontrol.go
│   ├── routing/                 # 路由层
│   │   ├── router.go
│   │   ├── rules.go
│   │   └── nodegroup.go
│   ├── protocol/                # 协议层
│   │   ├── socks5/
│   │   ├── http/
│   │   ├── npchain/
│   │   └── shadowsocks/
│   ├── inbound/                 # 入站处理器
│   │   ├── handler.go
│   │   ├── socks5.go
│   │   └── http.go
│   ├── outbound/                # 出站处理器
│   │   ├── handler.go
│   │   ├── direct.go
│   │   └── npchain.go
│   ├── control/                 # 控制平面
│   │   ├── server.go
│   │   ├── client.go
│   │   └── api/
│   ├── observability/           # 可观测性
│   │   ├── tracing.go
│   │   ├── metrics.go
│   │   └── logging.go
│   └── common/                  # 公共组件
│       ├── types.go
│       ├── errors.go
│       └── utils.go
├── pkg/                         # 可导出的包
│   └── npchain/                 # NP-Chain 协议库
├── api/                         # API 定义
│   └── proto/
│       └── control.proto
├── configs/                     # 配置示例
│   ├── ingress.yaml
│   ├── egress.yaml
│   └── relay.yaml
├── scripts/                     # 脚本
│   ├── build.sh
│   ├── test.sh
│   └── install.sh
├── test/                        # 集成测试
│   ├── e2e/
│   └── benchmark/
├── docs/                        # 文档
├── go.mod
├── go.sum
├── Makefile
├── .goreleaser.yml
└── README.md
```

---

## 初始化步骤

### 1. 创建项目

```bash
mkdir -p nodepass
cd nodepass
go mod init github.com/NodePassProject/nodepass

# 创建目录结构
mkdir -p cmd/nodepass internal/{agent,transport,mux,routing,protocol,inbound,outbound,control,observability,common} pkg/npchain api/proto configs scripts test/{e2e,benchmark} docs
```

### 2. 安装依赖

```bash
# 核心依赖
go get github.com/quic-go/quic-go
go get github.com/hashicorp/yamux
go get go.uber.org/zap
go get github.com/prometheus/client_golang/prometheus
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/trace
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
go get github.com/google/uuid
go get gopkg.in/yaml.v3

# gRPC 相关
go get google.golang.org/grpc
go get google.golang.org/protobuf

# 测试依赖
go get github.com/stretchr/testify
go get go.uber.org/goleak
go get github.com/Shopify/toxiproxy/v2

# 工具
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

### 3. 创建 Makefile

```makefile
.PHONY: all build test clean install proto lint

# 变量
BINARY_NAME=nodepass
VERSION?=$(shell git describe --tags --always --dirty)
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

all: lint test build

# 构建
build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) cmd/nodepass/main.go

# 交叉编译
build-all:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-amd64 cmd/nodepass/main.go
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-arm64 cmd/nodepass/main.go
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-amd64 cmd/nodepass/main.go
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-arm64 cmd/nodepass/main.go
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-windows-amd64.exe cmd/nodepass/main.go

# 测试
test:
	go test -v -race -coverprofile=coverage.out ./...

# 基准测试
bench:
	go test -bench=. -benchmem ./...

# 集成测试
test-e2e:
	go test -v ./test/e2e/...

# 代码检查
lint:
	golangci-lint run

# 生成 protobuf
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/*.proto

# 清理
clean:
	rm -rf bin/ coverage.out

# 安装
install:
	go install $(LDFLAGS) cmd/nodepass/main.go

# 运行
run:
	go run cmd/nodepass/main.go

# 格式化
fmt:
	go fmt ./...
	goimports -w .

# 依赖更新
deps:
	go mod tidy
	go mod verify
```

### 4. 创建 .goreleaser.yml

```yaml
project_name: nodepass

before:
  hooks:
    - go mod tidy
    - go generate ./...

builds:
  - id: nodepass
    main: ./cmd/nodepass
    binary: nodepass
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X main.Version={{.Version}}
      - -X main.BuildTime={{.Date}}

archives:
  - id: nodepass
    format: tar.gz
    format_overrides:
      - goos: windows
        format: zip
    files:
      - README.md
      - LICENSE
      - configs/*

checksum:
  name_template: 'checksums.txt'

snapshot:
  name_template: "{{ incpatch .Version }}-next"

changelog:
  sort: asc
  filters:
    exclude:
      - '^docs:'
      - '^test:'
      - '^chore:'

release:
  github:
    owner: yourusername
    name: nodepass
  draft: false
  prerelease: auto
```

### 5. 创建 .golangci.yml

```yaml
linters:
  enable:
    - gofmt
    - govet
    - errcheck
    - staticcheck
    - unused
    - gosimple
    - structcheck
    - varcheck
    - ineffassign
    - deadcode
    - typecheck
    - gosec
    - gocyclo
    - dupl
    - misspell
    - unparam
    - unconvert
    - goconst
    - goimports

linters-settings:
  gocyclo:
    min-complexity: 15
  dupl:
    threshold: 100
  goconst:
    min-len: 3
    min-occurrences: 3

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - gocyclo
        - errcheck
        - dupl
        - gosec

run:
  timeout: 5m
  tests: true
```

---

## 核心类型定义

### internal/common/types.go

```go
package common

import (
	"context"
	"net"
	"time"
)

// SessionMeta 会话元数据
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

// InboundHandler 入站处理器接口
type InboundHandler interface {
	// Start 启动监听，阻塞直到 ctx 取消
	Start(ctx context.Context, router Router) error
	
	// Stop 停止监听，必须是幂等的
	Stop() error
	
	// Addr 获取监听地址
	Addr() net.Addr
}

// OutboundHandler 出站处理器接口
type OutboundHandler interface {
	// Dial 拨号到目标，必须在 ctx 超时前返回
	Dial(ctx context.Context, meta SessionMeta) (net.Conn, error)
	
	// HealthCheck 健康检查，返回 0-1 的分值
	HealthCheck(ctx context.Context) float64
	
	// Name 节点名称
	Name() string
	
	// Group 所属节点组
	Group() string
}

// Router 路由器接口
type Router interface {
	// Route 根据会话元数据选择出站
	Route(meta SessionMeta) (OutboundHandler, error)
	
	// AddOutbound 添加出站处理器
	AddOutbound(out OutboundHandler)
	
	// RemoveOutbound 移除出站处理器
	RemoveOutbound(name string)
	
	// UpdateRules 更新路由规则
	UpdateRules(rules []RoutingRule)
}

// RoutingRule 路由规则
type RoutingRule struct {
	Type          string   // "domain", "ip", "user", "default"
	Pattern       string   // 匹配模式
	Outbound      string   // 出站名称
	OutboundGroup string   // 出站组名称
	Strategy      string   // "round-robin", "lowest-latency", "hash"
}

// FlowController 流控接口
type FlowController interface {
	// ShouldPause 是否应该暂停读取
	ShouldPause() bool
	
	// OnConsumed 通知已消费 n 字节
	OnConsumed(n int)
	
	// WindowSize 获取当前窗口大小
	WindowSize() int
	
	// Reset 重置窗口
	Reset()
}

// Config 配置结构
type Config struct {
	Version string      `yaml:"version"`
	Node    NodeConfig  `yaml:"node"`
	// ... 其他配置字段
}

// NodeConfig 节点配置
type NodeConfig struct {
	ID   string            `yaml:"id"`
	Type string            `yaml:"type"` // "ingress", "egress", "relay"
	Tags map[string]string `yaml:"tags"`
}
```

### internal/common/errors.go

```go
package common

import "errors"

var (
	// ErrInvalidConfig 无效配置
	ErrInvalidConfig = errors.New("invalid config")
	
	// ErrNoAvailableOutbound 无可用出站
	ErrNoAvailableOutbound = errors.New("no available outbound")
	
	// ErrDialTimeout 拨号超时
	ErrDialTimeout = errors.New("dial timeout")
	
	// ErrAuthFailed 认证失败
	ErrAuthFailed = errors.New("authentication failed")
	
	// ErrRateLimitExceeded 超过速率限制
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	
	// ErrCircuitBreakerOpen 熔断器打开
	ErrCircuitBreakerOpen = errors.New("circuit breaker open")
	
	// ErrInvalidProtocol 无效协议
	ErrInvalidProtocol = errors.New("invalid protocol")
	
	// ErrReplayAttack 重放攻击
	ErrReplayAttack = errors.New("replay attack detected")
)
```

---

## 主入口实现

### cmd/nodepass/main.go

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/yourusername/nodepass/internal/agent"
	"github.com/yourusername/nodepass/internal/observability"
	"go.uber.org/zap"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// 命令行参数
	var (
		configFile = flag.String("config", "config.yaml", "配置文件路径")
		runAs      = flag.String("run-as", "", "运行角色: ingress, egress, relay")
		version    = flag.Bool("version", false, "显示版本信息")
	)
	flag.Parse()

	// 显示版本
	if *version {
		fmt.Printf("NodePass %s (built at %s)\n", Version, BuildTime)
		os.Exit(0)
	}

	// 初始化日志
	logger, err := observability.NewLogger("info", "json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting nodepass",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("role", *runAs),
	)

	// 加载配置
	cfg, err := agent.LoadConfig(*configFile)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	// 如果指定了角色，覆盖配置
	if *runAs != "" {
		cfg.Node.Type = *runAs
	}

	// 初始化可观测性
	shutdown, err := observability.InitTracing(cfg.Observability.Tracing)
	if err != nil {
		logger.Fatal("failed to initialize tracing", zap.Error(err))
	}
	defer shutdown(context.Background())

	// 创建 Agent
	ag, err := agent.New(cfg, logger)
	if err != nil {
		logger.Fatal("failed to create agent", zap.Error(err))
	}

	// 启动 Agent
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ag.Start(ctx); err != nil {
		logger.Fatal("failed to start agent", zap.Error(err))
	}

	logger.Info("agent started successfully")

	// 等待信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	logger.Info("received signal, shutting down", zap.String("signal", sig.String()))

	// 优雅关闭
	if err := ag.Stop(); err != nil {
		logger.Error("failed to stop agent", zap.Error(err))
	}

	logger.Info("agent stopped")
}
```

---

## 下一步

完成项目初始化后，继续阅读：
- **第2部分**: Agent 核心引擎实现
- **第3部分**: 传输层与多路复用
- **第4部分**: 路由层与协议层
- **第5部分**: 控制平面实现
- **第6部分**: 测试与部署
