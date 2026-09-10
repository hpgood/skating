-- build.lua
print("Building project...")
local out, err = sh("go build ./...")
if err then
  error("build failed: " .. err)
end
print(out)
