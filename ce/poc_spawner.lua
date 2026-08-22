-- terrariabonker CE spike: port the ReGrind spawn-rate hook to 1.4.5.7. It forces
-- the two outputs of Spawner.GetSpawnRate(out spawnRate, out maxSpawns) at the
-- method epilogue (esi = out spawnRate ptr, edi = out maxSpawns ptr, held from the
-- prologue). Same esi/edi-out shape as GetRanges: inject at +0x1EAA over
-- `lea esp,[ebp-0C]; pop esi; pop edi`. Dump prologue (anchor) + epilogue (overwrite).
local LOG = getCheatEngineDir() .. [[tbonker_spawner.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hx(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end
local t = createTimer(nil); t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    local pid; for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local m = mono_findMethod("Terraria.GameContent", "Spawner", "GetSpawnRate")
    if not m then log("GetSpawnRate not found"); return end
    local jit = mono_compile_method(m); log(string.format("GetSpawnRate JIT @0x%X", jit))
    local addr = jit
    log("-- prologue (anchor) --")
    for _=1,22 do local good,ins=pcall(disassemble,addr); local sz=getInstructionSize(addr) or 0
      if not good or sz<1 then break end
      log(string.format("  +%X  %s  [%s]", addr-jit, ins, hx(addr,sz))); addr=addr+sz end
    log("-- epilogue (inject at +0x1EAA over 8D 65 F4 5E 5F) --")
    addr = jit + 0x1EA8
    for _=1,10 do local good,ins=pcall(disassemble,addr); local sz=getInstructionSize(addr) or 0
      if not good or sz<1 then break end
      log(string.format("  +%X  %s  [%s]", addr-jit, ins, hx(addr,sz))); addr=addr+sz
      if ins:upper():find("%- C3 %- RET") then break end end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
