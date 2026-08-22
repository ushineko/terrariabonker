-- terrariabonker CE recon: Terraria.Item field offsets for placeStyle (and re-confirm
-- createTile). Needed to render tile-drawn item icons (chests) from the Containers sheet.
-- Read <CE dir>/tbonker_itemplace.log after ce-terraria.sh runs this from autorun/.
local LOG = getCheatEngineDir() .. [[tbonker_itemplace.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local t = createTimer(nil); t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    local pid; for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local cls = mono_findClass("Terraria", "Item")
    if not cls then log("Item class not found"); return end
    local flds = mono_class_enumFields(cls) or {}
    log("### Terraria.Item fields matching place/style/tile/frame")
    for _, f in ipairs(flds) do
      local name = tostring(f.name or "?")
      local low = name:lower()
      if low:find("place") or low:find("style") or low:find("tile") or low:find("frame") then
        log(string.format("  +0x%X  %s  (%s)", f.offset or 0, name, f.typename or f.type or "?"))
      end
    end
    log("\n=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
