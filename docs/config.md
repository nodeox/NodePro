# NodePro 2.0 配置指南

NodePro 的 Agent 采用统一的 YAML 格式进行配置，文件默认路径为 `/etc/nodepass/config.yaml`。
通过命令行工具可以快速生成默认配置：
```bash
npctl --cmd init-config --out config.yaml
```

---

## 1. 基础节点配置 (Node & Controller)

```yaml
version: "2.0"

node:
  id: "bj-relay-01"     # 节点唯一标识
  type: "relay"         # 角色: ingress(入口), relay(中继), egress(出口)
  admin_addr: "127.0.0.1:8081" # 本地管理/监控接口监听地址
  tags:
    region: "cn"

controller:
  enabled: true         # 是否接入云端控制器
  address: "api.nodepass.io:8443"
  insecure: false
  cert_file: "/etc/nodepass/certs/client.crt" # mTLS 客户端证书
  key_file: "/etc/nodepass/certs/client.key"
  ca_file: "/etc/nodepass/certs/ca.crt"
```

## 2. 入站配置 (Inbounds)

定义节点如何接收客户端的数据。支持配置多个监听端口。

```yaml
inbounds:
  - protocol: "socks5"
    listen: "0.0.0.0:1080"
    proxy_protocol: false     # 如果部署在 HAProxy 之后，设置为 true
    auth:
      enabled: true
      users:
        - username: "admin"
          password: "$2a$10$..." # bcrypt 哈希密码

  - protocol: "http"
    listen: "0.0.0.0:8080"

  - protocol: "tcp-forward"   # 透明端口转发
    listen: "0.0.0.0:3306"
    settings:
      target: "192.168.1.100:3306"

  - protocol: "tproxy"        # Linux 透明代理
    listen: "0.0.0.0:12345"

  - protocol: "npchain"       # 隧道服务端（用于接收上一级节点的流量）
    listen: "0.0.0.0:443"
    obfuscation:              # 必须与上一级的出口配置一致
      type: "padding"
      max_pad: 64
```

## 3. 出站配置 (Outbounds)

定义节点如何将数据发送到下一个目的地。

```yaml
outbounds:
  - name: "direct-out"
    protocol: "direct"
    group: "default"

  - name: "us-node-1"
    protocol: "npchain"       # 采用私有协议发往国外节点
    group: "proxy-group"
    address: "node.us.example.com:443"
    transport: "quic"
    multipath: true           # 开启 QUIC 底层连接池并发复用，提升弱网吞吐
    obfuscation:
      type: "padding"         # 开启长度填充混淆
      max_pad: 128
      interval_ms: 1000       # 每秒发送空心跳包，抹除时序特征
      dummy_size: 256
```

## 4. 路由分流与 DNS (Routing & Smart DNS)

通过智能路由实现国内外分流、去广告等功能。

```yaml
routing:
  # Fake IP 功能配置 (极速解析，彻底消除首包延迟)
  fake_ip:
    enabled: true
    range: "198.18.0.0/16"
  
  # DNS 上游配置 (可配置 DoH, DoT, UDP)
  dns_upstreams:
    - "https://dns.alidns.com/dns-query" # 国内优先
    - "tls://223.5.5.5:853"
    - "119.29.29.29:53"

  rules:
    - type: "ip"
      pattern: "127.0.0.0/8"
      outbound: "direct-out"

    - type: "domain"
      pattern: "*.google.com"
      outbound_group: "proxy-group"  # 走国外代理池
      strategy: "lowest-latency"     # 自动选择池内延迟最低的节点

    - type: "geoip"
      pattern: "CN"                  # 大陆 IP 库直连
      outbound: "direct-out"

    - type: "default"                # 兜底规则
      outbound_group: "proxy-group"
```

## 5. QoS 与监控 (Limits & Observability)

```yaml
limits:
  max_bandwidth_mbps: 1000     # 节点总带宽上限
  per_user_bandwidth_mbps: 50  # 单用户默认带宽限速 (MB/s)

observability:
  metrics:
    enabled: true              # 开启 Prometheus 接口 (附属于 admin_addr/metrics)
  logging:
    level: "info"              # debug, info, warn, error
    format: "json"
    path: "/var/log/nodepass/agent.log" # 留空则输出到 stdout
```
