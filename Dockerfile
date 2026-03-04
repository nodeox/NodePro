# Stage 1: Build
FROM golang:1.22-alpine AS builder

WORKDIR /app

# 安装构建依赖
RUN apk add --no-cache git make

# 拷贝依赖文件并下载
COPY go.mod go.sum ./
RUN go mod download

# 拷贝源码
COPY . .

# 编译二进制文件 (静态链接)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o nodepass ./cmd/nodepass/main.go

# Stage 2: Final Image
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /root/

# 从构建阶段拷贝二进制文件
COPY --from=builder /app/nodepass .

# 暴露端口 (SOCKS5, QUIC, Metrics)
EXPOSE 1080 443 9090

# 启动命令
ENTRYPOINT ["./nodepass"]
CMD ["--config", "configs/ingress.yaml"]
