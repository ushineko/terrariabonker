-- terrariabonker CE spike (spec 037): resolve the pylon placement gate to its JIT
-- address and dump the prologue, so an AOB can be derived for it.
--
-- Read-only: resolves and reads, writes nothing to the game.
--
-- The target is TETeleportationPylon::PlacementPreviewHook_CheckIfCanPlace, whose IL is
--     type = GetPylonTypeFromPylonTileStyle(style)
--     return Main.PylonSystem.HasPylonOfType(type) ? 1 : 0
-- registered as PlacementHook(hook, badReturn: 1, ...), so returning 0 always is what
-- lifts the one-pylon-per-biome rule. The method is tiny and mono compiles it lazily,
-- so a pylon must have been placed at least once for this to find anything.
local LOG = getCheatEngineDir() .. [[tbonker_pylon.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hx(a,n)
  local b = readBytes(a,n,true) or {}
  local o = {}
  for i=1,#b do o[i] = string.format("%02X", b[i]) end
  return table.concat(o, " ")
end

local t = createTimer(nil); t.Interval = 1800
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== spec 037: pylon placement gate ===")
    local pid
    for k, v in pairs(getProcessList()) do
      if tostring(v):lower():find("terraria") then pid = k end
    end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end

    local targets = {
      {"Terraria.GameContent.Tile_Entities", "TETeleportationPylon",
       "PlacementPreviewHook_CheckIfCanPlace", 56},
      {"Terraria.GameContent", "TeleportPylonsSystem", "HasPylonOfType", 48},
      {"Terraria.GameContent.Tile_Entities", "TETeleportationPylon",
       "GetPylonTypeFromPylonTileStyle", 32},
    }
    for _, tg in ipairs(targets) do
      local ns, cls, meth, n = tg[1], tg[2], tg[3], tg[4]
      local m = mono_findMethod(ns, cls, meth)
      if not m then
        log(string.format("%s::%s NOT FOUND", cls, meth))
      else
        local jit = mono_compile_method(m)
        log(string.format("\n%s::%s  JIT @0x%X", cls, meth, jit))
        log("  bytes: " .. hx(jit, n))
        local addr = jit
        for _=1,20 do
          local good, ins = pcall(disassemble, addr)
          local sz = getInstructionSize(addr) or 0
          if not good or sz < 1 then break end
          log(string.format("   +%02X  %s", addr - jit, ins))
          addr = addr + sz
          if addr - jit >= n then break end
        end
      end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: " .. tostring(err)) end
end
