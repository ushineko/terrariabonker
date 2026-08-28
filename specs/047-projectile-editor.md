# Spec 047: A per-item projectile editor

**Status**: INCOMPLETE — implemented and tested headless; the in-game verification
criterion is outstanding and needs the maintainer at the keyboard.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Let a player change what the projectiles their weapon fires actually *do* — pass through
walls, pierce more enemies, fly faster, be bigger — chosen per weapon and applied while
they play.

## Context

Recon is finished and is recorded in `docs/item-fields.md`. Three findings decide this
spec's whole shape, and all three arrived late enough to have already cost an afternoon.

**Live projectile editing works, on measured evidence.** Forcing `tileCollide = 0` on
`BoneGloveProj` (532, which defaults to 1) against a slot-parity control in the same run:
57.5% of samples inside solid terrain versus 10.6%, and 3.4× the displacement per sample.
A projectile going through walls instead of stopping at them.

**Editing the template does nothing.** `ContentSamples.ProjectilesByType` is not consulted
by `Projectile.SetDefaults`, which assigns literals from its own `if (type == N)` chain. So
unlike the item editor — which edits a template and gets a permanent result — there is no
write that makes a projectile change stick. **Every field must be re-applied to each live
projectile, continuously.** That is the central constraint and it drives the design below.

**The offsets are no longer inferred.** `tools/monofields.py --verify` checks them against
the mono runtime's own field metadata, including widths: `tileCollide`, `friendly` and
`hostile` are single bytes packed against neighbours, so a 4-byte write corrupts the field
next door. The previous probe did exactly that, and read `active` at `Entity.wet` besides.

## Requirements

1. A player picks a weapon they own and sets overrides for the projectile it fires.
2. Overrides apply to projectiles already in flight and to every one spawned afterwards,
   for as long as the feature is enabled.
3. Turning it off restores nothing and needs to restore nothing — the next projectile the
   game spawns is a fresh object with the game's own defaults.
4. Overrides survive the weapon being unequipped and re-selected, and are remembered
   between sessions like the Effects panel is.
5. The editor never offers a field that can crash the game or turn the player's own
   projectiles against them.

## Design

**A poll loop, not an injected stub.** `Service` already owns tick loops of this shape
(`catch_tick`, `fishing_buff_tick`), and the probe sustained a 120 Hz sweep of all 1001
slots without trouble. An arena stub hooked into the projectile update would apply the
values a frame earlier and cost far more: the arena is the most delicate machinery in the
project, and this feature does not need it. Revisit only if the one-frame gap proves
visible.

**Keyed by projectile type, chosen by item.** The UI presents weapons, because that is how
a player thinks; `Item.shoot` (`0x0FC`) maps the chosen item to the projectile type that
the overrides are stored against. A weapon whose `shoot` is 0 fires no projectile and is
not offerable. Where several weapons share a projectile type, editing one edits both, and
the UI must say so rather than imply per-weapon isolation it does not have.

**Fields offered in v1**, all verified and all reversible by simply stopping:

| Field | Offset | Width | Why it is safe |
|---|---|---|---|
| `tileCollide` | `0x100` | bool | The proven case; worst outcome is a projectile leaving the world |
| `penetrate` | `0x0D4` | i32 | `-1` is the game's own value for infinite (`maxPenetrate` mirrors it) |
| `extraUpdates` | `0x104` | i32 | Ticks per frame — the game's own speed multiplier |
| `scale` | `0x08C` | f32 | Visual and hitbox size; the game varies it routinely |
| `timeLeft` | `0x0B4` | i32 | Lifetime in ticks |

`timeLeft` earns its place on evidence rather than symmetry. The Book of Skulls passes
through only a few tiles despite `tileCollide = 0`, because `AI_001` special-cases type 837
to subtract 33 ticks of life for every tick its centre is inside a solid tile. Terrain ages
that projectile rather than stopping it, so enforcing `timeLeft` — not `tileCollide` — is
what makes it cross a wall. See `docs/item-fields.md`.

**Fields deliberately excluded**, and the reason recorded so it is not re-litigated:

- `aiStyle` — reassigning behaviour makes the game run an AI against a projectile whose
  `ai[]` slots mean something else. This is the most likely way to crash a player's game.
- `hostile` / `friendly` — flipping these makes the player's own projectiles damage the
  player. A trainer that kills you is worse than one that does nothing.
- `damage` — already editable through the item editor, where it persists properly.

**Clamping is part of the write, not the UI.** Values are bounded at the point of writing
(`scale` to a sane range, `extraUpdates` to a small integer) so that a value typed into a
spin box cannot reach the game unbounded. The UI may also constrain; it may not be the
only thing that does.

## Acceptance criteria

- [x] A weapon can be given projectile overrides in the GUI, and the panel shows which
      projectile type it maps to, so a shared type is visible rather than surprising.
      *Projectiles tab; `Item.shoot` resolved per weapon, and weapons sharing a projectile
      are named in the label.*
- [x] Overrides apply to projectiles already in flight and to newly spawned ones, and stop
      applying when the feature is disabled. *A 50 ms tick sweeps the whole array;
      unticking stops it and tells the worker to forget per-projectile state.*
- [x] Only the five v1 fields are offered; `aiStyle`, `hostile`, `friendly` are not
      writable through this feature by any path. *The CLI parser refuses unknown and
      excluded names rather than dropping them; a test asserts they are absent.*
- [x] Values are clamped where they are written, and a test drives an out-of-range value
      through the write path rather than through the widget.
- [x] Overrides persist between sessions, alongside the existing GUI state. *The "Apply
      while I play" switch is deliberately NOT restored: overrides are a setting, but
      writing into a running game at launch is not something to start unasked.*
- [x] Bool fields are written one byte wide, with a test that asserts the neighbouring
      field is untouched — `reflected` sits at `0x0C9`, immediately after `hostile`.
- [x] Offsets used by this feature are covered by `tools/monofields.py --verify`, and
      pinned as literals in a test with their provenance.
- [ ] **In-game verification** (integration boundary — this writes to live game memory):
      a weapon that normally collides is given `tileCollide = 0` and observed passing
      through terrain, with the before-value recorded to prove it was not already 0.
- [x] Headless tests: overrides reach the right slots, non-matching types are untouched,
      disabling stops writes, and a projectile that dies mid-sweep does not raise.
      *33 tests across the sweep, the CLI/argv contract, persistence and the panel.
      Sixteen mutations run, fifteen caught; one equivalent, recorded below.*

## Findings

**One equivalent mutant, recorded rather than counted as a pass.** Removing the
`helper.available` guard from the panel's tick changes nothing observable: `request()`
already returns `False` when the worker is down, and the caller resets its in-flight flag
on that return. The guard is clarity, not behaviour.

**`_pj_select` exists because a test found the leak.** Setting the selected projectile type
and reloading the controls were two separate steps, so switching weapons left the previous
weapon's boxes ticked and the next edit wrote them onto the new projectile — with nothing
on screen to say so. They are one method now.

**A hazard the spec did not anticipate**, found while implementing and now in Design:
enforcing `timeLeft` every sweep stops projectiles ever expiring. The game allocates all
1001 slots up front, so a pinned lifetime fills the array and the player's weapons quietly
stop firing. `timeLeft` is applied once per projectile instead.

## Risks & Assumptions

- **Nothing here persists in the game.** Projectiles are transient and never saved, so the
  blast radius is a session. This is the safest cheat in the project by some margin, and
  it is the reason the fields can be offered at all.
- **A one-frame window before the first write.** A projectile acts on its own defaults for
  up to one poll interval before the override lands. For `tileCollide` this is invisible;
  for a field affecting spawn behaviour it might not be. Accepted, and revisit only on
  evidence.
- **Shared projectile types.** Several weapons shoot the same type. Handled by naming it
  in the UI, not by pretending otherwise.
- **`penetrate` and `maxPenetrate` travel together** (`0x0D4` / `0x0DC`); writing one and
  not the other may produce a projectile the game accounts for inconsistently. To settle
  during implementation by reading what `SetDefaults` does with the pair.
- **Multiplayer is out of scope.** These writes are local and would desync a server. The
  feature assumes single-player, as the rest of the project does.
- **Rollback**: `git revert`, or disable the toggle. No save-format change, no template
  write, no patch to game code.

## Alternatives considered

- **Patch `SetDefaults` in the arena** so projectiles are born with the overrides. Removes
  the one-frame gap and the poll cost entirely. Rejected for v1: it needs a hook in hot
  game code for a benefit currently measured at zero frames of visible difference.
- **Edit `ContentSamples.ProjectilesByType`.** Tried during recon; `SetDefaults` never
  reads it, so it changes nothing. Recorded in `docs/item-fields.md`.
- **Expose every field the metadata knows about.** Rejected: the metadata walker makes it
  trivial to offer all 118 `Projectile` fields, and most of them are a crash or a
  self-inflicted death. Verified-and-safe is a smaller set than verified.
