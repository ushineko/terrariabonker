-- terrariabonker CE spike #5: patch out all the frame-reset writes in ResetEffects
-- so /proc-set values hold — pickSpeed (mining), tileSpeed (placement speed),
-- wallSpeed, blockRange (placement reach, item-independent). Idempotent: a site
-- already patched is simply skipped. Re-resolves the method each run (JIT moves).

local LOG = getCheatEngineDir() .. [[tbonker_patchall.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hex(b) local t={} for i=1,#b do t[i]=string.format("%02X",b[i]) end return table.concat(t," ") end

-- fstp writes neutralized with fstp st(0)+nop (keep x87 stack balanced); the
-- blockRange mov is a plain nop-out.
local TARGETS = {
  {name="pickSpeed",  pat={0xD9,0x9F,0xD8,0x08,0x00,0x00}, patch={0xDD,0xD8,0x90,0x90,0x90,0x90}},
  {name="wallSpeed",  pat={0xD9,0x9F,0xDC,0x08,0x00,0x00}, patch={0xDD,0xD8,0x90,0x90,0x90,0x90}},
  {name="tileSpeed",  pat={0xD9,0x9F,0xE0,0x08,0x00,0x00}, patch={0xDD,0xD8,0x90,0x90,0x90,0x90}},
  {name="blockRange", pat={0xC7,0x87,0xF8,0x09,0x00,0x00,0x00,0x00,0x00,0x00},
                       patch={0x90,0x90,0x90,0x90,0x90,0x90,0x90,0x90,0x90,0x90}},
}

local function findSeq(code, seq)
  for i = 1, #code - #seq do
    local ok = true
    for j = 1, #seq do if code[i+j-1] ~= seq[j] then ok = false; break end end
    if ok then return i end
  end
end

local tmr = createTimer(nil)
tmr.Interval = 1500
tmr.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== patch all ResetEffects resets ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local m = mono_findMethod("Terraria", "Player", "ResetEffects")
    local jit = mono_compile_method(m)
    log("ResetEffects JIT @ 0x" .. string.format("%X", jit))
    local code = readBytes(jit, 0x2000, true)

    for _, tg in ipairs(TARGETS) do
      local i = findSeq(code, tg.pat)
      if i then
        local site = jit + (i - 1)
        writeBytes(site, tg.patch)
        log(string.format("%-11s @ 0x%X  %s -> %s", tg.name, site, hex(tg.pat), hex(tg.patch)))
      else
        -- maybe already patched: look for the patch bytes instead
        local j = findSeq(code, tg.patch)
        log(string.format("%-11s: pattern not found (%s)", tg.name,
            j and "ALREADY PATCHED" or "MISSING - check offsets"))
      end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
