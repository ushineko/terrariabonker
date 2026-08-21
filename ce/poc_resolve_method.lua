-- terrariabonker CE spike: prove that CE's mono dissector can resolve a managed
-- Terraria method to a native (JIT) address headlessly, under Wine, on this build.
--
-- Placed in Cheat Engine's autorun/ so it runs at startup with no interaction.
-- Writes a diagnostic log next to the CE install (readable from Linux via the
-- Proton prefix). If this log shows a real address for Player:ResetEffects, the
-- PyQt-drives-CE architecture is proven; if not, we learn exactly where it breaks.

local LOG = getCheatEngineDir() .. [[tbonker_poc.log]]

local function log(s)
  local f = io.open(LOG, "a")
  if f then f:write(tostring(s) .. "\n"); f:close() end
end

-- Run slightly after startup so the process list / mono are ready.
local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== terrariabonker CE PoC ===")
    log("CE version: " .. tostring(getCEVersion and getCEVersion() or "?"))

    -- Enumerate every process CE can see (tells us whether CE shares Terraria's
    -- wineserver namespace at all, and under what name the game is listed).
    local found_pid = nil
    local n = 0
    local pl = getProcessList()
    for k, v in pairs(pl) do
      n = n + 1
      local pid, name = k, v
      -- some CE builds key by pid->name, others give "pid-name" strings
      if type(v) == "string" and tostring(k):match("^%d+$") == nil then
        pid, name = v:match("^(%x+)%-(.+)$")
      end
      if name and tostring(name):lower():find("terraria") then
        log("  PROC MATCH: pid=" .. tostring(pid) .. " name=" .. tostring(name))
        found_pid = (type(pid) == "number") and pid
                    or (tonumber(pid, 10) or tonumber(pid, 16))
      end
    end
    log("total processes CE sees: " .. n)
    if not found_pid then
      -- log a sample so we can eyeball what CE actually sees
      local shown = 0
      for k, v in pairs(pl) do
        log("  proc: " .. tostring(k) .. " = " .. tostring(v)); shown = shown + 1
        if shown >= 25 then break end
      end
      log("FAIL: no Terraria process visible to CE (likely a separate wineserver namespace)")
      return
    end

    if not openProcess(found_pid) then
      log("FAIL: openProcess(" .. tostring(found_pid) .. ") returned false")
      return
    end
    log("attached: pid=" .. tostring(getOpenedProcessID()))

    -- Bring up CE's mono data collector (injects MonoDataCollector into the target).
    local launched = LaunchMonoDataCollector()
    log("LaunchMonoDataCollector -> " .. tostring(launched))
    if not launched or launched == 0 then
      log("FAIL: mono data collector did not launch")
      return
    end

    -- Resolve Terraria.Player, then ResetEffects, then its JIT address.
    local cls = mono_findClass("Terraria", "Player")
    log("mono_findClass('Terraria','Player') -> " .. tostring(cls))
    if not cls or cls == 0 then
      -- some builds report an empty namespace; try that too
      cls = mono_findClass("", "Player")
      log("retry mono_findClass('','Player') -> " .. tostring(cls))
    end
    if not cls or cls == 0 then log("FAIL: class not found"); return end

    local m = mono_findMethod("Terraria", "Player", "ResetEffects")
    log("mono_findMethod(Terraria,Player,ResetEffects) -> " .. tostring(m))
    if not m or m == 0 then
      m = mono_class_findMethod(cls, "ResetEffects")
      log("retry mono_class_findMethod -> " .. tostring(m))
    end
    if not m or m == 0 then log("FAIL: method not found"); return end
    log("method full name: " .. tostring(mono_method_getFullName and mono_method_getFullName(m)))

    local addr = mono_compile_method(m)
    log("mono_compile_method (JIT addr) -> " .. string.format("0x%X", addr or 0))
    if addr and addr ~= 0 then
      local bytes = readBytes(addr, 24, true)
      if bytes then
        local hex = {}
        for i = 1, #bytes do hex[i] = string.format("%02X", bytes[i]) end
        log("first bytes: " .. table.concat(hex, " "))
      end
      log("SUCCESS: managed method resolved to a native address")
    end

    -- Bonus for the real work: disassemble the method so we can see the exact
    -- instruction(s) that reset pickSpeed / tileRange (the future patch sites).
    local dis = mono_method_disassemble and mono_method_disassemble(m)
    if dis then
      log("--- ResetEffects disassembly (first 1200 chars) ---")
      log(tostring(dis):sub(1, 1200))
    end
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
  log("=== done ===")
end
