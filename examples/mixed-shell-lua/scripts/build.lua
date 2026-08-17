-- ============================================
-- Skating Lua Build Script
-- 演示 Lua 脚本中的条件编译和 sh() API 调用
-- ============================================

log("=== Lua 编译步骤 ===")

-- 获取环境变量
local build_id = os.getenv("SKA_BUILD_ID")
log("Build ID from Lua: " .. (build_id or "unknown"))

-- 使用 sh() 执行编译
log("执行 go build...")
local result, err = sh("go build -o myapp ./...")
if err then
    log("编译失败: " .. tostring(err))
    error("build failed")
end
log("编译输出: " .. (result or ""))

-- 根据构建编号决定行为
if build_id and tonumber(build_id) == 1 then
    log("这是首次构建，执行完整编译...")
    set_env("FULL_BUILD", "true")
end

log("=== Lua 编译步骤完成 ===")