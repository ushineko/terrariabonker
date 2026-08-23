# ilrecon — read Terraria's IL without Cheat Engine

Dumps method IL straight out of `Terraria.exe` using `System.Reflection.Metadata`, the same
approach as `tools/extract_item_names`. No Cheat Engine, no Wine prefix, and no running
game — which makes it the cheapest first pass when working out where a cheat should hook.
The `ce/` scripts remain the tool for the JIT'd side (mono method addresses, field offsets).

## Usage

```bash
cd tools/ilrecon
dotnet run -- <path-to-Terraria.exe> list  <substring>          # find types/methods
dotnet run -- <path-to-Terraria.exe> il    <Type::Method> ...   # dump a method's IL
dotnet run -- <path-to-Terraria.exe> calls <substring>          # methods whose IL contains it
dotnet run -- <path-to-Terraria.exe> field <Type>               # fields in declaration order
```

The exe path defaults to the Steam install used during development.

## What it is good for

- Finding the method that owns a behaviour (`list`, `calls`) before hunting for byte
  patterns.
- Reading loop bounds, constants and field accesses that become the AOB you scan for.
- Confirming array lengths and argument use — `hideVisibleAccessory` being `bool[10]`, and
  the slot argument only ever indexing it, is what made spec 032's clamp necessary and safe.

Worked example: `ce/ACCESSORY_FINDINGS.md`.

## Limits

- IL only. Field *offsets* and JIT addresses are runtime facts — use the `ce/` scripts or a
  live scan for those.
- Token resolution covers what these dumps need (methods, fields, type refs, strings);
  exotic signatures may print as a raw token.
- The disassembler handles the opcodes Terraria's own code uses.
