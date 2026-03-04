#!/bin/bash

# 配置
IMAGE_NAME="nodeox/nodepass"
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "v2.0.0-dev")
BUILD_TIME=$(date -u '+%Y%m%d-%H%M%S')
TAG="${VERSION}-${BUILD_TIME}"

echo "Starting build for ${IMAGE_NAME}:${TAG}..."

# 执行 Docker 构建
docker build -t ${IMAGE_NAME}:${TAG} -t ${IMAGE_NAME}:latest .

if [ $? -eq 0 ]; then
    echo "-------------------------------------------------------"
    echo "Build Success!"
    echo "Images created:"
    echo "  - ${IMAGE_NAME}:${TAG}"
    echo "  - ${IMAGE_NAME}:latest"
    echo ""
    echo "To push images, run:"
    echo "  docker push ${IMAGE_NAME}:${TAG}"
    echo "  docker push ${IMAGE_NAME}:latest"
    echo "-------------------------------------------------------"
else
    echo "Build failed!"
    exit 1
fi
