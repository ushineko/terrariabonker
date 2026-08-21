-- terrariabonker CE spike #7: patch placement timing directly.
-- Player.ApplyItemTime(Item, float) computes itemTime = max(1, useTime*tileSpeed).
-- tileSpeed is clamped up to 3.0 by accessories, so a value approach can't force it
-- low. Instead overwrite the `max(edi,1)` block (B8 01000000 3B F8 0F4C F8, 10 bytes,
-- where edi = the computed time) with `mov edi, N` + NOPs, forcing a small constant
-- itemTime -> fast placement independent of tileSpeed. Autoplacement untouched.

local ITEMTIME = 4   -- frames between placements (lower = faster)

local LOG = getCheatEngineDir() .. [[tbonker_placepatch.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hex(b) local t={} for i=1,#b do t[i]=string.format("%02X",b[i]) end return table.concat(t," ") end
local function findSeq(code, seq)
  for i=1,#code-#seq do local ok=true
    for j=1,#seq do if code[i+j-1]~=seq[j] then ok=false break end end
    if ok then return i end end
end

local tmr = createTimer(nil)
tmr.Interval = 1500
tmr.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== patch placement timing (ApplyItemTime) ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local cls = mono_findClass("Terraria", "Player")
    local m
    for _, mm in ipairs(mono_class_enumMethods(cls) or {}) do
      local mptr = (type(mm)=="table") and (mm.method or mm.address) or mm
      local nm = (type(mm)=="table" and mm.name) or mono_method_getName(mptr)
      if nm == "ApplyItemTime" then
        local p = mono_method_get_parameters(mptr)
        if p and p.parameters and #p.parameters == 2 then m = mptr end
      end
    end
    if not m then log("FAIL: 2-arg ApplyItemTime not found"); return end
    local jit = mono_compile_method(m)
    log("ApplyItemTime JIT @ 0x" .. string.format("%X", jit))

    local code = readBytes(jit, 0x100, true)
    local pat = {0xB8,0x01,0x00,0x00,0x00, 0x3B,0xF8, 0x0F,0x4C,0xF8}   -- max(edi,1)
    local i = findSeq(code, pat)
    if not i then
      local patched = {0xBF,ITEMTIME,0x00,0x00,0x00, 0x90,0x90,0x90,0x90,0x90}
      log(findSeq(code, patched) and "ALREADY PATCHED" or "FAIL: max(edi,1) pattern not found")
      return
    end
    local site = jit + (i - 1)
    log("patch site @ 0x" .. string.format("%X", site) .. "  orig: " .. hex(readBytes(site,10,true)))
    -- mov edi, ITEMTIME ; nop*5
    writeBytes(site, 0xBF, ITEMTIME, 0x00, 0x00, 0x00, 0x90, 0x90, 0x90, 0x90, 0x90)
    log("patched: " .. hex(readBytes(site,10,true)) .. "  (itemTime forced to " .. ITEMTIME .. ")")
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
