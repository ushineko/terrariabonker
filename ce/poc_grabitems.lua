-- terrariabonker CE spike: port the ReGrind pickup-range hook to 1.4.5.7.
-- ReGrind hooks Terraria.Player::GrabItems just after a call that returns the grab
-- range in eax (then `mov [ebp-XX],eax`), injecting `imul eax,50` to scale pickup
-- radius. Dump the method so we can find the same site (an `FF 15 <abs>` call
-- immediately followed by `89 45 XX`) and build the AOB + injection for our build.

local LOG = getCheatEngineDir() .. [[tbonker_grab.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hexbytes(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== Player.GrabItems dump ===")
    local pid
    for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local m = mono_findMethod("Terraria","Player","GrabItems")
    if not m then log("GrabItems not found"); return end
    local jit = mono_compile_method(m)
    log(string.format("GrabItems JIT @0x%X", jit))
    local addr, count, prevStore = jit, 0, nil
    while count < 1200 do
      local good, ins = pcall(disassemble, addr)
      local sz = getInstructionSize(addr) or 0
      if not good or sz<1 then break end
      local b = readBytes(addr, 2, true)
      local flag = ""
      -- an FF 15 (call [abs]) is where a helper returns a value in eax
      if b and b[1]==0xFF and b[2]==0x15 then flag = "   <== call [abs] (returns eax)" end
      -- a `mov [ebp-XX],eax` (89 45 XX) right after such a call = the ReGrind site
      if b and b[1]==0x89 and b[2]==0x45 then flag = flag .. "   <== mov [ebp-XX],eax" end
      log(string.format("  +%X  %s   [%s]%s", addr-jit, ins, hexbytes(addr,sz), flag))
      addr=addr+sz; count=count+1
      if ins:upper():find("%- C3 %- RET") or ins:upper():find("%- C2 ") then break end
      if addr>jit+0x500 then break end   -- the site is well within the first part
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: "..tostring(err)) end
end
