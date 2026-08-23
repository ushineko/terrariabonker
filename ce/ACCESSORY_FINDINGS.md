# Accessory findings — making the vanity accessory slots functional (1.4.5.7)

Recon for spec 032. Two passes: the managed layer read straight from `Terraria.exe` with
`tools/ilrecon` (no Cheat Engine, no Wine, no running game), then the JIT'd x86 read from
`/proc/<pid>/mem` and disassembled with `objdump -b binary -m i386` (no capstone, nothing
installed).

## The question

"More accessories" is done two ways in tModLoader mods: add slots (needs new UI), or make
accessories work from inventory/bank slots (no UI). A third option turned out to exist:
Terraria already has **seven more accessory slots on screen** — the vanity column — and
already runs half of their logic.

## PASS 1 — the managed layer (`tools/ilrecon`)

`Player.UpdateEquips` has four loops. Two matter:

```
loop 2:  for (i = 0; i < 10; i++)                      // armour + functional accessories
             item = GetEffectiveArmor(i)
             if (!item.IsAir && IsItemSlotUnlockedAndUsable(i)
                 && (!item.expertOnly || Main.expertMode)
                 && UpdateEquips_CanItemGrantBenefits(i, item)) {
                 if (item.accessory) GrantPrefixBenefits(item)
                 GrantArmorBenefits(item)
             }

loop 4:  for (k = 3; k < 10; k++)
             if (IsItemSlotUnlockedAndUsable(k))
                 ApplyEquipFunctional(k, GetEffectiveArmor(k))
```

`Player.UpdateVisibleAccessories` runs its own loops — `3..9` and **`13..19`** — calling
`ApplyEquipVanity` for both. That method starts with `RefreshInfoAccsFromItemType`, then the
wing *visual*, werewolf/drone flags and `ApplyShader`.

**That asymmetry is the whole story**, and it matches what the maintainer saw in-game before
any patch existed: a Depth Meter or Tungsten Watch in a vanity slot works, Hermes Boots and
wings do not. Info accessories arrive via `ApplyEquipVanity`; everything else needs
`ApplyEquipFunctional`, which vanilla never calls for `13..19`.

| fact | evidence |
| --- | --- |
| `armor` is 20 items: `0-2` armour, `3-9` accessories, `10-12` vanity armour, `13-19` vanity accessories | `Player::.ctor` `ldc.i4.s 20; newarr Terraria.Item` |
| `hideVisibleAccessory` is 10 bools | `Player::.ctor` `ldc.i4.s 10; newarr System.Boolean` |
| the slot argument is used **only** for `hideVisibleAccessory[slot]` | 7 × `ldfld hideVisibleAccessory; ldarg.1; ldelem.u1` in `ApplyEquipFunctional` |
| the benefit gate passes anything ≥ 10 | `UpdateEquips_CanItemGrantBenefits`: switch covers `0..9`, default is `ldc.i4.1; ret` |
| the vanity mirror is already understood | `IsItemSlotUnlockedAndUsable` gates `8`/`18` on `extraAccessory`+expert and `9`/`19` on master mode, else true |
| `expertOnly` cannot be smuggled in | `ApplyEquipFunctional` opens with its own `expertOnly` / `Main.expertMode` check |
| inventory already feeds info + mechanical accessories | `UpdateEquips` loop 1 over `inventory[0..57]` |

**The hazard**: `hideVisibleAccessory` is `bool[10]`, so passing a slot ≥ 10 to
`ApplyEquipFunctional` throws `IndexOutOfRange` every frame. The slot must be clamped below
10 before the call. Since its only use is the hide-visual lookup, clamping costs nothing but
which checkbox governs a vanity item's visual.

## PASS 2 — the JIT'd code

Located without Cheat Engine by scanning executable regions for `UpdateEquips`' distinctive
immediates (Football `4743` = `0x1287` near Void Bag `4131` = `0x1023`), then disassembling
the surrounding bytes. Note both constants appear in several methods, so co-location is a
lead, not proof — the structural anchors below are what pin it.

Loop 4, as compiled (mono x86, cdecl with args written into the outgoing stack area):

```
1edbd758:  8b 45 8c        mov  eax,[ebp-0x74]      ; k
1edbd763:  e8 ..           call GetEffectiveArmor   -> eax = Item*
1edbd768:  89 44 24 08     mov  [esp+0x8],eax       ; arg2 = item
1edbd76c:  8b 45 8c        mov  eax,[ebp-0x74]      ; k
1edbd76f:  89 44 24 04     mov  [esp+0x4],eax       ; arg1 = slot   <-- clamp here
1edbd773:  89 3c 24        mov  [esp],edi           ; arg0 = this
1edbd776:  90              nop
1edbd777:  e8 ..           call ApplyEquipFunctional
1edbd77c:  83 45 8c 01     add  [ebp-0x74],0x1
1edbd780:  83 7d 8c 0a     cmp  [ebp-0x74],0xa      ; <-- loop bound
1edbd784:  7c aa           jl   loop head
```

Two consequences that shape the patch:

- The slot is already in **`eax`** when it is stored, so the clamp needs no `ebp`
  displacement and does not depend on the JIT's frame layout.
- The loop bound sits nine bytes past the call, so **one anchor covers both edits**.

Loop 2's bound is reached through the `item.accessory` test (`Item::accessory` = `+0x7D`,
already pinned by `poc_itemcat.lua`) and the two benefit calls:

```
1edbd62b:  0f b6 40 7d     movzx eax,BYTE PTR [eax+0x7d]   ; item.accessory
1edbd62f:  85 c0           test  eax,eax
1edbd633:  8b 45 98        mov   eax,[ebp-0x68]
             ... call GrantPrefixBenefits
1edbd644:  8b 45 98        mov   eax,[ebp-0x68]
             ... call GrantArmorBenefits
1edbd654:  83 45 9c 01     add   [ebp-0x64],0x1
1edbd658:  83 7d 9c 0a     cmp   [ebp-0x64],0xa            ; <-- loop bound
1edbd65c:  0f 8c ..        jl    loop head (near)
```

### Anchors (verified unique against the live process)

| anchor | AOB | resolves |
| --- | --- | --- |
| `equip_apply` | `89 44 24 08 8B 45 ?? 89 44 24 04 89 3C 24 90 E8 ?? ?? ?? ?? 83 45 ?? 01 83 7D ?? 0A 7C ??` | 1 hit |
| `equip_benefits` | `0F B6 40 7D 85 C0 74 ?? 8B 45 ?? 89 44 24 04 89 3C 24 8B C0 E8 ?? ?? ?? ?? 8B 45 ?? 89 44 24 04 89 3C 24 90 E8 ?? ?? ?? ?? 83 45 ?? 01 83 7D ?? 0A` | 1 hit |

A shorter `equip_benefits` (`E8 .. 83 45 ?? 01 83 7D ?? 0A 0F 8C`) matched unrelated code
elsewhere in the process — the `item.accessory` prologue is what makes it specific. The
alignment paddings (`8b c0`, `90`) are part of this build's code and are matched literally;
if a rebuild emits different padding the anchor stops matching and reports honestly, which
is what the build ledger (spec 030) exists for.

### The patch

- `equip_apply` offset 27: loop-4 bound `0A` → `14`.
- `equip_benefits` offset 48: loop-2 bound `0A` → `14`.
- `equip_apply` offset 7: replace the 7 bytes `89 44 24 04 89 3C 24` with `E9 <rel32>` plus
  two `90`s, jumping to a cave holding:

```
    83 f8 0a        cmp eax,0xa
    7c 03           jl  +3
    83 e8 0a        sub eax,0xa       ; vanity slot -> its functional mirror
    89 44 24 04     mov [esp+0x4],eax ; the displaced stores
    89 3c 24        mov [esp],edi
    e9 <rel32>      jmp back to the nop before the call
```

`ResetEffects` is currently JIT'd twice in this process while `UpdateEquips` is not; the
multi-site resolution from spec 030 covers either case.

## Not done here

Accessories taking effect from **inventory or bank** slots — the second shape of the original
request — needs more: `GetEffectiveArmor` indexes `armor[20]`, so the item source has to be
redirected inside the cave, and running an 11.6 KB method over 59 inventory slots every frame
needs measuring. The clamp built here is the same one that approach would need.
