# NodePass 2.0 开发文档总览

## 📚 文档结构

本项目包含完整的开发文档，分为以下几个部分：

### 1. 架构设计文档
**文件**: `NodePass-2.0-Architecture.md`

包含内容：
- 项目概述和核心目标
- 系统分层模型
- 核心数据结构
- 关键技术决策
- 并发模型
- 安全模型
- 可观测性设计
- 容量规划
- 部署架构

### 2. 协议规范文档
**文件**: `NodePass-2.0-Protocol.md`

包含内容：
- NP-Chain 多跳协议详细规范
- 带宽聚合协议
- 控制平面 gRPC 协议
- 配置文件格式
- 会话状态机
- 错误码定义
- 版本兼容性策略

### 3. Codex 开发提示词（6部分）
**文件**: 
- `NodePass-2.0-Codex-Part1.md` - 项目初始化
- `NodePass-2.0-Codex-Part2.md` - Agent 核心引擎
- `NodePass-2.0-Codex-Part3.md` - 传输层与多路复用
- `NodePass-2.0-Codex-Part4-6.md` - 路由层、控制平面、测试与部署

包含内容：
- 完整的项目结构
- 详细的代码实现
- 编码约束和最佳实践
- 核心接口定义
- 实现模式
- 测试指南
- 部署流程

---

## 🚀 快速开始

### 第一步：阅读架构设计
```bash
# 理解整体架构
cat NodePass-2.0-Architecture.md
```

### 第二步：学习协议规范
```bash
# 了解协议细节
cat NodePass-2.0-Protocol.md
```

### 第三步：开始编码
```bash
# 按顺序阅读 Codex 提示词
cat NodePass-2.0-Codex-Part1.md  # 项目初始化
cat NodePass-2.0-Codex-Part2.md  # Agent 引擎
cat NodePass-2.0-Codex-Part3.md  # 传输层
cat NodePass-2.0-Codex-Part4-6.md # 完整实现
```

---

## 📋 开发路线图

### 阶段 0：基础设施（1-2周）
- [ ] 项目结构搭建
- [ ] 依赖安装
- [ ] CI/CD 配置
- [ ] 测试框架集成（toxiproxy, goleak）
- [ ] 性能基准测试

### 阶段 1：Agent 核心引擎（2-3周）
- [ ] Agent 生命周期管理
- [ ] 配置管理和热更新
- [ ] 协程调度
- [ ] 基础指标和日志

### 阶段 2：传输层与多路复用（2-3周）
- [ ] QUIC 连接池
- [ ] TCP 连接池
- [ ] 带宽聚合器
- [ ] 重组器
- [ ] 背压流控

### 阶段 3：路由层与协议层（2-3周）
- [ ] 路由器实现
- [ ] NP-Chain 协议编解码
- [ ] SOCKS5 协议
- [ ] HTTP 代理协议
- [ ] 入站/出站处理器

### 阶段 4：控制平面（2-3周）
- [ ] gRPC 服务实现
- [ ] Agent 注册和心跳
- [ ] 配置下发
- [ ] 统计上报
- [ ] Web Dashboard（可选）

### 阶段 5：测试与优化（2-3周）
- [ ] 单元测试（覆盖率 > 80%）
- [ ] 集成测试
- [ ] 性能测试
- [ ] 压力测试
- [ ] 内存泄漏检测
- [ ] 性能优化

### 阶段 6：部署与文档（1-2周）
- [ ] Docker 镜像
- [ ] Kubernetes 部署
- [ ] 监控配置
- [ ] 用户文档
- [ ] API 文档
- [ ] 故障排查手册

**总计**: 12-18周

---

## 🛠️ 技术栈

### 核心依赖
```
Go 1.22+
quic-go          # QUIC 协议
yamux            # 多路复用
zap              # 日志
prometheus       # 指标
opentelemetry    # 追踪
grpc             # RPC
```

### 开发工具
```
golangci-lint    # 代码检查
goreleaser       # 发布管理
protoc           # protobuf 编译
toxiproxy        # 网络模拟
goleak           # goroutine 泄漏检测
pprof            # 性能分析
```

---

## 📊 关键指标

### 性能目标
- 单节点并发会话: **10,000+**
- 单会话吞吐量: **1 Gbps+**
- 多跳延迟增量: **< 10%**
- 内存占用: **< 1GB** (10K 会话)
- CPU 占用: **< 50%** (10K 会话)

### 可靠性目标
- 可用性: **99.9%**
- 故障恢复时间: **< 30s**
- 配置热更新: **0 downtime**

---

## 🔒 安全要求

### 强制要求
- [x] 节点间通信必须使用 mTLS
- [x] 配置文件必须加密存储
- [x] 所有日志必须脱敏
- [x] 定期安全审计

### 推荐实践
- [ ] 使用硬件安全模块（HSM）存储密钥
- [ ] 实施最小权限原则
- [ ] 定期轮换证书
- [ ] 启用审计日志

---

## 📖 使用 AI 编码助手

### 推荐工作流

1. **初始化阶段**
   ```
   提示词: "根据 NodePass-2.0-Codex-Part1.md，帮我初始化项目结构"
   ```

2. **实现核心功能**
   ```
   提示词: "根据 NodePass-2.0-Codex-Part2.md，实现 Agent 生命周期管理"
   ```

3. **编写测试**
   ```
   提示词: "为 Agent.Start() 方法编写单元测试，参考 Codex 中的测试模板"
   ```

4. **代码审查**
   ```
   提示词: "审查这段代码，检查是否符合 NodePass 2.0 的编码约束"
   ```

### 上下文管理

将相关文档作为上下文提供给 AI：
```
# 架构问题
上下文: NodePass-2.0-Architecture.md

# 协议实现
上下文: NodePass-2.0-Protocol.md + NodePass-2.0-Codex-Part3.md

# 完整实现
上下文: 所有 Codex 文档
```

---

## 🐛 故障排查

### 常见问题

#### 1. Goroutine 泄漏
```bash
# 检测
go test -v ./... -run TestMain

# 分析
curl http://localhost:6060/debug/pprof/goroutine > goroutine.prof
go tool pprof goroutine.prof
```

#### 2. 内存泄漏
```bash
# 检测
go test -memprofile=mem.prof

# 分析
go tool pprof mem.prof
```

#### 3. TCP-over-TCP 性能问题
```bash
# 检查配置
nodepass --config config.yaml --validate

# 查看告警
grep "TCP-over-TCP" /var/log/nodepass/agent.log
```

#### 4. 连接失败
```bash
# 检查端口
netstat -tlnp | grep nodepass

# 测试连接
curl -x socks5://localhost:1080 http://example.com

# 查看日志
journalctl -u nodepass -f
```

---

## 📞 获取帮助

### 文档问题
- 检查文档是否有更新版本
- 查看 GitHub Issues

### 技术问题
- 查看故障排查手册
- 启用 debug 日志
- 收集 pprof 数据

### 性能问题
- 运行 benchmark 测试
- 分析 CPU/内存 profile
- 检查网络延迟

---

## 📝 贡献指南

### 代码规范
- 遵循 Codex 中的编码约束
- 通过 golangci-lint 检查
- 单元测试覆盖率 > 80%
- 所有公共 API 必须有文档注释

### 提交流程
1. Fork 项目
2. 创建特性分支
3. 编写代码和测试
4. 提交 Pull Request
5. 等待代码审查

---

## 📄 许可证

[待定]

---

## 🎯 项目状态

当前版本: **2.0.0-dev**
状态: **开发中**

### 已完成
- [x] 架构设计
- [x] 协议规范
- [x] 开发文档

### 进行中
- [ ] 核心实现
- [ ] 测试编写

### 待开始
- [ ] 性能优化
- [ ] 生产部署

---

**最后更新**: 2026-03-04
