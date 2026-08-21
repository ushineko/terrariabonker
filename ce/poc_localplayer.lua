-- terrariabonker CE spike: dump Main.get_LocalPlayer to re-derive the Main.player /
-- Main.myPlayer static addresses and the array layout used by resolve_local_player
-- (locate.py) to read the ground-truth live player (Main.player[Main.myPlayer]).
local LOG = getCheatEngineDir() .. [[tbonker_localplayer.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hx(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end
local t=createTimer(nil); t.Interval=1500
t.OnTimer=function(timer) timer.destroy()
 local ok,err=pcall(function()
  local pid; for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
  if not pid or not openProcess(pid) then log("FAIL attach"); return end
  if not LaunchMonoDataCollector() then log("FAIL mono"); return end
  local m=mono_findMethod("Terraria","Main","get_LocalPlayer")
  if not m then log("get_LocalPlayer not found"); return end
  local jit=mono_compile_method(m); log(string.format("get_LocalPlayer JIT @0x%X",jit))
  local addr=jit
  for i=1,40 do
   local good,ins=pcall(disassemble,addr); local sz=getInstructionSize(addr) or 0
   if not good or sz<1 then break end
   log(string.format("  +%X  %s  [%s]", addr-jit, ins, hx(addr,sz)))
   addr=addr+sz; if ins:upper():find("%- C3 %- RET") then break end
  end
  log("=== done ===")
 end); if not ok then log("LUA ERROR: "..tostring(err)) end
end
