# NodePro 2.0 (NodePass)

[![Go Report Card](https://goreportcard.com/badge/github.com/nodeox/NodePro)](https://goreportcard.com/report/github.com/nodeox/NodePro)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

NodePro 2.0 是一个由 Go 语言编写的高性能、高隐蔽性的下一代内网穿透与代理系统。它整合了透明代理、智能分流、流量混淆及基于云端 gRPC 的集中式策略管控，旨在为复杂网络环境下的跨域网络互联提供企业级解决方案。

## 🌟 核心特性

- **多协议接入 (Inbounds)**：原生支持 HTTP, SOCKS5 (TCP/UDP), TCP/UDP 端口映射，以及 Linux 原生透明代理 (TProxy / Redirect)。支持解析 HAProxy `Proxy Protocol v1/v2` 获取真实 IP。
- **专有隧道技术 (NPChain)**：基于 QUIC 协议构建的安全底层。支持**多路径并发池化 (Multipath/Connection Pooling)**，极大提升弱网与高时延链路的吞吐量。
- **极致的隐蔽性 (Obfuscation)**：
  - **SNI/Host 嗅探**：穿透识别真实访问目标。
  - **特征消除**：采用长度随机填充 (Padding) 和空包定时发送 (Timing/Dummy Traffic)，从统计学上阻断 DPI 深度包检测。
- **智能路由与 DNS**：支持基于 GeoIP、域名和 IP CIDR 的精细化分流。内置 Smart DNS，支持 DoH/DoT 并发竞速解析，并独创 Fake IP 引擎，彻底消除 DNS 首包解析延迟。
- **动态控制面 (Control Plane)**：基于 gRPC 的长连接心跳，支持毫秒级的**无缝热更新配置**，云端可动态下发用户宽带限速 (QoS)、流量配额甚至即时阻断连接，Agent 无需重启。
- **高可观测性**：全链路 Prometheus 指标暴露，内置 `npctl` CLI 工具用于本地运维和管理。

## 📚 快速导航

技术细节与使用方法，请参阅 `docs` 目录下的详细文档：

1. **[架构设计文档](docs/architecture.md)**：深入理解系统的三大平面及并发模型。
2. **[协议规范文档](docs/protocol.md)**：NPChain 隧道协议封包与混淆原理。
3. **[配置指南](docs/config.md)**：各模块 YAML 配置项说明与示例。
4. **[部署指南](docs/deployment.md)**：Linux 服务器的一键安装、Systemd 托管与系统调优。
5. **[开发指南](docs/development.md)**：项目目录结构与新协议开发接入规范。

## 🚀 快速开始

**一键安装 (Linux):**
```bash
git clone https://github.com/nodeox/NodePro.git
cd NodePro
sudo bash scripts/install.sh
```

**管理节点:**
```bash
# 检查运行状态
npctl --cmd status

# 实时查看在线会话
npctl --cmd connections
```

## 📄 协议与授权

本项目采用 MIT 协议开源。
