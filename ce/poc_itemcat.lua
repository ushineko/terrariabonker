-- terrariabonker CE recon: dump Terraria.Item field offsets relevant to weapon class
-- (melee/ranged/magic/summon/accessory/DamageType) so the prefix editor can show only
-- item-appropriate modifiers. Read <CE dir>/tbonker_itemcat.log after ce-terraria.sh.
local LOG = getCheatEngineDir() .. [[tbonker_itemcat.log]]
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
    log("### Terraria.Item class-flag fields")
    local want = {"accessory","melee","ranged","magic","summon","damagetype","damageclass",
                  "useStyle","autoReuse","consumable","shoot","dye","expert","material"}
    for _, f in ipairs(flds) do
      local name = tostring(f.name or "?"); local low = name:lower()
      for _, w in ipairs(want) do
        if low == w:lower() or low:find(w:lower()) then
          log(string.format("  +0x%X  %-22s (%s)", f.offset or 0, name, f.typename or f.type or "?"))
          break
        end
      end
    end
    log("\n=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
