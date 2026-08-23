# Spec 033: Accessories take effect from the inventory

**Status**: COMPLETE
**Implementation Date**: 2026-08-23

> **Note**: No issue tracker ticket (personal utility).

## Context

Spec 032 made the vanity accessory column functional by widening two loop bounds, because
those slots already lived in `armor[20]` — the loop's item source, `GetEffectiveArmor(k)`,
indexes that array, so a wider bound simply reached slots that were already there.

That trick does not transfer to the inventory. `Player.inventory` is a different array
(59 items), so pushing `k` past 20 would read off the end of `armor[]`. The item source has
to change, not the bound.

### The hook

`UpdateEquips`' **first** loop already walks the whole inventory every frame — vanilla uses
it to refresh info and mechanical accessories, which is why a Depth Meter works from your
inventory today. Its compiled body hands us the item pointer directly:

```
1edbd4e4:  8b 87 d4 00 00 00   mov eax,[edi+0xd4]        ; this.inventory
1edbd4ea:  8b 4d a4            mov ecx,[ebp-0x5c]        ; j
1edbd4ed:  39 48 0c            cmp [eax+0xc],ecx         ; length  (ARR_LEN_OFF)
1edbd4f6:  8d 44 88 10         lea eax,[eax+ecx*4+0x10]  ; &inventory[j]  (ARR_DATA_OFF)
1edbd4fa:  8b 00               mov eax,[eax]             ; <-- Item* in eax
1edbd4fc:  8b 40 6c            mov eax,[eax+0x6c]        ; item.type  (ITEM_TYPE)
1edbd4ff:  89 45 a0            mov [ebp-0x60],eax
```

Every offset in that sequence matches a constant the project already had — `ARR_LEN_OFF`
`0x0C`, `ARR_DATA_OFF` `0x10`, `ITEM_TYPE` `0x6C`, and `[edi+0xd4]` is exactly
`INVENTORY_PTR_OFF` `-0x664` measured from the object base rather than `statLife`. The recon
and the existing constants corroborate each other.

So the shape is a **call injection**, not a bound change: hook the point where the `Item*` is
already in a register inside a loop that already runs, and call the accessory machinery on
it. The project has precedent for calling a managed method from a cave (the map-ping
teleport calls `Player.Teleport`).

### Why this is affordable

`ApplyEquipFunctional` is an 11.6 KB method and the loop runs 58 times a frame, so calling it
unconditionally would be wasteful. `Item::accessory` is a byte at `+0x7D` (already pinned by
`poc_itemcat.lua`, and visible in loop 2's `movzx eax,[eax+0x7d]`), so the stub tests it
first and calls only for real accessories — typically a handful, not 58.

## Requirements

1. A toggleable cheat that makes accessories anywhere in the inventory grant their full
   effects, without being equipped.
2. Prefix bonuses (Menacing, Warding, …) on those accessories apply too, matching what spec
   032 did for the vanity column.
3. Non-accessories cost nothing measurable: they are rejected by a byte test before any call.
4. Disabling restores the original bytes and scrubs the stub; a game restart clears it.
5. It persists and auto-restores through the existing profile machinery.

### Technical

6. **Injection point** is `1edbd4fa`-equivalent: clobber the 5 bytes `8b 00 8b 40 6c`
   (`mov eax,[eax]` + `mov eax,[eax+0x6c]`) with a `jmp rel32`, and reproduce both in the
   stub. Anchored on the surrounding loop body — the array bounds check, the `lea`, and the
   two `Refresh*` calls — with the ebp displacements and call rel32s wildcarded.
7. **Stub.** As built it follows the teleport stub's discipline rather than a hand-rolled
   scratch frame: `pushad`/`popad` around everything, the `Item*` parked in `esi`, and `esp`
   restored from `ebx` after each call so it is correct whether or not mono cleaned the
   arguments (mono emits `ret N` for some methods, which a `sub esp`/`add esp` pair would
   double-count).

   ```
       mov  eax,[eax]              ; Item*  (displaced)
       cmp  byte [eax+0x7d],0      ; item.accessory
       je   skip
       pushad
       mov  esi,eax                ; Item*
       mov  ebx,esp
       push esi / push 0 / push edi        ; item, slot 0, this
       mov  eax,<ApplyEquipFunctional> ; call eax ; mov esp,ebx
       push esi / push edi                 ; item, this
       mov  eax,<GrantPrefixBenefits>  ; call eax ; mov esp,ebx
       push esi / push edi
       mov  eax,<GrantArmorBenefits>   ; call eax ; mov esp,ebx
       popad                       ; eax is the Item* again
   skip:
       mov  eax,[eax+0x6c]         ; item.type (displaced)
       jmp  back
   ```

8. **Call targets** are read from the rel32 of calls we have already anchored, rather than
   anchoring each method's prologue: `ApplyEquipFunctional` from the `equip_apply` anchor
   (`+15`), `GrantPrefixBenefits` (`+20`) and `GrantArmorBenefits` (`+36`) from
   `equip_benefits`. Target = `site + 5 + rel32`. This reuses anchors that spec 032 already
   verified. All three are called, so an inventory accessory goes through exactly what an
   equipped one does (`GrantArmorBenefits` added at the maintainer's request).
9. **Slot 0** is passed for the same reason spec 032 clamps: the slot argument is only ever
   used to index `hideVisibleAccessory`, which is `bool[10]`.
10. Anchors are recorded in the spec-030 ledger with the build key they are confirmed on, and
    resolved with the multi-site rule in case `UpdateEquips` is JIT'd more than once.

## Risks & Assumptions

- **Stacking is unbounded and much larger than spec 032's.** In the vanity column an
  accessory could double; from the inventory, forty of the same accessory apply forty times.
  Vanilla has no dedupe for this because it never expected the case. Accepted as trainer
  behaviour for now; a "count each type once" guard is real assembly work and is deferred.
- **The stub runs inside a hot loop** — 58 times a frame. The `item.accessory` test keeps the
  expensive calls to the accessories actually carried, and the measured cost is within noise
  (see Testing). It must not disturb the caller's outgoing argument area, hence pushad/popad
  and restoring `esp` from `ebx`.
- **Register discipline.** `eax` is reloaded from the scratch slot after the calls because the
  callees clobber it; `edi` (`this`) is callee-saved by the JIT's own convention, which the
  surrounding code already relies on when it writes `mov [esp],edi` before every call.
- **Visual coupling.** Every inventory accessory follows `hideVisibleAccessory[0]`.
- **Wings and mounts.** Several wings active at once means the last one wins, as in spec 032.
- **Rollback.** `git revert`; the cheat is off by default and disable restores the displaced
  bytes and scrubs the cave. No save-format change.
- **Out of scope.** Piggy bank, safe and Defender's Forge: nothing in `UpdateEquips` iterates
  them, so they need a hand-written loop in a cave. The Void Vault (`bank4`) *does* have an
  existing loop and would be the cheapest next addition, but it is gated on carrying the Void
  Bag and is not part of this spec.

## Acceptance Criteria

- [x] An `inventory_accs` cheat appears on the Trainer tab, off by default, with a note
      explaining what it does
- [x] With it on, an accessory carried in the inventory (not equipped) grants its effect
      in-game — verified with wings and a movement accessory
- [x] A prefix bonus on an inventory accessory applies (Warding defense, as verified for
      spec 032)
- [x] Non-accessory items are rejected by the byte test: no call is made for them
- [x] Disabling restores the displaced bytes, scrubs the stub, and the effects stop
- [x] The anchor still resolves with the cheat applied (cold re-resolve), so it can be turned
      off after a restart
- [x] The stub leaves the stack as it found it: `pushad`/`popad` with `esp` restored from
      `ebx` after each call (headless test), and the game survives both enabling and
      disabling while the site executes 58 times a frame
- [x] No measurable performance cost: 4 interleaved A/B samples of the game process's CPU
      gave off 105.9% (stdev 1.9) vs on 104.4% (stdev 1.6) of one core — a delta of -1.6
      points against ~1.9 points of noise. CPU, not frame rate; a first non-interleaved
      attempt was inconclusive because the game's own workload drifts more than the effect
- [x] Anchors carry the build key they were confirmed on
- [x] The cheat persists and auto-restores like the others
- [x] All tests pass headless; flake8 clean on changed files; security review recorded
- [x] README updated; version bumped to 0.23.0 (maintainer confirmed)

## Executive Summary

Accessories now grant their effects from anywhere in the inventory, without being equipped.
This could not reuse spec 032's trick — the vanity slots were already inside `armor[20]`, so
a wider loop bound reached them, while inventory items live in a different array entirely.
Instead it hooks the loop `UpdateEquips` already runs over all 58 inventory slots each frame,
at the point where the `Item*` is in `eax`, and calls the three methods an equipped accessory
goes through.

The cost concern is answered by testing `item.accessory` (a byte at `+0x7D`) before calling
anything: the 11.6 KB `ApplyEquipFunctional` runs only for items that are actually
accessories, typically a handful rather than 58.

Reviewers: `_inventory_accs_body` (register and stack discipline around three managed calls),
`_call_target` (reading a callee's entry out of an already-anchored call), and the
`inventory_scan` anchor.

## Testing

212 headless tests, flake8 clean, `pip-audit 2.10.0` clean. `tests/test_inventory_accs.py`
(9): call targets read from the anchored call sites, the site takes a jump and disable
restores it, the stub tests `item.accessory` before calling anything, all three methods are
called, pushad/popad with `esp` restored from `ebx` after each call, the `je` lands exactly
on the reproduced `mov eax,[eax+0x6c]`, the stub jumps back to the right address, the anchor
still resolves with the cheat applied, and disable scrubs the cave.

The built stub was disassembled against the live game before enabling: `je 0x31` landing on
the reproduced instruction, and all three call targets matching addresses seen independently
in the original disassembly (`0x21730000`, `0x1edaf518`, `0x1edad008`).

Cost: 4 interleaved A/B samples of the game process's CPU — off 105.9% (stdev 1.9) vs on
104.4% (stdev 1.6) of one core, a -1.6 point delta against ~1.9 points of noise, i.e. no
measurable cost. Interleaving mattered: a first attempt alternating in two long blocks was
inconclusive because the game's own workload drifts by more than the effect being measured.

Live, maintainer-verified: accessories carried in the inventory granted their effects
unequipped; moving a Warding accessory out of a vanity slot into the bag kept its defense
contribution, isolating the `GrantPrefixBenefits` call; disabling restored the displaced
bytes, scrubbed the cave and stopped the effects, with the game running throughout — both
directions exercised on a site executing 58 times a frame.
