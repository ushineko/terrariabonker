-- terrariabonker CE recon PASS 2 (1.4.5.7 differs from ReGrind's 1.4.5):
--  * enumerate CommonDrop fields to pin chanceNumerator/chanceDenominator offsets
--  * dump TryDroppingItem fully; find the chance compare + which reg/offset holds
--    the denominator so we can cap it
--  * get param signatures for TriggerPing and Player.Teleport
--  * dump TriggerPing further to choose an inject point with known arg offsets
local LOG = getCheatEngineDir() .. [[tbonker_droptp.log]]
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

local function sig(ns, cls, mth)
  local ok, res = pcall(function()
    local m = mono_findMethod(ns, cls, mth)
    if not m then return "(method not found)" end
    local pars = mono_method_getParameters and mono_method_getParameters(m)
    if type(pars) == "table" then
      local o={}
      for i,p in ipairs(pars) do o[i] = (p.name or "?")..":"..(p.type or p.typename or "?") end
      return "("..table.concat(o,", ")..")"
    end
    return "(no param API)"
  end)
  return ok and res or ("sig err: "..tostring(res))
end

local t = createTimer(nil); t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    local pid; for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end

    -- CommonDrop field offsets
    log("### CommonDrop fields")
    local cls = mono_findClass("Terraria.GameContent.ItemDropRules", "CommonDrop")
    if cls then
      local flds = mono_class_enumFields(cls) or {}
      for _,f in ipairs(flds) do
        log(string.format("  +0x%X  %s  (%s)", f.offset or 0, f.name or "?", f.typename or f.type or "?"))
      end
    else log("  CommonDrop class not found") end

    -- TryDroppingItem full dump
    local m1 = mono_findMethod("Terraria.GameContent.ItemDropRules", "CommonDrop", "TryDroppingItem")
    if m1 then
      local jit = mono_compile_method(m1)
      log(string.format("\n### TryDroppingItem JIT @0x%X  sig %s", jit, sig("Terraria.GameContent.ItemDropRules","CommonDrop","TryDroppingItem")))
      dump(jit, 0, 90, "full body", true)
    end

    -- TriggerPing sig + deeper dump
    local m2 = mono_findMethod("Terraria", "Main", "TriggerPing")
    if m2 then
      local jit = mono_compile_method(m2)
      log(string.format("\n### TriggerPing JIT @0x%X  sig %s", jit, sig("Terraria","Main","TriggerPing")))
      dump(jit, 0, 40, "body", true)
    end

    log(string.format("\n### Player.Teleport sig %s", sig("Terraria","Player","Teleport")))
    log("\n=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
