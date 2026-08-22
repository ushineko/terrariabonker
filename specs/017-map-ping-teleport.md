# Spec 017: Map-ping teleport (managed-call hook on TriggerPing → Player.Teleport)

**Status**: COMPLETE
**Implementation Date**: 2026-08-21

> **Note**: This work has no associated issue tracker ticket. It is a personal
> utility in a script monorepo. The cheat is ported from the FearLess "TerrariaReGrind"
> Cheat Engine table (credit in the README); the 1.4.5.7 sites were re-derived here.

## Context

Terraria fires `Main.TriggerPing(Vector2 position)` when the player drops a ping on
the fullscreen map. The ReGrind "map teleport" cheat hooks that entry, grabs the ping's
world coordinates, and calls `Player.Teleport(Vector2 newPos, int Style, int extraInfo)`
on the local player so pinging the map warps you there. This is the project's first
*managed-call* code cave: every existing injection only forces a register/memory value
(`_force_xy`, `_force_spawn`, `_cap_drop_denom`), whereas this one must CALL a managed
method from the borrowed cave.

Recon (read-only, via `ce/poc_teleport.lua`, log `tbonker_tp2.log`) pinned both sites and
their 32-bit mono calling conventions (cdecl, all args on the stack) on 1.4.5.7:

**`Player.Teleport(Vector2 newPos, int Style, int extraInfo)`** — instance:
- `[ebp+08]` = `this` (Player*)
- `[ebp+0C]` = `newPos.X` (float)
- `[ebp+10]` = `newPos.Y` (float)
- `[ebp+14]` = `Style` (int)  — branches only on 4/9/10; **0 = plain teleport, no side effects**
- `[ebp+18]` = `extraInfo` (int)

**`Main.TriggerPing(Vector2 position)`** — static:
- `[ebp+08]` = `position.X` (float) = ping **tile** X
- `[ebp+0C]` = `position.Y` (float) = ping **tile** Y

**Coordinate units (corrected during testing).** `TriggerPing` delivers the ping in
**tile** coordinates, but `Player.Teleport` sets `Player.position`, which is in **world
pixels** (1 tile = 16 px). Live capture confirmed this: a ping near the player read
`(3501.84, 334.69)` tiles while the player object held `(54445.78, 5782.0)` px
(= tiles `(3402.9, 361.4)`). So the stub must scale the ping coords **×16** before the
call — done by adding `0x02000000` to each coord's IEEE-754 float bits (exponent += 4),
no FPU or memory constant. The Player object base (Teleport's `this`) is
`life_addr − 0x738` — `statLife`'s object offset (`STATLIFE_FROM_OBJ`), from
`docs/discovery.md`; the local player is resolved via `resolve_local_player`
(`Main.player[myPlayer]`), not the raw scan, which can also return inert snapshots.

**Trigger gesture (confirmed in-game).** The ping fires on a **double-click** on the
fullscreen map (single left-click / drag pans the map). With the hook on, that
double-click warps the player to the clicked location.

JIT addresses are dynamic per session, so both the inject site and the Teleport call
target are resolved by position-independent AOB at enable time, and the displaced
class-init instruction (which embeds a runtime address) is avoided by injecting at a
position-independent instruction boundary.

## Requirements

1. A `teleport` injection cheat: when enabled, dropping a map ping teleports the local
   player to the ping location; disabling restores the original bytes; a game restart
   clears it (same lifecycle as the other injections).
2. On/off only — no tunable value (like `fast_place`). It carries no `ValueSpec`.
3. Toggleable from both the CLI (`terrariabonker patch enable/disable teleport`) and the
   GUI Trainer tab, listed in `PATCH_CATALOG` like the other patches.
4. Credit the FearLess ReGrind table in the README (per repo attribution policy).

### Technical

5. **Inject anchor (`trigger_ping`)** — position-independent tail at `TriggerPing+0x2D`:
   `8B 4D 08 89 4C 24 04 8B 4D 0C 89 4C 24 08 89 04 24 39 00 8D 6D 00`
   (`mov ecx,[ebp+08]; mov [esp+04],ecx; mov ecx,[ebp+0C]; mov [esp+08],ecx;
   mov [esp],eax; cmp [eax],eax; lea ebp,[ebp+00]`). Unique (non-`multi`). `inject_off = 0`.
   Overwrite = the first 7 bytes `8B 4D 08 89 4C 24 04` (`mov ecx,[ebp+08]; mov [esp+04],ecx`),
   which are position-independent and re-run in the cave.
6. **Call-target anchor (`player_teleport`)** — the two constant field stores at
   `Teleport+0x32`: `C7 83 F4 0B 00 00 64 00 00 00 C7 83 68 04 00 00 04 00 00 00`
   (`mov [ebx+BF4],64; mov [ebx+468],4`). Unique. Teleport entry = anchor_base − 0x32;
   that entry is the cave's absolute `call` target.
7. **Cave stub** (~41 bytes, borrowed via `_find_cave` — small-stub-safe per the existing
   note). `ebp` is valid at the inject point (frame set at `TriggerPing+1`):
   ```
   pushad                          ; preserve every GPR the original code still needs
   mov  ebx,esp                    ; save esp restore-point (survives the call in ebx)
   mov  eax,[ebp+08]               ; ping tile X (float bits)
   add  eax,0x02000000             ; ×16 -> world-pixel X (exponent += 4)
   mov  ecx,[ebp+0C]               ; ping tile Y (float bits)
   add  ecx,0x02000000             ; ×16 -> world-pixel Y
   push 0                          ; extraInfo
   push 0                          ; Style = 0 (plain teleport)
   push ecx                        ; newPos.Y (px)
   push eax                        ; newPos.X (px)
   push <player_base>              ; this  (life_addr − 0x738), baked at enable time
   mov  eax,<teleport_entry>       ; resolved via player_teleport anchor
   call eax
   mov  esp,ebx                    ; restore esp (correct whether callee cleans or not)
   popad
   mov  ecx,[ebp+08]              ; re-run displaced overwrite …
   mov  [esp+04],ecx             ; … so the original ping still proceeds
   jmp  <TriggerPing+0x34>        ; rel32 back to the byte after the overwrite
   ```
   `pushad`/`popad` protect `eax` (loaded at `+0x27`, consumed at `+0x3B` as the ping
   list) and the rest. esp is saved in `ebx` (callee-saved, preserved by Teleport) and
   restored after the call, so the stub is correct whether mono emits `ret N` (callee
   cleans) or `ret` — no `add esp,N` cleanup guess. The player both teleports and drops
   the ping.
8. **Managed-call injection path.** The existing `Injection.make_body(value)` contract
   cannot express a body needing `(player_base, teleport_entry)`. Extend minimally: add
   an optional `call_anchor` field and a context-aware builder so `_enable_injection`
   resolves the second anchor and the local player, then builds the stub. Keep the
   borrow-a-cave, jmp-in / jmp-back, persist-`{sites, stub_len}` mechanics unchanged.
   Do not regress the value-force injections.
9. **Displaced-byte handling** mirrors the loot cheat: `rerun_overwrite=False` (the body
   reproduces the two displaced instructions itself, after the call), single site.

## Risks & Assumptions

- **Player-address staleness.** `player_base` is baked into the stub at enable time. If
  the Player object moves (exit to menu / load a different character), the baked pointer
  goes stale and a ping would write through a bad `this` → crash. The address was stable
  across the whole test session (many pings, no crash). Mitigation for v1: document
  "re-toggle after a world/character reload," matching the other cheats' restart caveat.
  A later version can resolve the player dynamically in the cave
  (`[Main.player_arr + myPlayer*4 + 0x10]`, statics from `poc_localplayer`).
- **Coordinate units (RESOLVED).** The ping `Vector2` is in **tile** coordinates, not
  world pixels; the stub scales ×16. Confirmed by live capture (ping `(3501.84, 334.69)`
  tiles vs the player object's `(54445.78, 5782.0)` px) and by in-game testing — with the
  ×16 conversion, pings land on the clicked spot ("spot on", user-confirmed).
- **TriggerPing scope.** Assumes `TriggerPing` fires on the local player's map ping in
  single-player. The trainer is single-player only; multiplayer is out of scope.
- **Style = 0.** Chosen to avoid the 4/9/10 special branches (which read extra fields).
  Plain teleport, no dust/sound. Documented, not a limitation to fix.
- **Cave-borrow safety.** Same heuristic and failure mode as every other injection
  (clobbered stub → crash, recovered by disable / restart / re-derive by AOB). Stub is
  small (~41 B), within the small-stub-only envelope.
- **Build-specific AOBs.** Both anchors and `0x738` are 1.4.5.7-specific; re-derive with
  `ce/poc_teleport.lua`. A game update degrades to "anchor not found", never a bad write.
- **Rollback.** `git revert`; disable restores the original 7 bytes and scrubs the cave;
  a game restart clears the patch.

## Acceptance Criteria

- [x] `teleport` injection enables/disables via CLI and GUI Trainer tab; listed in
      `PATCH_CATALOG` with no `ValueSpec` (renders as a plain checkbox automatically)
- [x] Enable resolves `trigger_ping` (inject) and `player_teleport` (call target) by AOB,
      bakes `player_base = life_addr − 0x738`, writes the managed-call stub, and jumps in;
      disable restores the 7 displaced bytes and scrubs the cave (ground-truth verified on
      the live game)
- [x] Live in-game: with the cheat on, a fullscreen-map **double-click** teleports the
      local player to the clicked location; the game does not crash; coordinates land
      on-spot after the tile→pixel ×16 fix (user-confirmed "spot on")
- [x] `is_enabled` reads ground truth (an `E9` at the inject site); state persists across
      GUI restarts for the same pid
- [x] Managed-call path added without regressing the value-force injections
      (`tool_reach`, `pickup`, `spawn_rate`, `loot`) — 92 tests pass
- [x] Tests pass headless (stub-bytes incl. ×16, calling-convention, enable-disable
      roundtrip, built against the synthetic memory image); flake8 clean
- [x] README documents the cheat (double-click gesture) and credits the FearLess ReGrind
      table; version bumped to 0.13.0 (user-approved) in source + README

## Alternatives Considered

- **On-demand "teleport to X,Y" from the GUI** (call `Teleport` out of band on the
  local player): rejected. Executing a managed method from `/proc` requires a valid mono
  thread/GC context; there is no safe way to synthesize that externally. Hooking a method
  the game already calls on its own main thread (TriggerPing) is the sane trigger.
- **Raw `Player.position` write** (no code patch): rejected. It bypasses `Teleport`'s
  safe placement and side-effects and can wedge the player inside blocks or desync state.
- **Injecting at the class-init `mov eax,<imm>` (TriggerPing+0x6)**: rejected. That
  instruction embeds a runtime (ASLR/JIT) address, so its bytes are not a stable overwrite;
  the `+0x2D` tail is position-independent and matches the existing anchor idiom.

## Executive Summary

Adds `teleport`, the project's first *managed-call* code cave: a stub hooked at
`Main.TriggerPing+0x2D` reads the ping's tile coordinates, scales them ×16 to world
pixels, and **calls** `Player.Teleport(this, x, y, 0, 0)` on the local player — so a
fullscreen-map double-click warps you there. Two AOBs are resolved at enable time
(`trigger_ping` inject site, `player_teleport` call target = match−0x32); `this` is the
authoritative `resolve_local_player` object base; esp is saved/restored via `ebx` to be
agnostic to mono's caller/callee stack cleanup. The tile→pixel ×16 factor was found by
capturing live ping coords against the player's real position, not assumed. Reviewers:
`patcher._teleport_body` (the ×16 add + call), the two new anchors, and
`_enable_injection`'s `call_anchor` branch.

## Testing

`tests/test_patcher.py`: `test_teleport_managed_call_stub` (AOB resolve, baked `this` =
`life_addr − STATLIFE_FROM_OBJ`, call target = `player_teleport − 0x32`, ×16 adds,
enable/disable roundtrip) and `test_teleport_stub_is_stack_convention_agnostic`
(esp saved/restored via ebx, no `add esp` guess, ×16 present). 92 tests pass headless;
flake8 clean. Live: AOBs validated read-only against the running game (both unique, inject
bytes match, Teleport prologue at entry−0x32); a read-only diagnostic hook captured the
ping coords to prove the tile unit; the ×16-fixed stub was installed via the CLI path,
ground-truth-verified byte-for-byte, and the user confirmed pings land "spot on" with no
crash across many teleports.
