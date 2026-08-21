-- terrariabonker CE spike: the mining reach uses a 4-output GetRanges overload
-- (bounding box). Chain: IsTargetTileInItemRange -> helper (tail call) -> first
-- call inside helper = the 4-out GetRanges. Disassemble it and log every absolute
-- memory read with its live value to find the tileRange source mining actually uses.

local LOG = getCheatEngineDir() .. [[tbonker_mingr.log]]
local function log(s) local f=io.open(LOG,"a"); if f then f:write(tostring(s).."\n"); f:close() end end
local function hexbytes(a,n) local b=readBytes(a,n,true) or {} local o={} for i=1,#b do o[i]=string.format("%02X",b[i]) end return table.concat(o," ") end
local function firstCall(jit, maxins)
  local addr = jit
  for _=1,(maxins or 200) do
    local good, ins = pcall(disassemble, addr)
    local sz = getInstructionSize(addr) or 1
    if not good then break end
    local b = readBytes(addr,5,true)
    if b and b[1]==0xE8 then
      -- skip the mono class-init thunk (preceded by test [eax],1 / je)
      local rel=b[2]+b[3]*256+b[4]*65536+b[5]*16777216
      if rel>=0x80000000 then rel=rel-0x100000000 end
      local tgt = addr+5+rel
      -- heuristic: real managed call target is in the JIT range (>0x20000000-ish)
      if tgt > 0x20000000 then return tgt end
    end
    addr=addr+sz
  end
end

local t = createTimer(nil)
t.Interval = 1500
t.OnTimer = function(timer)
  timer.destroy()
  local ok, err = pcall(function()
    log("=== mining 4-out GetRanges disasm ===")
    local pid
    for k,v in pairs(getProcessList()) do if tostring(v):lower():find("terraria") then pid=k end end
    if not pid or not openProcess(pid) then log("FAIL attach"); return end
    if not LaunchMonoDataCollector() then log("FAIL mono"); return end
    local rm = mono_findMethod("Terraria","Player","IsTargetTileInItemRange")
    local rjit = mono_compile_method(rm)
    -- tail call = helper
    local addr, helper = rjit, nil
    for _=1,120 do
      local good, ins = pcall(disassemble, addr); local sz=getInstructionSize(addr) or 1
      if not good then break end
      local b=readBytes(addr,5,true)
      if b and b[1]==0xE8 then local rel=b[2]+b[3]*256+b[4]*65536+b[5]*16777216; if rel>=0x80000000 then rel=rel-0x100000000 end; helper=addr+5+rel end
      addr=addr+sz
      if ins and ins:upper():find("%- C3 %- RET") then break end
    end
    log(string.format("helper @0x%X", helper or 0))
    local gr = helper and firstCall(helper, 60)
    log(string.format("4-out GetRanges @0x%X", gr or 0))
    if not gr then return end
    addr = gr
    for _=1,400 do
      local good, ins = pcall(disassemble, addr); local sz=getInstructionSize(addr) or 0
      if not good or sz<1 then break end
      local note=""
      local b=readBytes(addr,6,true)
      if b and b[1]==0x8B and (b[2]==0x05 or b[2]==0x0D or b[2]==0x15 or b[2]==0x1D or b[2]==0x25 or b[2]==0x2D or b[2]==0x35 or b[2]==0x3D) then
        local ab=b[3]+b[4]*256+b[5]*65536+b[6]*16777216
        note=string.format("   <== ABS 0x%08X = %s", ab, tostring(readInteger(ab)))
      end
      log(string.format("  +%X  %s   [%s]%s", addr-gr, ins, hexbytes(addr,sz), note))
      addr=addr+sz
      if ins:upper():find("%- C3 %- RET") or ins:upper():find("%- C2 ") then break end
      if addr>gr+0x300 then break end
    end
    log("=== done ===")
  end)
  if not ok then log("LUA ERROR: "..tostring(err)) end
end
