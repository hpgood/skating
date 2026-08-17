-- ============================================
-- Skating Lua Test Script
-- 演示 sh() API 的错误处理
-- ============================================

log("=== Lua 测试步骤 ===")

-- 执行测试
log("执行 go test...")
local result, err = sh("go test ./...")
if err then
    log("测试失败: " .. tostring(err))
    log("失败输出: " .. (result or ""))
    error("test failed")
end

log("测试输出:")
log(result or "(空)")

log("=== 测试通过 ===")