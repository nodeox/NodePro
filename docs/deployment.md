# NodePro 2.0 部署指南

本指南将帮助您在 Linux 服务器上快速部署和管理 NodePro 2.0 节点。

## 1. 快速一键安装

在目标 Linux 服务器上（支持 Ubuntu, Debian, CentOS, RHEL，架构支持 amd64/arm64），使用 root 权限执行：

```bash
# 获取源码后，在项目根目录执行
sudo bash scripts/install.sh
```

**安装脚本做了什么？**
1. 自动编译 `nodepass` (核心服务) 和 `npctl` (命令行控制工具)。
2. 将二进制文件安装至 `/usr/local/bin/`。
3. 创建配置目录 `/etc/nodepass/` 和日志目录 `/var/log/nodepass/`。
4. 使用 `npctl` 自动生成一份标准的 `config.yaml` 配置文件。
5. 注册并启动 Systemd 服务。

## 2. 目录结构与关键文件

安装完成后，系统的相关文件分布如下：
*   **主程序**: `/usr/local/bin/nodepass`
*   **CLI工具**: `/usr/local/bin/npctl`
*   **主配置**: `/etc/nodepass/config.yaml`
*   **运行日志**: `/var/log/nodepass/agent.log`
*   **审计/流水日志**: `/etc/nodepass/logs/audit.log` (视配置而定)
*   **证书存放**: `/etc/nodepass/certs/`
*   **服务文件**: `/etc/systemd/system/nodepass.service`

## 3. 服务管理

NodePro 完全托管于 Systemd，支持意外崩溃自动重启。

```bash
# 启动服务
sudo systemctl start nodepass

# 停止服务
sudo systemctl stop nodepass

# 重启服务 (重新加载配置)
sudo systemctl restart nodepass

# 查看运行状态
sudo systemctl status nodepass

# 查看实时系统级日志
sudo journalctl -u nodepass -f
```

## 4. 命令行交互 (npctl)

`npctl` 是与正在运行的 Agent 互动的首选工具。默认通过 `127.0.0.1:8081`（在 `config.yaml` 中配置的 `admin_addr`）进行通信。

**常用命令：**

*   **查看系统状态（CPU/内存/流量/连接数）**：
    ```bash
    npctl --cmd status
    ```
*   **查看当前所有在线连接及源/目标地址**：
    ```bash
    npctl --cmd connections
    ```
*   **实时查看应用日志（尾部 100 行）**：
    ```bash
    npctl --cmd logs
    ```
*   **清空 DNS 和 Fake IP 缓存**：
    ```bash
    npctl --cmd dns-flush
    ```
*   **切断某个用户的全部连接**：
    ```bash
    npctl --cmd kick-user --id "user-001"
    ```
*   **强制重置某用户的流量配额**：
    ```bash
    npctl --cmd reset-quota --id "user-001"
    ```

## 5. 内核网络参数调优 (可选)

为了使 NodePro 达到最佳吞吐，特别是作为高并发入口节点时，建议对系统进行以下优化：

编辑 `/etc/sysctl.conf`：
```ini
# 提高文件描述符上限
fs.file-max = 1048576

# 扩大 TCP 和 UDP 缓冲区
net.core.rmem_max = 67108864
net.core.wmem_max = 67108864
net.core.rmem_default = 65536
net.core.wmem_default = 65536
net.ipv4.tcp_rmem = 4096 87380 33554432
net.ipv4.tcp_wmem = 4096 65536 33554432

# 开启 BBR 拥塞控制算法
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
```

执行应用：
```bash
sudo sysctl -p
```
