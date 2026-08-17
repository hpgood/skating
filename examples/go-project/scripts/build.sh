#!/bin/bash
# ============================================
# Skating Example: Go Project Scripts
# 这些是 go-project 示例引用的外部脚本
# ============================================

echo "=== Go 项目构建脚本 ==="
echo "Build ID: ${SKA_BUILD_ID:-未设置}"
echo "Go 版本: $(go version 2>/dev/null || echo 'Go 未安装')"

# 编译
echo "正在编译..."
go build -v ./... 2>&1

# 测试
echo "正在运行测试..."
go test -v -count=1 ./... 2>&1

echo "=== 构建完成 ==="