-- terrariabonker CE spike #4 (the payoff): patch out the per-frame pickSpeed reset
-- in ResetEffects so a value we set from /proc actually holds - the frame race we
-- couldn't win externally, won by removing the reset from inside the code.
--
-- The reset is `fstp dword [edi+8D8]` (D9 9F D8 08 00 00). Blind-NOPing it would
-- leak the value the preceding fld left on the x87 stack, so we replace it with
-- `fstp st(0)` (DD D8 = pop, keep the stack balanced) + NOP padding. JIT address
-- moves per run, so we re-resolve ResetEffects and scan for the pattern.

local LOG = getCheatEngineDir() .. [[tbonker_patch.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hex(bytes) local t={} for i=1,#bytes do t[i]=string.format("%02X",bytes[i]) end return table.concat(t," ") end

local tmr = createTimer(nil)
tmr.Interval = 1500
tmr.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== patch pickSpeed reset ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local cls = mono_findClass("Terraria", "Player")
    -- statLife offset, so /proc can compute the pickSpeed address from its anchor
    for _, f in ipairs(mono_class_enumFields(cls, true) or {}) do
      if f.name == "statLife" then log("statLife offset = 0x" .. string.format("%X", f.offset)) end
      if f.name == "pickSpeed" then log("pickSpeed offset = 0x" .. string.format("%X", f.offset)) end
    end

    local m = mono_findMethod("Terraria", "Player", "ResetEffects")
    local jit = mono_compile_method(m)
    log("ResetEffects JIT @ 0x" .. string.format("%X", jit))

    -- scan the method body for the pickSpeed fstp: D9 9F D8 08 00 00
    local pat = {0xD9, 0x9F, 0xD8, 0x08, 0x00, 0x00}
    local code = readBytes(jit, 0x2000, true)
    local site = nil
    for i = 1, #code - #pat do
      local match = true
      for j = 1, #pat do if code[i + j - 1] ~= pat[j] then match = false; break end end
      if match then site = jit + (i - 1); break end
    end
    if not site then log("FAIL: pickSpeed fstp pattern not found"); return end
    log("pickSpeed reset @ 0x" .. string.format("%X", site))
    log("original bytes: " .. hex(readBytes(site, 6, true)))

    -- neutralize: fstp st(0) (DD D8) + 4x nop
    writeBytes(site, 0xDD, 0xD8, 0x90, 0x90, 0x90, 0x90)
    log("patched bytes:  " .. hex(readBytes(site, 6, true)))
    log("SUCCESS: pickSpeed reset neutralized (fstp st(0)+nop). Set pickSpeed via /proc; it should now hold.")
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
