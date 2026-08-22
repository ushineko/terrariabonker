-- terrariabonker CE spike: real crafting-station names. Terraria's tile display name
-- is Lang._mapLegendCache[ MapHelper.tileLookup[tileType] ]._value. Dump
-- MapHelper.TileToLookup (the tileLookup ushort[] read: `lea eax,[eax+esi*2+10];
-- movzx eax,word[eax]; add eax,edi`) and Lang.GetMapObjectName (the _mapLegendCache
-- read), whose operands recipes.py resolves by AOB. LocalizedText._value @0xC (String*).
local LOG = getCheatEngineDir() .. [[tbonker_tilename.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hx(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end
local function rstr(p) if not p or p==0 then return "" end
  local len=readInteger(p+8); if not len or len<1 or len>80 then return "" end
  local b=readBytes(p+12,len*2,true) or {}; local s="" for i=1,len do s=s..string.char(b[(i-1)*2+1] or 63) end return s end
local function dump(ns,cls,name)
  local m=mono_findMethod(ns,cls,name); if not m then log(name.." NOT FOUND"); return end
  local jit=mono_compile_method(m); log(string.format("-- %s @0x%X --",name,jit))
  local addr=jit
  for _=1,80 do local good,ins=pcall(disassemble,addr); local sz=getInstructionSize(addr) or 0
    if not good or sz<1 then break end
    log(string.format("  +%X %s [%s]",addr-jit,ins,hx(addr,sz))); addr=addr+sz
    if ins:upper():find("%- C3 %- RET") then break end end
end
local t=createTimer(nil); t.Interval=1500
t.OnTimer=function(timer) timer.destroy()
 local ok,err=pcall(function()
  local pid; for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
  if not pid or not openProcess(pid) then log("FAIL attach"); return end
  if not LaunchMonoDataCollector() then log("FAIL mono"); return end
  dump("Terraria.Map","MapHelper","TileToLookup")
  dump("Terraria.Localization","Lang","GetMapObjectName")
  log("=== done ===")
 end); if not ok then log("LUA ERROR: "..tostring(err)) end
end
