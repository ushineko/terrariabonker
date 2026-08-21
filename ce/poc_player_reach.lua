-- terrariabonker CE spike: pin the reach fields on Player and their reset sites.
-- tileRangeX/tileRangeY are the shared base for mining + placement + interaction
-- reach. Enumerate their instance offsets from mono metadata, then disassemble
-- ResetEffects and log every write that references those offsets (that's the
-- frame-reset patch site to boost the base). blockRange included for reference.

local LOG = getCheatEngineDir() .. [[tbonker_reach.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hexbytes(addr, n)
  local b = readBytes(addr, n, true) or {}
  local out = {} for i=1,#b do out[i]=string.format("%02X", b[i]) end
  return table.concat(out, " ")
end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== Player reach fields + reset sites ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local cls = mono_findClass("Terraria", "Player")
    local fields = mono_class_enumFields(cls, true)
    local want = {tilerangex=1, tilerangey=1, tilerangex2=1, tilerangey2=1,
                  lasttilerangex=1, lasttilerangey=1, blockrange=1}
    local offs = {}   -- displacement-hex -> field name
    for _, f in ipairs(fields or {}) do
      local low = tostring(f.name or ""):lower()
      if want[low] then
        log(string.format("FIELD %-16s offset=0x%X static=%s", f.name, f.offset or 0,
            tostring(f.isstatic or f.isStatic)))
        offs[string.format("%08X", f.offset or 0)] = f.name
      end
    end

    local m = mono_findMethod("Terraria", "Player", "ResetEffects")
    local jit = mono_compile_method(m)
    log("ResetEffects JIT @ " .. string.format("0x%X", jit))
    local addr, count = jit, 0
    while count < 8000 do
      local good, ins = pcall(disassemble, addr)
      if not good or not ins then break end
      local sz = getInstructionSize(addr) or 0
      if sz < 1 then break end
      local up = ins:upper()
      for disp, nm in pairs(offs) do
        if up:find(disp) then
          log(string.format("[%s] @0x%X  %s  bytes: %s", nm, addr, ins, hexbytes(addr, sz)))
        end
      end
      addr = addr + sz; count = count + 1
      if addr > jit + 0x8000 then break end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
