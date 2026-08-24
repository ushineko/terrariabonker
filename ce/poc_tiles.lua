-- terrariabonker CE spike (ore extractor recon): resolve the mining path and the
-- Main.tile static, so the tile map can be read from outside. Read-only.
local LOG = getCheatEngineDir() .. [[tbonker_tiles.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hx(a,n)
  local b = readBytes(a,n,true) or {}
  local o = {}
  for i=1,#b do o[i] = string.format("%02X", b[i]) end
  return table.concat(o, " ")
end
local t = createTimer(nil); t.Interval = 1800
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== ore extractor recon ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local targets = {
      {"Terraria", "Player", "PickTile", 64},
      {"Terraria", "WorldGen", "KillTile", 48},
      {"Terraria", "Player", "ItemCheck_UseMiningTools_ActuallyUseMiningTool", 48},
    }
    for _, tg in ipairs(targets) do
      local m = mono_findMethod(tg[1], tg[2], tg[3])
      if not m then log(tg[2] .. "::" .. tg[3] .. " NOT FOUND")
      else
        local jit = mono_compile_method(m)
        log(string.format("\n%s::%s  JIT @0x%X", tg[2], tg[3], jit))
        log("  bytes: " .. hx(jit, tg[4]))
        local a = jit
        for _=1,18 do
          local good, ins = pcall(disassemble, a)
          local sz = getInstructionSize(a) or 0
          if not good or sz < 1 then break end
          log(string.format("   +%02X  %s", a - jit, ins))
          a = a + sz
          if a - jit >= tg[4] then break end
        end
      end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
