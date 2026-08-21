-- terrariabonker CE spike: dump Terraria.Item field offsets (authoritative mono
-- metadata) to pin prefix / defense / rare and cross-check the /proc-derived
-- offsets. Templates all carry prefix 0, so prefix cannot be found by diffing —
-- this is the reliable route. Writes tbonker_itemfields.log next to the CE install.

local LOG = getCheatEngineDir() .. [[tbonker_itemfields.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== Item field dump ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL: attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL: mono"); return end

    local cls = mono_findClass("Terraria", "Item")
    log("Item class -> " .. tostring(cls))
    local fields = mono_class_enumFields(cls, true)   -- include parents
    log("field count (with parents): " .. tostring(fields and #fields or 0))

    -- the fields we care about, plus known anchors to validate the dump
    local want = {"prefix","defense","rare","damage","type","stack","useanimation",
                  "usetime","pick","axe","hammer","tileboost","createtile"}
    for _, f in ipairs(fields or {}) do
      local low = tostring(f.name or ""):lower()
      for _, w in ipairs(want) do
        if low == w then
          log(string.format("  %-16s offset=0x%X  static=%s  type=%s",
              f.name, f.offset or 0, tostring(f.isstatic or f.isStatic),
              tostring(f.vartype or f.typename or f.monotype)))
          break
        end
      end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
