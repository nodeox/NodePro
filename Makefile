.PHONY: all build build-ctl test clean install proto lint bench

# 变量
BINARY_NAME=nodepass
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "v2.0.0-dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

all: lint test build build-ctl

# 构建
build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) cmd/nodepass/main.go

build-ctl:
	go build $(LDFLAGS) -o bin/npctl cmd/npctl/main.go

# 交叉编译
build-all:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-amd64 cmd/nodepass/main.go
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-arm64 cmd/nodepass/main.go

# 测试
test:
	go test -v -race ./internal/...

# 基准测试
bench:
	go test -bench=. -benchmem ./test/benchmark/...

# 代码检查
lint:
	go vet ./...

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
	go install $(LDFLAGS) cmd/npctl/main.go
