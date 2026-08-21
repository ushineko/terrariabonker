-- terrariabonker CE spike #6: disassemble Player.ApplyItemTime(Item, float) - the
-- single funnel for tile/wall PLACEMENT timing (itemTime = useTime * tileSpeed).
-- We want to patch the multiply here so placement is fast regardless of tileSpeed
-- (which is clamped up to 3.0 by accessories, so the value approach can't win).

local LOG = getCheatEngineDir() .. [[tbonker_placement.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local tmr = createTimer(nil)
tmr.Interval = 1500
tmr.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== ApplyItemTime(Item,float) disasm ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local cls = mono_findClass("Terraria", "Player")
    -- enumerate ApplyItemTime overloads, pick the 2-param one (Item, float) = placement
    local m
    for _, mm in ipairs(mono_class_enumMethods(cls) or {}) do
      local mptr = (type(mm) == "table") and (mm.method or mm.address) or mm
      local nm = (type(mm) == "table" and mm.name) or mono_method_getName(mptr)
      if nm == "ApplyItemTime" then
        local p = mono_method_get_parameters(mptr)
        local pc = (p and p.parameters and #p.parameters) or 0
        log("overload ApplyItemTime params=" .. pc .. " method=" .. tostring(mptr))
        if pc == 2 then m = mptr end
      end
    end
    if not m or m == 0 then log("FAIL: 2-arg ApplyItemTime not found"); return end
    log("full name: " .. tostring(mono_method_getFullName(m)))
    local jit = mono_compile_method(m)
    log("JIT @ 0x" .. string.format("%X", jit))

    -- linear native disassembly of the whole (small) method
    local addr, n = jit, 0
    while n < 120 do
      local good, ins = pcall(disassemble, addr)
      if not good or not ins then break end
      log(ins)
      local sz = getInstructionSize(addr)
      if not sz or sz < 1 then break end
      addr = addr + sz; n = n + 1
      if ins:find(" %- C3 %- ret") or ins:find(" %- C2 ") then break end   -- ret / ret imm
    end
    log("=== done (" .. n .. " instructions) ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
