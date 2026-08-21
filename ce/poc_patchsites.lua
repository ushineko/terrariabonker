-- terrariabonker CE spike #3: find the native instructions in ResetEffects that
-- reset the frame-managed fields, so we know exactly what to NOP/patch.
-- Disassembles the JIT'd method linearly and flags any instruction whose operand
-- references pickSpeed(+0x8D8) / wallSpeed(+0x8DC) / tileSpeed(+0x8E0) /
-- blockRange(+0x9F8). (tileRangeX/Y are static -> handled separately later.)

local LOG = getCheatEngineDir() .. [[tbonker_patchsites.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== ResetEffects patch-site scan ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local m = mono_findMethod("Terraria", "Player", "ResetEffects")
    local jit = mono_compile_method(m)
    log("ResetEffects JIT @ " .. string.format("0x%X", jit))

    -- displacement strings as CE prints them, plus symbol names it may resolve
    local targets = {
      ["000008D8"] = "pickSpeed", ["000008DC"] = "wallSpeed",
      ["000008E0"] = "tileSpeed", ["000009F8"] = "blockRange",
    }
    local addr, count, hits = jit, 0, 0
    while count < 6000 do
      local good, ins = pcall(disassemble, addr)
      if not good or not ins then break end
      for disp, nm in pairs(targets) do
        if ins:find(disp) then
          log(string.format("[%s] %s", nm, ins))
          hits = hits + 1
        end
      end
      local sz = getInstructionSize(addr)
      if not sz or sz < 1 then break end
      addr = addr + sz
      count = count + 1
      -- native ret at the outer level ends the method; keep a hard bound too
      if ins:find("^%x+ %- C3 %- ret") or addr > jit + 0x8000 then break end
    end
    log(string.format("scanned %d instructions, %d field-write hits", count, hits))
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
