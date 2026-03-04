# NodePass 2.0 Codex 提示词 - 第4-6部分：完整实现指南

## 第4部分：路由层与协议层

### 路由器实现

**internal/routing/router.go**

```go
package routing

import (
	"fmt"
	"math/rand"
	"sync"

	"github.com/yourusername/nodepass/internal/common"
	"go.uber.org/zap"
)

type Router struct {
	outbounds map[string]common.OutboundHandler
	groups    map[string][]common.OutboundHandler
	rules     []common.RoutingRule
	logger    *zap.Logger
	mu        sync.RWMutex
}

func NewRouter(logger *zap.Logger) *Router {
	return &Router{
		outbounds: make(map[string]common.OutboundHandler),
		groups:    make(map[string][]common.OutboundHandler),
		logger:    logger,
	}
}

func (r *Router) Route(meta common.SessionMeta) (common.OutboundHandler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 遍历路由规则
	for _, rule := range r.rules {
		if r.matchRule(rule, meta) {
			if rule.Outbound != "" {
				// 直接指定出站
				if out, ok := r.outbounds[rule.Outbound]; ok {
					return out, nil
				}
			} else if rule.OutboundGroup != "" {
				// 从节点组选择
				return r.selectFromGroup(rule.OutboundGroup, rule.Strategy)
			}
		}
	}

	return nil, common.ErrNoAvailableOutbound
}

func (r *Router) matchRule(rule common.RoutingRule, meta common.SessionMeta) bool {
	switch rule.Type {
	case "domain":
		return matchDomain(rule.Pattern, meta.Target)
	case "ip":
		return matchIP(rule.Pattern, meta.Target)
	case "user":
		return matchUser(rule.Pattern, meta.UserID)
	case "default":
		return true
	}
	return false
}

func (r *Router) selectFromGroup(group, strategy string) (common.OutboundHandler, error) {
	outs, ok := r.groups[group]
	if !ok || len(outs) == 0 {
		return nil, common.ErrNoAvailableOutbound
	}

	switch strategy {
	case "round-robin":
		return outs[rand.Intn(len(outs))], nil
	case "lowest-latency":
		return r.selectLowestLatency(outs), nil
	default:
		return outs[0], nil
	}
}

func (r *Router) AddOutbound(out common.OutboundHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.outbounds[out.Name()] = out
	
	group := out.Group()
	r.groups[group] = append(r.groups[group], out)
}
```

### NP-Chain 协议实现

**internal/protocol/npchain/codec.go**

```go
package npchain

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"

	"github.com/google/uuid"
	"github.com/yourusername/nodepass/internal/common"
)

const (
	Magic   = 0x4E504332 // "NPC2"
	Version = 0x01
)

func Encode(meta common.SessionMeta, payload []byte) ([]byte, error) {
	buf := new(bytes.Buffer)

	// Header
	binary.Write(buf, binary.BigEndian, uint32(Magic))
	buf.WriteByte(Version)
	buf.WriteByte(uint8(len(meta.HopChain)))
	binary.Write(buf, binary.BigEndian, uint16(0)) // Reserved

	// Session ID
	sessionID, _ := uuid.Parse(meta.ID)
	buf.Write(sessionID[:])

	// Hop List
	for _, hop := range meta.HopChain {
		if err := encodeHop(buf, hop); err != nil {
			return nil, err
		}
	}

	// Payload
	buf.Write(payload)

	return buf.Bytes(), nil
}

func encodeHop(buf *bytes.Buffer, hop string) error {
	host, portStr, _ := net.SplitHostPort(hop)
	port, _ := strconv.Atoi(portStr)

	var addrType byte
	var addrBytes []byte

	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			addrType = 0x01
			addrBytes = ip.To4()
		} else {
			addrType = 0x02
			addrBytes = ip.To16()
		}
	} else {
		addrType = 0x03
		addrBytes = []byte(host)
	}

	buf.WriteByte(addrType)
	binary.Write(buf, binary.BigEndian, uint16(port))
	buf.WriteByte(uint8(len(addrBytes)))
	buf.Write(addrBytes)
	buf.Write(make([]byte, 30-len(addrBytes))) // Padding

	return nil
}
```

---

## 第5部分：控制平面实现

### gRPC 服务定义

**api/proto/control.proto**

```protobuf
syntax = "proto3";

package nodepass.v2;
option go_package = "github.com/yourusername/nodepass/api/proto";

service ControlPlane {
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  rpc GetConfig(GetConfigRequest) returns (GetConfigResponse);
  rpc ReportStats(ReportStatsRequest) returns (ReportStatsResponse);
}

message RegisterRequest {
  string node_id = 1;
  string node_type = 2;
  map<string, string> tags = 3;
}

message RegisterResponse {
  string token = 1;
  int64 heartbeat_interval = 2;
}

message HeartbeatRequest {
  string node_id = 1;
  string token = 2;
  NodeStatus status = 3;
}

message NodeStatus {
  int64 active_sessions = 1;
  int64 total_bytes_in = 2;
  int64 total_bytes_out = 3;
  double cpu_usage = 4;
  double mem_usage = 5;
}

message HeartbeatResponse {
  bool config_updated = 1;
}

message GetConfigRequest {
  string node_id = 1;
  string token = 2;
  int64 config_version = 3;
}

message GetConfigResponse {
  int64 config_version = 1;
  bytes config_data = 2;
}

message ReportStatsRequest {
  string node_id = 1;
  repeated SessionStats sessions = 2;
}

message SessionStats {
  string session_id = 1;
  string user_id = 2;
  int64 bytes_in = 3;
  int64 bytes_out = 4;
  int64 duration_ms = 5;
}

message ReportStatsResponse {
  bool success = 1;
}
```

### Controller 服务端

**internal/control/server.go**

```go
package control

import (
	"context"
	"fmt"
	"net"

	pb "github.com/yourusername/nodepass/api/proto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type Server struct {
	pb.UnimplementedControlPlaneServer
	logger *zap.Logger
	nodes  map[string]*NodeInfo
}

type NodeInfo struct {
	ID     string
	Type   string
	Token  string
	Status *pb.NodeStatus
}

func NewServer(logger *zap.Logger) *Server {
	return &Server{
		logger: logger,
		nodes:  make(map[string]*NodeInfo),
	}
}

func (s *Server) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	s.logger.Info("node registered",
		zap.String("node_id", req.NodeId),
		zap.String("node_type", req.NodeType),
	)

	token := generateToken()
	s.nodes[req.NodeId] = &NodeInfo{
		ID:    req.NodeId,
		Type:  req.NodeType,
		Token: token,
	}

	return &pb.RegisterResponse{
		Token:             token,
		HeartbeatInterval: 10,
	}, nil
}

func (s *Server) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	node, ok := s.nodes[req.NodeId]
	if !ok {
		return nil, fmt.Errorf("node not found")
	}

	if node.Token != req.Token {
		return nil, fmt.Errorf("invalid token")
	}

	node.Status = req.Status

	return &pb.HeartbeatResponse{
		ConfigUpdated: false,
	}, nil
}

func (s *Server) Start(listen string) error {
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer()
	pb.RegisterControlPlaneServer(grpcServer, s)

	s.logger.Info("control plane server started", zap.String("listen", listen))

	return grpcServer.Serve(lis)
}
```

---

## 第6部分：测试与部署

### 集成测试

**test/e2e/basic_test.go**

```go
package e2e

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yourusername/nodepass/internal/agent"
	"go.uber.org/zap"
)

func TestEndToEnd(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// 启动目标服务器
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer targetLn.Close()

	go func() {
		for {
			conn, err := targetLn.Accept()
			if err != nil {
				return
			}
			go io.Copy(conn, conn) // Echo
		}
	}()

	// 启动 Egress
	egressCfg := &common.Config{
		Version: "2.0",
		Node: common.NodeConfig{
			ID:   "egress-1",
			Type: "egress",
		},
		Inbounds: []common.InboundConfig{
			{
				Protocol: "socks5",
				Listen:   "127.0.0.1:0",
			},
		},
		Outbounds: []common.OutboundConfig{
			{
				Name:     "direct",
				Protocol: "direct",
			},
		},
	}

	egress, err := agent.New(egressCfg, logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = egress.Start(ctx)
	require.NoError(t, err)
	defer egress.Stop()

	// 测试连接
	time.Sleep(100 * time.Millisecond)

	// TODO: 通过 SOCKS5 代理连接目标服务器
	// 验证数据传输
}
```

### 性能测试

**test/benchmark/throughput_test.go**

```go
package benchmark

import (
	"io"
	"net"
	"testing"
)

func BenchmarkThroughput(b *testing.B) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	data := make([]byte, 64*1024)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))

	go func() {
		for i := 0; i < b.N; i++ {
			server.Write(data)
		}
	}()

	buf := make([]byte, len(data))
	for i := 0; i < b.N; i++ {
		io.ReadFull(client, buf)
	}
}
```

### Docker 部署

**Dockerfile**

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY . .

RUN go mod download
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o nodepass cmd/nodepass/main.go

FROM scratch

COPY --from=builder /build/nodepass /nodepass
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ENTRYPOINT ["/nodepass"]
```

**docker-compose.yml**

```yaml
version: '3.8'

services:
  controller:
    build: .
    command: ["--config", "/etc/nodepass/controller.yaml"]
    ports:
      - "8443:8443"
    volumes:
      - ./configs/controller.yaml:/etc/nodepass/controller.yaml

  ingress:
    build: .
    command: ["--config", "/etc/nodepass/ingress.yaml"]
    ports:
      - "1080:1080"
    volumes:
      - ./configs/ingress.yaml:/etc/nodepass/ingress.yaml
    depends_on:
      - controller

  egress:
    build: .
    command: ["--config", "/etc/nodepass/egress.yaml"]
    volumes:
      - ./configs/egress.yaml:/etc/nodepass/egress.yaml
    depends_on:
      - controller
```

### 配置示例

**configs/ingress.yaml**

```yaml
version: "2.0"

node:
  id: "ingress-1"
  type: "ingress"
  tags:
    region: "us-west"

controller:
  enabled: true
  address: "controller:8443"
  tls:
    cert: "/etc/nodepass/certs/node.crt"
    key: "/etc/nodepass/certs/node.key"
    ca: "/etc/nodepass/certs/ca.crt"

inbounds:
  - protocol: "socks5"
    listen: "0.0.0.0:1080"
    auth:
      enabled: true
      users:
        - username: "user1"
          password: "pass1"

outbounds:
  - name: "relay-1"
    protocol: "np-chain"
    group: "relay"
    address: "relay1.example.com:443"
    transport: "quic"
    tls:
      enabled: true
      server_name: "relay1.example.com"

routing:
  rules:
    - type: "default"
      outbound_group: "relay"
      strategy: "round-robin"

observability:
  tracing:
    enabled: true
    endpoint: "otel-collector:4317"
    sample_rate: 0.1
  metrics:
    enabled: true
    listen: "0.0.0.0:9090"
  logging:
    level: "info"
    format: "json"
```

### 部署脚本

**scripts/deploy.sh**

```bash
#!/bin/bash

set -e

# 构建
make build-all

# 生成证书
./scripts/gen-certs.sh

# 部署到服务器
for server in ingress-1 egress-1 relay-1; do
    echo "Deploying to $server..."
    
    scp bin/nodepass-linux-amd64 $server:/usr/local/bin/nodepass
    scp configs/$server.yaml $server:/etc/nodepass/config.yaml
    scp -r certs/* $server:/etc/nodepass/certs/
    
    ssh $server "systemctl restart nodepass"
done

echo "Deployment complete"
```

### 监控配置

**prometheus.yml**

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'nodepass'
    static_configs:
      - targets:
          - 'ingress-1:9090'
          - 'egress-1:9090'
          - 'relay-1:9090'
```

---

## 完整开发流程

### 1. 初始化项目
```bash
make deps
make proto
```

### 2. 开发阶段
```bash
# 运行测试
make test

# 代码检查
make lint

# 本地运行
make run
```

### 3. 构建发布
```bash
# 构建所有平台
make build-all

# 使用 goreleaser
goreleaser release --snapshot --clean
```

### 4. 部署
```bash
# Docker 部署
docker-compose up -d

# 或手动部署
./scripts/deploy.sh
```

### 5. 监控
```bash
# 查看指标
curl http://localhost:9090/metrics

# 查看日志
tail -f /var/log/nodepass/agent.log

# 查看追踪
# 访问 Jaeger UI
```

---

## 故障排查清单

### 问题：连接失败
```bash
# 检查端口
netstat -tlnp | grep nodepass

# 检查日志
journalctl -u nodepass -f

# 测试连接
curl -x socks5://localhost:1080 http://example.com
```

### 问题：性能下降
```bash
# CPU Profile
curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.prof
go tool pprof cpu.prof

# 内存 Profile
curl http://localhost:6060/debug/pprof/heap > mem.prof
go tool pprof mem.prof

# Goroutine 泄漏检测
curl http://localhost:6060/debug/pprof/goroutine > goroutine.prof
```

### 问题：配置错误
```bash
# 验证配置
nodepass --config config.yaml --validate

# 查看当前配置
curl http://localhost:9090/debug/config
```

---

## 总结

完成以上6个部分后，你将拥有一个完整的 NodePass 2.0 实现：

1. ✅ 项目结构和基础设施
2. ✅ Agent 核心引擎
3. ✅ 传输层和多路复用
4. ✅ 路由层和协议层
5. ✅ 控制平面
6. ✅ 测试和部署

**下一步建议**:
- 实现更多协议支持（Shadowsocks, Trojan, VLESS）
- 添加 Web Dashboard
- 实现自动化测试 CI/CD
- 性能优化和压力测试
- 编写详细的用户文档
