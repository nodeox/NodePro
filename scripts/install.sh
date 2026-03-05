#!/bin/bash

# NodePass 2.0 一键安装脚本
# 支持架构: amd64, arm64

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

printf "${BLUE}====================================${NC}\n"
printf "${BLUE}   NodePass 2.0 一键安装脚本        ${NC}\n"
printf "${BLUE}====================================${NC}\n"

# 1. 环境检查
if [[ $EUID -ne 0 ]]; then
   printf "${RED}错误: 必须使用 root 权限运行此脚本${NC}\n"
   exit 1
fi

ARCH=$(uname -m)
case $ARCH in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *) printf "${RED}不支持的架构: $ARCH${NC}\n"; exit 1 ;;
esac

# 2. 创建目录
printf "${BLUE}[1/5] 创建系统目录...${NC}\n"
mkdir -p /etc/nodepass/certs
mkdir -p /var/log/nodepass
mkdir -p /usr/local/bin

# 3. 编译/获取二进制文件
# 在实际发布中，这里可以是 curl 下载。目前我们假设在源码目录下运行，执行构建。
if [ -f "./Makefile" ]; then
    printf "${BLUE}[2/5] 检测到源码，正在本地编译...${NC}\n"
    make build build-ctl
    cp bin/nodepass /usr/local/bin/
    cp bin/npctl /usr/local/bin/
else
    printf "${RED}错误: 未找到二进制文件或源码。请在项目根目录下运行此脚本。${NC}\n"
    exit 1
fi

# 4. 生成初始配置
if [ ! -f "/etc/nodepass/config.yaml" ]; then
    printf "${BLUE}[3/5] 生成默认配置文件...${NC}\n"
    /usr/local/bin/npctl --cmd init-config --out /etc/nodepass/config.yaml
    # 修改默认日志路径
    sed -i 's/path: ""/path: "\/var\/log\/nodepass\/agent.log"/' /etc/nodepass/config.yaml
else
    printf "${BLUE}[3/5] 配置文件已存在，跳过生成。${NC}\n"
fi

# 5. 安装 Systemd 服务
printf "${BLUE}[4/5] 配置 Systemd 服务...${NC}\n"
if [ -f "./scripts/nodepass.service" ]; then
    cp ./scripts/nodepass.service /etc/systemd/system/
    systemctl daemon-reload
    systemctl enable nodepass
    printf "${GREEN}服务已注册并设为自启动${NC}\n"
else
    printf "${RED}警告: 未找到 scripts/nodepass.service 文件${NC}\n"
fi

# 6. 完成
printf "${BLUE}[5/5] 安装完成!${NC}\n"
printf "${GREEN}------------------------------------${NC}\n"
printf "管理命令:\n"
printf "  启动: systemctl start nodepass\n"
printf "  停止: systemctl stop nodepass\n"
printf "  状态: systemctl status nodepass\n"
printf "  日志: tail -f /var/log/nodepass/agent.log\n"
printf "  CLI控制: npctl --cmd status\n"
printf "${GREEN}------------------------------------${NC}\n"
printf "配置文件位置: /etc/nodepass/config.yaml\n"
