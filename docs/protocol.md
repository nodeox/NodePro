# NodePro 2.0 协议规范

本文档定义了 NodePro 2.0 中涉及的专有协议，包含用于隧道穿透的 **NP-Chain 数据平面协议** 和用于节点管理的 **Control Plane gRPC 协议**。

---

## 1. NP-Chain 隧道协议

NP-Chain (NodePass Chain) 是基于 QUIC Stream 和 Datagram 之上构建的轻量级、低延迟、高隐蔽性的隧道协议，支持多级节点跳跃和特征混淆。

### 1.1 协议头部结构 (24 Bytes)

每个新的 NP-Chain 连接（Stream 或 Datagram 的首个包）都必须携带此 24 字节头部。

| 字段名 | 长度 (Bytes) | 描述 |
| :--- | :---: | :--- |
| **Magic** | 4 | 魔数，固定为 `0x4E504332` ("NPC2") |
| **Version** | 1 | 协议版本，当前为 `0x01` |
| **NHops** | 1 | 剩余跳数 (Hop Count)。每经过一个中继节点减 1 |
| **Flags** | 1 | 标志位集合 (详见 1.2) |
| **PadLen** | 1 | 混淆填充长度。表示头部之后跟随了多少字节的随机干扰数据 |
| **SessionID** | 16 | UUID (二进制格式)，用于全链路追踪和控制端管控 |

### 1.2 标志位定义 (Flags)

| 位 (Bit) | 常量 | 含义 |
| :---: | :--- | :--- |
| `0x00` | `FlagTCP` | 该连接传输的是 TCP 数据流 |
| `0x01` | `FlagUDP` | 该连接传输的是 UDP 数据报 |
| `0x80` | `FlagPad` | 启用包头随机混淆填充 (Padding) |

### 1.3 跳数路由信息 (Hop Entry)

如果 `NHops > 0`，则在 24 字节的 Header 及随机 Padding 之后，紧接着包含 `NHops` 个路由条目，每个条目固定 34 字节。

| 字段名 | 长度 (Bytes) | 描述 |
| :--- | :---: | :--- |
| **Type** | 1 | 目标地址类型: `0x01` (IPv4), `0x02` (IPv6), `0x03` (Domain) |
| **Port** | 2 | 目标端口 (Big Endian) |
| **AddrLen** | 1 | 地址实际长度 |
| **Address** | 30 | 地址数据，右侧用零补齐以固定长度 |

### 1.4 中继节点行为

1. 读取并校验 `Magic` 和 `Version`。
2. 提取 `Flags` 和 `PadLen`。
3. 如果 `Flags` 包含 `0x80` 且 `PadLen > 0`，读取并丢弃接下来的 `PadLen` 字节（即剥离混淆层）。
4. 解析出第一个 `Hop Entry` 作为 `NextHop`。
5. 将 `NHops` 减 1，重新构造 Header，并将剩余的 `Hop Entry` 及用户的 Payload 转发给 `NextHop`。

---

## 2. 流量特征混淆协议 (Obfuscation)

为了对抗 DPI (深度包检测)，NodePro 2.0 引入了基于分块传输的混淆机制。

### 2.1 分块头部结构 (3 Bytes)

如果链路开启了 `padding` 混淆，底层数据流将被分割成多个 Chunk，每个 Chunk 以 3 字节头部开始。

| 字段名 | 长度 (Bytes) | 描述 |
| :--- | :---: | :--- |
| **PayloadLen** | 2 | 实际业务数据的长度 |
| **PadLen** | 1 | 附加在业务数据之后的随机垃圾数据长度 |

**结构图**：
`[PayloadLen: 2] [PadLen: 1] [Actual Payload: $PayloadLen] [Random Junk: $PadLen]`

### 2.2 时序混淆 (Dummy Traffic)

如果 `PayloadLen == 0`，则该 Chunk 被视为**纯心跳/伪造包**。接收端解析出头部后，仅读取并丢弃 `PadLen` 长度的数据，不会将其上传给业务层。
此机制配合定时器，可生成持续的背景噪音流量，抹除真实业务连接的启停时序特征。

---

## 3. Control Plane 控制平面协议

基于 gRPC 构建，允许 `Controller` (服务端) 管理成千上万的 `Agent` (客户端)。

### 3.1 核心 RPC 方法

*   **`Register`**: 节点启动时调用，上报 NodeID、NodeType 及机器 Tags，换取长效的身份鉴权 Token。
*   **`Heartbeat`**: 节点定时（默认 30s）调用。
    *   **上行**：汇报 CPU、内存、活跃会话数、流入/流出总字节数。
    *   **下行**：服务端下发动态指令（如：ConfigUpdated，要求拉取新配置；或 Command = "restart"）。
*   **`GetConfig`**: 获取最新的完整 YAML 配置文件二进制流，用于热更新。

### 3.2 动态策略下发

通过 `PolicyUpdate` 消息流，可以在不重启、不修改全局配置的情况下实时管控指定用户：
*   动态修改特定用户的 `BandwidthMbps` (宽带限速)。
*   动态修改特定用户的 `QuotaMb` (流量配额)。
*   发送 `Revoke` 指令，立即阻断该用户当前所有的活跃长连接。
