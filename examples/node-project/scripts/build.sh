#!/bin/bash
# ============================================
# Node.js 构建脚本
# ============================================

echo "=== Node.js 项目构建 ==="
echo "Build ID: ${SKA_BUILD_ID:-未设置}"
echo "Node 版本: $(node --version 2>/dev/null || echo '未安装')"
echo "npm 版本: $(npm --version 2>/dev/null || echo '未安装')"

npm ci
npm run build --if-present
npm test --if-present

echo "=== 构建完成 ==="