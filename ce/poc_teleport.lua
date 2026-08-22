-- terrariabonker CE recon: map-ping teleport, PASS 2 (Player.Teleport half).
-- poc_droploot_teleport.lua captured Main.TriggerPing but never compiled Player.Teleport
-- (the mono param API in this CE build returns nothing). This pass uses only the API
-- idioms proven by the other pocs (mono_findMethod + mono_compile_method + disassemble):
--  * compile Player.Teleport, log its JIT address + prologue so we can pick the call
--    target and read the 32-bit mono calling convention (this-ptr, Vector2-by-value,
--    the two int args Style/extraInfo).
--  * re-dump Main.TriggerPing and its two ping-helper callees so we can see how the ping
--    world-coordinate Vector2 is formed — that is the (x,y) the injected hook will feed
--    to Teleport.
-- Read <CE dir>/tbonker_tp2.log after ce-terraria.sh runs this from autorun/.
local LOG = getCheatEngineDir() .. [[tbonker_tp2.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hx(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end

local function dump(jit, from, count, label, stop_ret)
  log("-- "..label.." --")
  local addr = jit + from
  for _=1,count do
    local good,ins = pcall(disassemble, addr)
    local sz = getInstructionSize(addr) or 0
    if not good or sz < 1 then break end
    log(string.format("  +%X  %s  [%s]", addr-jit, ins, hx(addr,sz)))
    addr = addr + sz
    if stop_ret and ins:upper():find("%- C3 %- RET") then break end
  end
end

local function dump_method(ns, cls, mth, count)
  local m = mono_findMethod(ns, cls, mth)
  if not m then log(string.format("\n### %s.%s.%s -- NOT FOUND", ns, cls, mth)); return end
  local jit = mono_compile_method(m)
  log(string.format("\n### %s.%s.%s  method=0x%X  JIT @0x%X", ns, cls, mth, tonumber(m) or 0, jit or 0))
  if jit and jit ~= 0 then dump(jit, 0, count or 44, mth.." body", true) end
  return jit
end

local t = createTimer(nil); t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    local pid; for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end

    dump_method("Terraria", "Player", "Teleport", 64)
    dump_method("Terraria", "Main", "TriggerPing", 70)

    log("\n=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
