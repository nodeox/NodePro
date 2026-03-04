# NodePass 2.0 协议规范

**版本**: 2.0.0
**日期**: 2026-03-04

---

## 1. NP-Chain 多跳协议

### 1.1 协议概述

NP-Chain 是 NodePass 2.0 的核心多跳协议，用于在多个中继节点间传递流量。

### 1.2 数据包格式

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Magic (0x4E504332)                     |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|    Version    |    N-Hops     |           Reserved            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                         Session ID (16 bytes)                 +
|                                                               |
+                                                               +
|                                                               |
+                                                               +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                         Hop List (N * 34 bytes)               +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                         Payload (Variable)                    +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### 1.3 字段说明

#### Header (24 bytes)

| 字段 | 长度 | 说明 |
|------|------|------|
| Magic | 4 bytes | 魔数 `0x4E504332` ("NPC2") |
| Version | 1 byte | 协议版本，当前为 `0x01` |
| N-Hops | 1 byte | 跳数，最大 8 |
| Reserved | 2 bytes | 保留字段，必须为 0 |
| Session ID | 16 bytes | 会话唯一标识 (UUID) |

#### Hop Entry (34 bytes per hop)

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|     Type      |              Port             |   Addr Len    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                    Address (30 bytes)                         +
|                                                               |
+                                                               +
|                                                               |
+                                                               +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

| 字段 | 长度 | 说明 |
|------|------|------|
| Type | 1 byte | 地址类型: `0x01` IPv4, `0x02` IPv6, `0x03` Domain |
| Port | 2 bytes | 端口号 (Big Endian) |
| Addr Len | 1 byte | 地址实际长度 |
| Address | 30 bytes | 地址数据 (不足部分填充 0) |

### 1.4 处理流程

#### Ingress 节点

```go
func (i *Ingress) EncodeChain(meta SessionMeta, payload []byte) []byte {
    buf := new(bytes.Buffer)

    // 写入 Header
    binary.Write(buf, binary.BigEndian, uint32(0x4E504332)) // Magic
    buf.WriteByte(0x01)                                      // Version
    buf.WriteByte(uint8(len(meta.HopChain)))                // N-Hops
    binary.Write(buf, binary.BigEndian, uint16(0))          // Reserved
    buf.Write([]byte(meta.ID)[:16])                         // Session ID

    // 写入 Hop List
    for _, hop := range meta.HopChain {
        host, port, _ := net.SplitHostPort(hop)

        // 判断地址类型
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
        buf.Write(make([]byte, 30-len(addrBytes))) // 填充
    }

    // 写入 Payload
    buf.Write(payload)

    return buf.Bytes()
}
```

#### Relay 节点

```go
func (r *Relay) ProcessChain(data []byte) (nextHop string, payload []byte, err error) {
    buf := bytes.NewReader(data)

    // 读取 Header
    var magic uint32
    binary.Read(buf, binary.BigEndian, &magic)
    if magic != 0x4E504332 {
        return "", nil, errors.New("invalid magic")
    }

    version, _ := buf.ReadByte()
    if version != 0x01 {
        return "", nil, errors.New("unsupported version")
    }

    nHops, _ := buf.ReadByte()
    if nHops == 0 {
        return "", nil, errors.New("no more hops")
    }

    buf.Seek(2, io.SeekCurrent) // Skip Reserved

    sessionID := make([]byte, 16)
    buf.Read(sessionID)

    // 读取第一跳（下一跳）
    addrType, _ := buf.ReadByte()
    var port uint16
    binary.Read(buf, binary.BigEndian, &port)
    addrLen, _ := buf.ReadByte()
    addrBytes := make([]byte, addrLen)
    buf.Read(addrBytes)
    buf.Seek(int64(30-addrLen), io.SeekCurrent) // Skip padding

    // 构造下一跳地址
    var host string
    switch addrType {
    case 0x01, 0x02:
        host = net.IP(addrBytes).String()
    case 0x03:
        host = string(addrBytes)
    }
    nextHop = net.JoinHostPort(host, strconv.Itoa(int(port)))

    // 重新封装剩余跳数
    newBuf := new(bytes.Buffer)
    binary.Write(newBuf, binary.BigEndian, magic)
    newBuf.WriteByte(version)
    newBuf.WriteByte(nHops - 1) // 减少跳数
    binary.Write(newBuf, binary.BigEndian, uint16(0))
    newBuf.Write(sessionID)

    // 复制剩余的 Hop List
    remainingHops := make([]byte, int(nHops-1)*34)
    buf.Read(remainingHops)
    newBuf.Write(remainingHops)

    // 复制 Payload
    payloadBytes, _ := io.ReadAll(buf)
    newBuf.Write(payloadBytes)

    return nextHop, newBuf.Bytes(), nil
}
```

#### Egress 节点

```go
func (e *Egress) ExtractTarget(data []byte) (target string, payload []byte, err error) {
    // 与 Relay 类似，但当 N-Hops == 1 时，直接提取目标地址
    // 并返回原始 Payload（不再包含 NP-Chain Header）
}
```

### 1.5 安全考虑

#### 防重放攻击

```go
type ReplayFilter struct {
    seen sync.Map // map[string]time.Time
}

func (rf *ReplayFilter) Check(sessionID string) bool {
    if _, exists := rf.seen.LoadOrStore(sessionID, time.Now()); exists {
        return false // 重放攻击
    }
    return true
}

// 定期清理过期 Session ID
func (rf *ReplayFilter) Cleanup() {
    ticker := time.NewTicker(1 * time.Minute)
    for range ticker.C {
        now := time.Now()
        rf.seen.Range(func(key, value interface{}) bool {
            if now.Sub(value.(time.Time)) > 5*time.Minute {
                rf.seen.Delete(key)
            }
            return true
        })
    }
}
```

#### HMAC 签名（可选）

```go
// 在 Header 后添加 32 字节 HMAC-SHA256
func SignChain(data []byte, secret []byte) []byte {
    h := hmac.New(sha256.New, secret)
    h.Write(data)
    signature := h.Sum(nil)
    return append(data, signature...)
}

func VerifyChain(data []byte, secret []byte) ([]byte, error) {
    if len(data) < 32 {
        return nil, errors.New("data too short")
    }

    payload := data[:len(data)-32]
    signature := data[len(data)-32:]

    h := hmac.New(sha256.New, secret)
    h.Write(payload)
    expected := h.Sum(nil)

    if !hmac.Equal(signature, expected) {
        return nil, errors.New("invalid signature")
    }

    return payload, nil
}
```

---

## 2. 带宽聚合协议

### 2.1 分片格式

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Session ID (16 bytes)                  |
+                                                               +
|                                                               |
+                                                               +
|                                                               |
+                                                               +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Sequence Number                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|           Chunk Size          |     Flags     |   Reserved    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                         Data (Variable)                       +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### 2.2 字段说明

| 字段 | 长度 | 说明 |
|------|------|------|
| Session ID | 16 bytes | 逻辑会话 ID |
| Sequence Number | 4 bytes | 分片序号 (从 0 开始) |
| Chunk Size | 2 bytes | 数据块大小 (最大 64KB) |
| Flags | 1 byte | `0x01` FIN (最后一个分片) |
| Reserved | 1 byte | 保留 |
| Data | Variable | 实际数据 |

### 2.3 发送端实现

```go
type Aggregator struct {
    transports []net.Conn // 多个物理连接
    nextConn   atomic.Int32
}

func (a *Aggregator) Write(sessionID [16]byte, data []byte) error {
    const chunkSize = 64 * 1024
    seq := uint32(0)

    for len(data) > 0 {
        size := min(len(data), chunkSize)
        chunk := data[:size]
        data = data[size:]

        // 构造分片
        buf := new(bytes.Buffer)
        buf.Write(sessionID[:])
        binary.Write(buf, binary.BigEndian, seq)
        binary.Write(buf, binary.BigEndian, uint16(size))

        flags := byte(0)
        if len(data) == 0 {
            flags |= 0x01 // FIN
        }
        buf.WriteByte(flags)
        buf.WriteByte(0) // Reserved
        buf.Write(chunk)

        // 轮询选择物理连接
        connIdx := a.nextConn.Add(1) % int32(len(a.transports))
        conn := a.transports[connIdx]

        if _, err := conn.Write(buf.Bytes()); err != nil {
            return err
        }

        seq++
    }

    return nil
}
```

### 2.4 接收端实现

```go
type Reassembler struct {
    sessions sync.Map // map[sessionID]*SessionBuffer
}

type SessionBuffer struct {
    chunks    map[uint32][]byte
    nextSeq   uint32
    output    chan []byte
    flowCtrl  *WindowFlowController
}

func (r *Reassembler) Process(data []byte) error {
    buf := bytes.NewReader(data)

    // 解析 Header
    sessionID := make([]byte, 16)
    buf.Read(sessionID)

    var seq uint32
    binary.Read(buf, binary.BigEndian, &seq)

    var chunkSize uint16
    binary.Read(buf, binary.BigEndian, &chunkSize)

    flags, _ := buf.ReadByte()
    buf.ReadByte() // Skip Reserved

    chunk := make([]byte, chunkSize)
    buf.Read(chunk)

    // 获取或创建 Session Buffer
    key := string(sessionID)
    val, _ := r.sessions.LoadOrStore(key, &SessionBuffer{
        chunks:   make(map[uint32][]byte),
        output:   make(chan []byte, 100),
        flowCtrl: NewWindowFlowController(16 * 1024 * 1024),
    })
    sb := val.(*SessionBuffer)

    // 存储分片
    sb.chunks[seq] = chunk

    // 尝试重组
    for {
        if data, ok := sb.chunks[sb.nextSeq]; ok {
            // 检查背压
            if sb.flowCtrl.ShouldPause() {
                break // 暂停重组
            }

            sb.output <- data
            sb.flowCtrl.buffered.Add(int64(len(data)))
            delete(sb.chunks, sb.nextSeq)
            sb.nextSeq++

            // 检查 FIN
            if flags&0x01 != 0 {
                close(sb.output)
                r.sessions.Delete(key)
                break
            }
        } else {
            break // 等待缺失的分片
        }
    }

    return nil
}
```

---

## 3. 控制平面协议

### 3.1 gRPC 服务定义

```protobuf
syntax = "proto3";

package nodepass.v2;

service ControlPlane {
  // Agent 注册
  rpc Register(RegisterRequest) returns (RegisterResponse);

  // 心跳
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);

  // 获取配置
  rpc GetConfig(GetConfigRequest) returns (GetConfigResponse);

  // 上报统计
  rpc ReportStats(ReportStatsRequest) returns (ReportStatsResponse);
}

message RegisterRequest {
  string node_id = 1;
  string node_type = 2; // "ingress", "egress", "relay"
  map<string, string> tags = 3;
}

message RegisterResponse {
  string token = 1;
  int64 heartbeat_interval = 2; // 秒
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
  bytes config_data = 2; // JSON 格式的配置
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

### 3.2 Agent 连回机制

```go
func (a *Agent) ConnectToController(controllerAddr string) error {
    conn, err := grpc.Dial(controllerAddr,
        grpc.WithTransportCredentials(credentials.NewTLS(a.tlsConfig)),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                10 * time.Second,
            Timeout:             3 * time.Second,
            PermitWithoutStream: true,
        }),
    )
    if err != nil {
        return err
    }

    client := pb.NewControlPlaneClient(conn)

    // 注册
    resp, err := client.Register(context.Background(), &pb.RegisterRequest{
        NodeId:   a.nodeID,
        NodeType: a.nodeType,
        Tags:     a.tags,
    })
    if err != nil {
        return err
    }

    a.token = resp.Token

    // 启动心跳协程
    go a.heartbeatLoop(client, resp.HeartbeatInterval)

    return nil
}

func (a *Agent) heartbeatLoop(client pb.ControlPlaneClient, interval int64) {
    ticker := time.NewTicker(time.Duration(interval) * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            resp, err := client.Heartbeat(context.Background(), &pb.HeartbeatRequest{
                NodeId: a.nodeID,
                Token:  a.token,
                Status: a.collectStatus(),
            })
            if err != nil {
                log.Error("heartbeat failed", zap.Error(err))
                continue
            }

            // 检查配置更新
            if resp.ConfigUpdated {
                a.fetchAndApplyConfig(client)
            }

        case <-a.ctx.Done():
            return
        }
    }
}
```

---

## 4. 配置文件格式

### 4.1 YAML 配置示例

```yaml
version: "2.0"

node:
  id: "node-001"
  type: "ingress"  # ingress, egress, relay
  tags:
    region: "us-west"
    isp: "aws"

controller:
  enabled: true
  address: "controller.example.com:8443"
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

  - protocol: "http"
    listen: "0.0.0.0:8080"

outbounds:
  - name: "direct"
    protocol: "direct"
    group: "default"

  - name: "relay-1"
    protocol: "np-chain"
    group: "relay"
    address: "relay1.example.com:443"
    transport: "quic"
    tls:
      enabled: true
      server_name: "relay1.example.com"

  - name: "relay-2"
    protocol: "np-chain"
    group: "relay"
    address: "relay2.example.com:443"
    transport: "quic"

routing:
  rules:
    - type: "domain"
      pattern: "*.google.com"
      outbound: "direct"

    - type: "ip"
      pattern: "10.0.0.0/8"
      outbound: "direct"

    - type: "user"
      pattern: "premium-*"
      outbound_group: "relay"
      strategy: "lowest-latency"

    - type: "default"
      outbound_group: "relay"
      strategy: "round-robin"

aggregation:
  enabled: false
  connections: 3
  chunk_size: 65536

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
    output: "/var/log/nodepass/agent.log"

limits:
  max_sessions: 10000
  max_bandwidth_mbps: 1000
  per_user_bandwidth_mbps: 100
```

---

## 5. 状态机

### 5.1 会话状态机

```
     ┌──────┐
     │ INIT │
     └───┬──┘
         │
         ↓
  ┌──────────────┐
  │ HANDSHAKING  │
  └──┬────────┬──┘
     │        │
     ↓        ↓
┌────────┐  ┌────────┐
│ESTABLISHED│ │ FAILED │
└────┬─────┘  └────────┘
     │
     ↓
┌──────────┐
│ DRAINING │
└────┬─────┘
     │
     ↓
┌────────┐
│ CLOSED │
└────────┘
```

### 5.2 状态转换代码

```go
type SessionState int

const (
    StateInit SessionState = iota
    StateHandshaking
    StateEstablished
    StateDraining
    StateClosed
    StateFailed
)

type Session struct {
    state atomic.Int32
    span  trace.Span
}

func (s *Session) TransitionTo(newState SessionState) {
    old := SessionState(s.state.Swap(int32(newState)))

    // 记录状态转换事件
    s.span.AddEvent(fmt.Sprintf("state: %s -> %s", old, newState))

    // 更新指标
    sessionStateTransitions.WithLabelValues(old.String(), newState.String()).Inc()
}
```

---

## 6. 错误码

| 错误码 | 名称 | 说明 |
|--------|------|------|
| 0x00 | SUCCESS | 成功 |
| 0x01 | INVALID_MAGIC | 无效的魔数 |
| 0x02 | UNSUPPORTED_VERSION | 不支持的协议版本 |
| 0x03 | INVALID_HOP_COUNT | 无效的跳数 |
| 0x04 | AUTHENTICATION_FAILED | 认证失败 |
| 0x05 | RATE_LIMIT_EXCEEDED | 超过速率限制 |
| 0x06 | NO_AVAILABLE_OUTBOUND | 无可用出口 |
| 0x07 | DIAL_TIMEOUT | 拨号超时 |
| 0x08 | REPLAY_ATTACK_DETECTED | 检测到重放攻击 |
| 0x09 | SIGNATURE_VERIFICATION_FAILED | 签名验证失败 |
| 0xFF | INTERNAL_ERROR | 内部错误 |

---

## 7. 版本兼容性

### 7.1 协议版本演进

| 版本 | 发布日期 | 主要变更 |
|------|----------|----------|
| 1.0 | 2024-01 | 初始版本，基础点对点隧道 |
| 2.0 | 2026-03 | 多跳协议、带宽聚合、控制平面 |

### 7.2 向后兼容策略

- 2.0 节点可以识别 1.0 协议（通过 Magic 区分）
- 1.0 节点无法处理 2.0 协议（会返回错误）
- 建议在过渡期同时运行 1.0 和 2.0 节点
