-- terrariabonker CE spike: CraftingUI.DrawGridToggle draws the "Crafting Window"
-- toggle button AND handles its click. Dump it to find what the click toggles.
-- (Finding: the toggle is NOT a plain `mov [flag]` here — it routes through helper
-- calls to nearby CraftingUI methods. See CRAFTING_WINDOW_FINDINGS.md.)

local LOG = getCheatEngineDir() .. [[tbonker_gridtoggle.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hexbytes(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== CraftingUI.DrawGridToggle ===")
    local pid
    for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local m = mono_findMethod("Terraria.UI","CraftingUI","DrawGridToggle")
    if not m then log("not found"); return end
    local jit = mono_compile_method(m)
    log(string.format("JIT @0x%X", jit))
    local addr, count = jit, 0
    while count < 800 do
      local good, ins = pcall(disassemble, addr)
      local sz = getInstructionSize(addr) or 0
      if not good or sz < 1 then break end
      log(string.format("  +%X  %s   [%s]", addr - jit, ins, hexbytes(addr, sz)))
      addr = addr + sz; count = count + 1
      if ins:upper():find("%- C3 %- RET") or ins:upper():find("%- C2 ") then break end
      if addr > jit + 0x600 then break end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
