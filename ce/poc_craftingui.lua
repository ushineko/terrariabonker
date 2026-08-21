-- terrariabonker CE spike: is toggling the crafting window a flag flip or a method
-- call? Enumerate Terraria.UI.CraftingUI — its fields (look for a visibility bool)
-- and its methods (Show/Hide/Toggle/SetActive/Open/Close). That decides the hook.

local LOG = getCheatEngineDir() .. [[tbonker_craftui.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== Terraria.UI.CraftingUI ===")
    local pid
    for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local cls = mono_findClass("Terraria.UI","CraftingUI")
    if not cls then log("class not found (Terraria.UI.CraftingUI)");
      cls = mono_findClass("Terraria.UI.CraftingUI",""); end
    log("class -> "..tostring(cls))
    if not cls then return end

    log("-- fields --")
    for _, f in ipairs(mono_class_enumFields(cls, true) or {}) do
      log(string.format("  %-28s offset=0x%X static=%s type=%s",
          f.name, f.offset or 0, tostring(f.isstatic or f.isStatic),
          tostring(f.vartype or f.typename or f.monotype)))
    end
    log("-- methods --")
    for _, m in ipairs(mono_class_enumMethods(cls) or {}) do
      log("  "..tostring(m.name))
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: "..tostring(err)) end
end
