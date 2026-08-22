-- terrariabonker CE recon: Player.maxMinions offset + its per-frame reset in
-- Player.ResetEffects, so a "raise minion cap" code patch can force the reset value.
-- Read <CE dir>/tbonker_minion.log after ce-terraria.sh runs this from autorun/.
local LOG = getCheatEngineDir() .. [[tbonker_minion.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hx(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end

local function dump(jit, from, count, label)
  log("-- "..label.." --")
  local addr = jit + from
  for _=1,count do
    local good,ins = pcall(disassemble, addr)
    local sz = getInstructionSize(addr) or 0
    if not good or sz < 1 then break end
    log(string.format("  +%X  %s  [%s]", addr-jit, ins, hx(addr,sz)))
    addr = addr + sz
  end
end

local t = createTimer(nil); t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    local pid; for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local cls = mono_findClass("Terraria", "Player")
    if not cls then log("Player class not found"); return end
    local flds = mono_class_enumFields(cls) or {}
    log("### Player minion fields")
    local maxoff = nil
    for _, f in ipairs(flds) do
      local name = tostring(f.name or "")
      if name:lower():find("minion") or name:lower():find("slotsminion") then
        log(string.format("  +0x%X  %s  (%s)", f.offset or 0, name, f.typename or f.type or "?"))
        if name == "maxMinions" then maxoff = f.offset end
      end
    end
    -- ResetEffects: find the maxMinions reset write (mov dword [reg+maxoff], imm)
    local m = mono_findMethod("Terraria", "Player", "ResetEffects")
    if m then
      local jit = mono_compile_method(m)
      log(string.format("\n### ResetEffects JIT @0x%X  (maxMinions off = 0x%X)", jit, maxoff or 0))
      -- scan for the C7 write whose disp matches maxoff
      local addr = jit
      for _=1,4000 do
        local sz = getInstructionSize(addr) or 0
        if sz < 1 then break end
        local ins = disassemble(addr)
        if maxoff and ins:find("%[") and ins:lower():find(string.format("%x", maxoff)) then
          log(string.format("  @+%X  %s  [%s]", addr-jit, ins, hx(addr, sz)))
        end
        addr = addr + sz
        if ins:upper():find("%- C3 %- RET") then break end
      end
    end
    log("\n=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
