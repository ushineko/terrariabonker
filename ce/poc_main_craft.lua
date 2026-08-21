-- terrariabonker CE spike: find the crafting-window toggle. Enumerate Terraria.Main
-- fields whose names look crafting/recipe/window-related, with offset + static flag,
-- so we know what levers exist (e.g. HidePlayerCraftingMenu, recBigList, and any
-- crafting-window visibility bool the inventory icon toggles).

local LOG = getCheatEngineDir() .. [[tbonker_maincraft.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== Main crafting/window fields ===")
    local pid
    for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local cls = mono_findClass("Terraria","Main")
    log("Main class -> "..tostring(cls))
    local fields = mono_class_enumFields(cls, true)
    log("field count: "..tostring(fields and #fields or 0))
    local want = {"craft","recipe","recbig","window","hideplayer","invbottom","reclist"}
    for _, f in ipairs(fields or {}) do
      local low = tostring(f.name or ""):lower()
      for _, w in ipairs(want) do
        if low:find(w) then
          log(string.format("  %-32s offset=0x%X static=%s type=%s",
              f.name, f.offset or 0, tostring(f.isstatic or f.isStatic),
              tostring(f.vartype or f.typename or f.monotype)))
          break
        end
      end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: "..tostring(err)) end
end
