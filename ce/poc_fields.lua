-- terrariabonker CE spike #2: dump the Player fields that back the frame-reset
-- cheats (pickSpeed / tileSpeed / wallSpeed / tileRangeX/Y / grab range) with
-- their offsets and static flags. These offsets tell us what native writes to
-- look for in ResetEffects (the patch sites).

local LOG = getCheatEngineDir() .. [[tbonker_fields.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== Player field dump ===")
    -- attach (getProcessList keys by numeric pid -> name)
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local cls = mono_findClass("Terraria", "Player")
    log("Player class -> " .. tostring(cls))
    local fields = mono_class_enumFields(cls, true)   -- include Entity parent
    log("field count (with parents): " .. tostring(fields and #fields or 0))

    -- show the shape of a field entry once
    if fields and fields[1] then
      local keys = {}
      for k, _ in pairs(fields[1]) do keys[#keys+1] = k end
      log("field entry keys: " .. table.concat(keys, ", "))
    end

    local want = {"pickspeed","tilespeed","wallspeed","tilerange","blockrange",
                  "grab","reach","mount"}
    for _, f in ipairs(fields or {}) do
      local nm = tostring(f.name or "")
      local low = nm:lower()
      for _, w in ipairs(want) do
        if low:find(w) then
          log(string.format("  %-22s offset=0x%X  static=%s  type=%s",
              nm, f.offset or 0, tostring(f.isstatic or f.isStatic),
              tostring(f.vartype or f.typename or f.monotype)))
          break
        end
      end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
