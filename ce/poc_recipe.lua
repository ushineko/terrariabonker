-- terrariabonker CE spike: recipe extraction recon. Enumerate Terraria.Recipe fields
-- and Main's recipe bookkeeping, and validate the walk that terrariabonker/recipes.py
-- does over /proc: Main static base (Main.player abs from get_LocalPlayer minus its
-- field offset) -> Main.recipe (+0xA68) -> Recipe[]; per Recipe: createItem (0x8),
-- requiredItem (0xC, Item[]), requiredTile (0x1C, scalar int). Item type/stack at
-- +0x6C/+0x88, createTile at +0xA0 (used to name stations).

local LOG = getCheatEngineDir() .. [[tbonker_recipe.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    local pid
    for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end

    log("=== Terraria.Recipe fields ===")
    local rc = mono_findClass("Terraria", "Recipe")
    for _, f in ipairs(mono_class_enumFields(rc, true) or {}) do
      log(string.format("  %-22s off=0x%X static=%s type=%s", f.name, f.offset or 0,
          tostring(f.isstatic or f.isStatic), tostring(f.vartype or f.typename or f.monotype)))
    end

    -- Main static base from get_LocalPlayer operand, then walk a few recipes
    local mc = mono_findClass("Terraria", "Main"); local playerOff
    for _, f in ipairs(mono_class_enumFields(mc, true) or {}) do
      if tostring(f.name) == "player" then playerOff = f.offset end
    end
    local jit = mono_compile_method(mono_findMethod("Terraria", "Main", "get_LocalPlayer"))
    local b = readBytes(jit, 6, true)
    local base = (b[3]+b[4]*256+b[5]*65536+b[6]*16777216) - playerOff
    local arr = readInteger(base + 0xA68)
    log(string.format("Main base=0x%X  recipe arr=0x%X  maxRecipes=%s", base, arr, tostring(readInteger(arr+0xC))))
    for i = 0, 3 do
      local ro = readInteger(arr + 0x10 + i*4)
      local ci = readInteger(ro + 0x8)
      local ing = readInteger(ro + 0xC)
      log(string.format("recipe %d: out=%d x%d tile=%d ingArrLen=%s",
          i, readInteger(ci+0x6C), readInteger(ci+0x88), readInteger(ro+0x1C),
          tostring(ing and readInteger(ing+0xC))))
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
