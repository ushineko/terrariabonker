-- terrariabonker CE spike: port the ReGrind reach hook to 1.4.5.7. The modding
-- scene unifies tile reach via Terraria.DataStructures.TileReachCheckSettings.
-- GetRanges(this, out x, out y) — forcing its two outputs extends mining/placement/
-- interaction together. Dump the full method so we can build a /proc-friendly
-- IN-PLACE patch (equal-length; no code cave) at the range-value writes.

local LOG = getCheatEngineDir() .. [[tbonker_getranges.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hexbytes(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== TileReachCheckSettings.GetRanges full dump ===")
    local pid
    for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local m = mono_findMethod("Terraria.DataStructures", "TileReachCheckSettings", "GetRanges")
    if not m then log("method not found via (ns,cls,name); trying alt")
      m = mono_findMethod("Terraria.DataStructures.TileReachCheckSettings", "", "GetRanges") end
    if not m then log("STILL not found"); return end
    local jit = mono_compile_method(m)
    log(string.format("GetRanges JIT @0x%X", jit))
    local addr, count = jit, 0
    while count < 400 do
      local good, ins = pcall(disassemble, addr)
      local sz = getInstructionSize(addr) or 0
      if not good or sz<1 then break end
      log(string.format("  +%X  0x%X  %s   [%s]", addr-jit, addr, ins, hexbytes(addr,sz)))
      addr=addr+sz; count=count+1
      local up = ins:upper()
      if up:find("%- C2 ") or up:find("%- C3 %- RET") then break end
      if addr>jit+0x200 then break end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: "..tostring(err)) end
end
