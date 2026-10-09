# Spec 052: Native Windows port

**Status**: DRAFT — phases 0 (recon) and 1 (platform layer) complete; phase 2 and phase 3 step 1 (the version table and its gate) done and checked live on both platforms. Phase 3 step 2 (the CLR read path) is under way: on Windows the live player's life, mana and inventory are read, the live copy picked by Main.player's statics; every write is still refused.

> **Note**: This work has no associated issue tracker ticket (personal utility).

Today the tool targets one configuration: Terraria 1.4.5.8's Windows build, run on Linux
under Proton, executed by **wine-mono**. This spec is a native Windows build of the same
tool against the same game build, executed by **.NET Framework**.

The question that decides the size of this work is whether the numbers the project has
derived — field offsets, Main statics, code-patch byte patterns — hold on Windows. The
game assembly is the same file; the runtime that lays out its objects and compiles its
code is not.

**Answer so far: no.** No field offset survives (measured), and no code-patch anchor
matches at the main menu (measured; not yet conclusive, see below). The approach
survives: locating by value, validating by name, and patching by byte pattern all work
on Windows, given numbers derived for the CLR.

## Context

### What is the same

- **The game build.** Steam build id `24893155`, version `1.4.5.8` (changelog header),
  `Terraria.exe` sha256 `960a03bf…6ba2f3`, 26,597,888 bytes. The Linux build key
  `1.4.5.8+24893155` is the same key.
- **Bitness.** `Terraria.exe` is PE32 (`machine 0x14C`) with CLR header flags `0x3`
  (`ILONLY | 32BITREQUIRED`). It runs as a 32-bit (WOW64) process on Windows as it does
  under Proton, so `uint32` addresses throughout the code stay correct. The image is
  `LARGE_ADDRESS_AWARE` (`0x20` in characteristics), so addresses above 2 GB occur.
- **Not NGEN'd.** No native image for Terraria in `C:\Windows\assembly\NativeImages_v4.0.30319_32`.
  Game methods are JIT-compiled at run time, so locating code by byte pattern in
  executable memory is still the right model.
- **IL.** `tools/ilrecon` reads the assembly and is runtime-independent; its findings
  (which method owns a behaviour, loop bounds, constants) carry over unchanged.

### What differs

| | Linux (today) | Windows |
| --- | --- | --- |
| Runtime | wine-mono (mono JIT, x86) | .NET Framework 4.8.1 (`Release 0x82405`): `clr.dll` + `clrjit.dll` 4.8.9345.0 from `Framework\v4.0.30319` — measured from the loaded modules |
| Object header | vtable, sync → fields at `+0x08` | MethodTable at `+0x00`, fields from `+0x04` — measured |
| Field ordering | mono auto layout | CLR auto layout: references first, then by size, bools last — measured; no offset survives |
| String | vtable, sync, length `+0x08`, chars `+0x0C` | MethodTable, length `+0x04`, chars `+0x08` — measured (player names decode) |
| szarray | length `+0x0C`, data `+0x10` | length `+0x04`, data `+0x08` — measured on `Item[]`, a reference-type array; there is no element type handle |
| Statics | one per-class static block (`MainStaticBase`) | offsets are known (below); where the blocks live is not yet measured |
| Code | mono JIT output | clrjit x86 output |
| Memory access | `/proc/<pid>/mem` under sudo | `OpenProcess` + `Read/WriteProcessMemory`, same user — `PROCESS_VM_READ` measured to work without elevation |
| Region map | `/proc/<pid>/maps` | `VirtualQueryEx` |
| Allocating the arena | springboard hook makes the game call `VirtualAlloc` | `VirtualAllocEx` from outside; the bootstrap is unnecessary |
| Recon tooling | Cheat Engine mono dissector under Wine, `cmd/monofields` | `cmd/clrfields` (this spec) |

## Phase 0 findings (2026-10-08)

Measured with two read-only tools against the running game, player at the main menu:

- `cmd/winrecon`: runtime modules, region sizes, player scan, anchor match counts.
- `cmd/clrfields`: the runtime's own field offsets by name. Full dump for Entity,
  Projectile, NPC, Item, Player and Main is in `docs/clr-fields-1.4.5.8.txt`.

### How the CLR's field table was read

The CLR keeps one 12-byte `FieldDesc` per stored field, contiguous per class. Measured
shape on this runtime:

| dword | contents |
| --- | --- |
| `+0` | enclosing MethodTable, **self-relative**: record address + this signed value |
| `+4` | field RID in the low 17 bits; bit 24 is static; access bits above |
| `+8` | offset in bits 0..26, `CorElementType` in bits 27..31 |

The first model tried treated `+0` as an absolute MethodTable pointer and found only
noise. A search that assumed nothing but "RIDs appear in order" found the real records,
and showed the self-relative encoding (three consecutive records holding `0x24`, `0x18`,
`0x0C`, all resolving to one address).

Field names come from the assembly's metadata by RID (`cmd/clrfields/metadata.go`). A
class's list is accepted only when its record count equals the class's stored
(non-literal) field count from the metadata. Exactly one such list exists per class,
and every record in each agrees with the metadata's static flag:

| Class | Stored fields | Records | Static flag agrees | MethodTable (this process) |
| --- | --- | --- | --- | --- |
| Entity | 14 | 14 | 14/14 | `0x084fbf1c` |
| Projectile | 113 | 113 | 113/113 | `0x085309ac` |
| NPC | 308 | 308 | 308/308 | `0x084fe6f0` |
| Item | 138 | 138 | 138/138 | `0x085323fc` |
| Player | 1195 | 1195 | 1195/1195 | `0x0853aa94` |
| Main | 1129 | 1129 | 1129/1129 | `0x08279950` |

Element types also match the declarations (`0x08` I4 for the life ints, `0x0C` R4 for
`pickSpeed`, `0x12` class for `name`/`inventory`, `0x02` bool for `active`).

**Independent confirmation.** `winrecon` scanned for the life/mana block in CLR order and
checked each hit's object start (`statLife − 0x470`) against the Player MethodTable
`clrfields` reported. All seven of the maintainer's characters on the selection screen
matched, with names decoding at `+0x08C` and a 59-slot `Item[]` at `+0x0D4`. Separately,
a blind sweep for name-shaped string pointers near every life block ranked
`statLife − 0x3E4` first — the distance `clrfields` predicts — without being told where
to look.

### Player (instance, object-relative)

| Field | CLR offset | from statLife (CLR) | from statLife (mono) |
| --- | --- | --- | --- |
| name | `0x08C` | `-0x3E4` | `-0x6C0` |
| inventory | `0x0D4` | `-0x39C` | `-0x664` |
| statLifeMax | `0x468` | `-0x08` | `-0x08` |
| statLifeMax2 | `0x46C` | `-0x04` | `-0x04` |
| statLife | `0x470` | `0` | `0` |
| statMana / ManaMax / ManaMax2 | `0x474` / `0x478` / `0x47C` | `+0x04..+0x0C` | same |
| pickSpeed | `0x514` | `+0xA4` | `+0x1A0` |
| wallSpeed / tileSpeed | `0x518` / `0x51C` | `+0xA8` / `+0xAC` | — |
| blockRange | `0x570` | `+0x100` | `+0x2C0` |

**Correction (2026-10-08, later the same day): the life caps are in the same order on
both runtimes.** This section first said `statLifeMax` and `statLifeMax2` were in the
opposite order from mono. They are not: `cmd/monofields` against the live Linux game
reports `statLifeMax` 0x730 and `statLifeMax2` 0x734, statLife 0x738 -- the permanent cap
first, as on the CLR and in the metadata's declaration order. The mono *names* in
`internal/layout` had been swapped since they were first written, inferred rather than
asked; the comments beside them were right. Comparing the CLR's runtime-reported names
with those swapped names is what produced the "opposite order". The consequence on Linux
was a bug: `locate.ValidBlock`, trusting the names, took the permanent cap for the boosted
one, so a player whose cap in effect was above the permanent one (Lifeforce, max-life
accessories) was never found. Fixed with the names (see the life-cap fix's validation
report); `cmd/monofields --verify` now checks all six fields.

There is no `selectedItem` field in 1.4.5.8's metadata on either runtime's view of the
assembly — the closest is `selectedItemState` (`0x9F0`, a struct). Mono's
`SelectedItemOff` was found by watching values, not by name, so the CLR equivalent has to
be found the same way or by reading what `selectedItemState` holds.

### Projectile, Item, NPC (instance, object-relative)

| Field | CLR | mono (`cmd/monofields`) |
| --- | --- | --- |
| Entity.whoAmI | `0x004` | `0x008` |
| Entity.position | `0x020` | `0x00C` |
| Entity.wet | `0x018` | `0x03C` |
| Projectile.type | `0x080` | `0x094` |
| Projectile.active | `0x102` | `0x078` |
| Projectile.hostile | `0x109` | `0x0C8` |
| Projectile.tileCollide | `0x112` | `0x100` |
| Projectile.timeLeft | `0x09C` | `0x0B4` |
| Item.type / stack / prefix | `0x050` / `0x064` / `0x12E` | see `internal/layout/item.go` |
| NPC.type / active / netID | `0x0F4` / `0x18C` / `0x14C` | see `internal/layout/npc.go` |

None coincide. On the CLR, bools are packed at the end of each class, which is why
`active` and `hostile` moved furthest.

### Main (static)

`clrfields` reports static offsets as the FieldDesc stores them: `player 0x878`,
`npc 0x824`, `projectile 0x830`, `tile 0x810`, `recipe 0x870`, `worldName 0x5F8`,
`npcFrameCount 0x918` (references, `0x5F8..0x918`); `maxTilesX/Y 0x1228/0x122C`,
`myPlayer 0x1384`, `netMode 0x1414`, `gamePaused 0x160A`, `gameMenu 0x1656`
(primitives). On .NET Framework, reference and primitive statics are expected to live in
two different blocks. Measured in a world:

- **Reference statics: located.** The live player object is element 0 of a `Player[256]`.
  Exactly one slot holding that array has a base (`slot − 0x878`, `0x069e5660` in this
  process) from which `+0x824` reaches an `NPC[201]` and `+0x830` a `Projectile[1001]`.
  Those are three independent arrays whose lengths are the game's own limits (255+1,
  200+1, 1000+1). The other two holders of a 256-player array reached empty arrays.
- **Primitive statics: located.** The world's `4200×1200` appears as a
  `maxTilesX/maxTilesY` pair at two bases (`0x07f05c98`, `0x07f0632c`), both reading
  `myPlayer = 0` and `gameMenu = 0` — a tie. A first sample of `time` (`+0x1058`) read
  `11209` at the first and `0` at the second, but neither moved: the game was paused
  (window unfocused), so that was plausibility, not liveness. Re-measured with the game
  focused: `time` at the first base advanced by 60 per second (`15844 → 15904`,
  `15909 → 15969`, `15976 → 16036`), the game's tick rate; the second stayed `0`. The
  primitive base is the first (`0x07f05c98` in this process). A third pair at
  `0x39f213fc` reads `myPlayer = 10`, which a single-player game rules out.
- The two blocks are about 21 MB apart in this process, so on the CLR "Main's static
  base" is two bases, located separately and validated separately.
- How to find either base without a live player object to start from (what
  `FindLocalPlayerAnchor` does on mono from JIT'd code) is phase 3 work.

### Code-patch anchors

All 17 anchors in `internal/patch/anchors.go` match **zero** times in executable memory,
both at the main menu and **in a world**. In the world, the CLR's compiled code is
there: 16 sites write `mov dword [reg+0x570], 0` (`blockRange`'s CLR offset; one sits in
a run of adjacent field clears, the shape a `ResetEffects` candidate would have) and 19
store or load `[reg+0x514]` (`pickSpeed`). The base register is `edi` at some sites and
`esi` or `ebx` at others, so mono's `edi = this` cannot be assumed. Every anchor fails
safe ("anchor not found"), which is the behaviour the project already requires of a
moved layout. Code patches need re-deriving cheat by cheat (phase 4).

### Build and test state on Windows (go1.27.0 windows/amd64)

`go vet` with `GOOS=linux CGO_ENABLED=0` over the non-GUI packages passes. On Windows:

- Does not compile: `internal/builds`, `internal/profile`, `internal/patch/state.go`,
  `internal/gui/single.go` (`syscall.Flock`); `internal/version` (`inodeOf`/`stampOf`
  exist only in `stat_linux.go`). Everything importing these fails with them: `cli`,
  `service`, `recipes`, `sprites`, both commands.
- Compiles and passes: `buffs`, `content`, `game`, `inventory`, `layout`, `locate`,
  `memtest`, `player`, `projectile`, `selling`, `tiles`, `trainer`, `xnb`.
- Fails: `internal/proc` `TestARealListingParsesIntoScannableRegions` (a `/proc/maps`
  fixture); `cmd/prefixstats` `TestTheToolReproducesTheCheckedInTable` — the checkout has
  `core.autocrlf=true`, so a checked-in JSON file arrives with CRLF and the byte comparison
  fails. A `.gitattributes` marking data files `-text`/`eol=lf` fixes the class.

## Design

### The privilege boundary on Windows

The game runs as the user; a same-user, same-integrity process can open it with
`PROCESS_VM_READ | PROCESS_VM_WRITE | PROCESS_VM_OPERATION | PROCESS_QUERY_INFORMATION`
without elevation (read access measured; write access to be confirmed in phase 1). The
rule "the GUI does not run as root" has nothing to guard on Windows. The GUI still
reaches memory only through the CLI (`serve` worker or one-shot): that keeps one code
path, one JSON contract, and the AST-enforced rule that JSON parsing lives only in
`internal/gui/client`. `proc.Elevate` becomes a no-op on Windows.

### Table-driven version support

Phase 0 settles what a "supported version" is: **a game build *and* the runtime
executing it**. The same `Terraria.exe` produced a different layout for every class
measured, and different machine code, purely because the runtime changed. Mono versions
can differ from each other the same way: a wine-mono update can change JIT output, and
in principle field layout, with the game untouched. `DetectRuntime`'s docstring already
says this about code patches; the data offsets are no different.

So version support becomes a table. Each supported combination with its own numbers is
one entry, and the running combination is matched against the table at startup.

**The key** is `(game build key, runtime)`:

| Part | Source | Example |
| --- | --- | --- |
| Game build key | `version.BuildKey` (version + Steam build id), as today | `1.4.5.8+24893155` |
| Runtime family | which runtime module is loaded | `wine-mono`, `netfx` |
| Runtime version | mono: the `wine-mono-X.Y.Z` path (today's `DetectRuntime`); .NET Framework: `clr.dll`'s file version | `wine-mono-<maintainer's>`, `netfx-4.8.9345.0` |

**An entry** carries everything that varies with the key, and nothing that does not:

- Object shapes: header size, string length/chars offsets, szarray length/data offsets.
- The life/mana block's field order, which `locate.ValidBlock` reads (opposite on the
  two runtimes).
- Every field offset now in `internal/layout` (Player, Item, NPC, Projectile, Entity,
  buffs, selling).
- How Main's statics are found (a locator strategy plus its offsets; mono uses one
  static block, the CLR at least two).
- Code-patch anchors, patch bytes, stub templates and the field displacements embedded
  in them. This absorbs today's `Anchor.Variants`, which are keyed by build only.
- Provenance: which tool produced the numbers (`monofields`, `clrfields`, CE) and when.
- The verification ledger: the cheats the maintainer confirmed in play on this key.

**Several keys may share one entry** when they are measured to have identical numbers —
the 24825745 → 24893155 rebuild that left seven of nine anchors matching is the
precedent. Sharing is recorded as an explicit alias after a measurement, never
inferred. A combination that differs in anything gets its own entry; entries are
appended and never edited in place to suit a newer build (the accumulation rule in
`anchors.go`, applied to the whole table).

**Matching at startup:**

1. Detect the game build key and the runtime, as today.
2. **Exact match** → use that entry. Report it as supported.
3. **Same runtime family, no exact match** (a new wine-mono, a .NET Framework servicing
   update, a Steam rebuild) → take the nearest entry of that family as a *candidate*,
   and run it through the existing degraded-build flow: probe every anchor, validate the
   player locate by name, and let the user accept or decline. This is the "ledger, not a
   gate" rule kept as it is. A runtime self-check (`monofields --verify`/`clrfields
   --verify` against the candidate's offsets) is the stronger test, offered as the way
   to promote a candidate to an entry.
4. **Different runtime family** → never used. Phase 0 is the measurement: a mono entry
   on the CLR misses every offset. Without an entry for the family, memory features are
   disabled with "unsupported runtime", rather than run on numbers known not to apply.

The selected entry is fixed for the life of the process (a game update needs a restart
anyway). The common layer receives the entry. No package reads an offset from anywhere
else.

**What changes in code:**

- `internal/layout` keeps sole ownership of the numbers ("declared once" still holds:
  once per entry). Its package-level constants become fields of an entry value; the mono
  entry is today's constants, unchanged, and the CLR entry is the `clrfields` output.
  Packages that read a constant directly today (`locate`, `inventory`, `player`,
  `projectile`, `patch`, …) take the entry instead. Mechanical, but it touches every
  package that reads memory, so it gets characterization tests first (AGENTS.md,
  "Refactors are pinned before they start").
- Each entry is pinned in a test as literals, with its provenance in the docstring, and a
  `sha256` of the whole entry is frozen. A transcribed copy gets edited to match; a
  digest does not.
- `internal/builds.Decision` (the user's accept/degrade choice) is already keyed by
  build and records runtime beside it. It is re-keyed by the full `(build, runtime)` key,
  reading old records as "runtime unknown", which is what they are.
- `version.DetectRuntime` gains a Windows implementation that reads `clr.dll`'s version
  from the loaded module list, and reports the family explicitly.

**The first mono entry's key is not recorded anywhere.** Checked on the maintainer's
Linux machine, 2026-10-08:

- `accepted-builds.json` holds `1.4.5.8+24893155: accepted` with **no `runtime`** — the
  decision predates runtime tracking.
- Terraria (app 105600) is mapped to `proton_experimental`, the bleeding-edge branch
  (`experimental-bleeding-edge-11.0-446795-20261005`), which ships **wine-mono 11.3.0**.
  That build is dated 2026-10-05, after the mono numbers were derived (August and
  September), so it is not evidence of what they were verified under.
- Three wine-mono versions are installed for the same game build: 10.4.1
  (GE-Proton10-29), 11.2.0 (proton-cachyos 11.0-20260703), 11.3.0 (Proton Experimental
  and Hotfix). Each would be a distinct key.

So the first entry is keyed by measurement during phase 3: run the existing Linux build
against the game under wine-mono 11.3.0, and record that key only if the read path and
every cheat still resolve. Other wine-mono versions get entries only when they are run
and checked the same way. Until then they fall to the "same family, candidate" path.

CLR numbers are pinned as literals in a test with `clrfields` as their provenance, the
same rule as the mono ones.

### Platform layer

Split by build tags (`_linux.go` / `_windows.go`). Landed in phase 1:

- `proc`: the region types and `/proc/maps` parsers stay common; the `/proc` half moved
  verbatim to `proc_linux.go`. `proc_windows.go`: `Mem` over a process handle opened
  once per `Mem`; `Regions` (committed, writable), `ExecRegions` (committed, executable,
  **private** — JIT code is private memory, so the executable sections of mapped DLLs
  are left out of an anchor search), `AllRegions` (committed or reserved, for gap
  finding) from `VirtualQueryEx`; `Read` retries once clamped to the region end, because
  `ReadProcessMemory` fails a whole read that crosses into an unreadable page where
  `/proc/<pid>/mem` returns it short; `Write` flushes the instruction cache;
  `FindPID` from a Toolhelp snapshot; `ExePath` from `QueryFullProcessImageName`;
  `ModulePaths` for runtime detection. `Elevate` is a no-op on Windows.
- `proc.Alive` replaces `cli`'s `/proc/<pid>` stat. On Windows it checks the exit code:
  a process object outlives its process while any handle to it is open.
- `internal/filelock`: one `Lock`/`TryLock` replacing four copies of `syscall.Flock`
  (`flock` on Linux; `LockFileEx` on Windows, over one byte past any real file's end so
  the GUI's lock file stays readable for the "already open (pid N)" message).
- `internal/paths`: one `ConfigDir`/`CacheDir` replacing eight hand-spelled
  `home/.config/terrariabonker` and `home/.cache/terrariabonker`. Linux keeps exactly
  the old paths — deliberately not `os.UserConfigDir`, which honours `XDG_CONFIG_HOME`
  and would move existing state. Windows: `%APPDATA%\terrariabonker` and
  `%LOCALAPPDATA%\terrariabonker`.
- `version`: `mappingIsCurrent` per platform (Linux keeps the inode check; Windows
  cannot overwrite a running image, so the check is true there, with the rename gap
  documented); Windows `stampOf` uses the NTFS file index; Windows `DetectRuntime` reads
  `clr.dll`'s file version from the module list (`netfx-4.8.9345.0` on the live game).
- Catalog cache filenames: Windows refuses `?`, which is what an undetected half of a
  build key reads as. Reserved characters are replaced on Windows only, so Linux caches
  keep their names.
- GUI single-instance lock: no uid in the name on Windows (`Getuid` is -1 and the temp
  directory is already per-user).
- `.gitattributes`: text checked out with LF everywhere (`* text=auto eol=lf`).
- Dependencies: `golang.org/x/sys/windows` v0.48.0 direct (approved 2026-10-08);
  `golang.org/x/net` v0.59.0 → v0.60.0, indirect via fyne, for GO-2026-6610..6617
  (not called by this code; govulncheck now reports none).

Deferred, because nothing in phase 1 needs it:

- `patch` arena via `VirtualAllocEx(PAGE_EXECUTE_READWRITE)`; the springboard bootstrap
  stays Linux-only. Phase 4, with the first Windows code patch.
- Steam library discovery from `libraryfolders.vdf` (sprite extraction finds the
  install through the running game today). Phase 5.
- `cmd/monofields` compiles on Windows and fails at run time reading `/proc/<pid>/maps`;
  `cmd/clrfields` is its Windows counterpart.
- Not ported: KWin rules, `install.sh`, `tools/screenshot.sh`, `.desktop` file.

### Phase 1 findings

- **Test isolation leaked on Windows.** Tests isolated config by setting `HOME`, which
  Windows ignores (`os.UserHomeDir` reads `USERPROFILE`; config and cache come from
  `APPDATA`/`LOCALAPPDATA`). The first full run wrote `accepted-builds.json`,
  `patches.json`, `profile.json` and a sprite cache into the developer's real
  `%APPDATA%`/`%LOCALAPPDATA%`; patch's `TestTheTestsNeverTouchTheRealState` caught it.
  The files were removed (the directories did not exist before the run). Fixed with
  `memtest.IsolateHome`, which sets all four variables, and `memtest.ConfigUnder`/
  `CacheUnder`, which spell each platform's expected layout as literals. Re-run with the
  real directories redirected to a trap: nothing of this program's landed there.
- **The build gate calls an unverified runtime "known good".** `build-check` against the
  live Windows game reports `1.4.5.8+24893155 (exact)`, `known-good: true`, with runtime
  `netfx-4.8.9345.0` and every cheat unresolved. The gate keys on the build only. Nothing
  mis-writes (the player locate finds no match under mono offsets, measured in phase 0,
  and every anchor fails), but the claim is wrong. Phase 3's `(build, runtime)` key fixes
  it; until then a Windows build must not ship.
- `TestNothingIsRaisedIfTheOriginalCannotBeRecorded` made the profile directory
  unwritable with `chmod 0500`, which Windows ignores for directories. It now puts a
  plain file where the directory should be, which fails on both platforms.

## Phases

0. **Recon** (this document). Read-only tools `cmd/winrecon` and `cmd/clrfields`. No
   writes to the game.
1. **Platform layer.** Everything compiles and `make test` passes on Windows headless;
   the Linux build is unchanged. `.gitattributes` for data files.
2. **CLR field-offset tool.** `cmd/clrfields` exists from phase 0; this phase adds
   `--verify` against `internal/layout`'s CLR table, as `monofields --verify` does for
   mono. Chosen over Cheat Engine's .NET dissector (maintainer, 2026-10-08): it keeps
   recon in the repo's toolchain and needs nothing installed beside the game.
3. **Version table, then the read path on CLR.** First, the table with one entry, today's
   mono numbers, and startup matching, with no behaviour change on Linux. That step is
   useful on its own: it is also how a wine-mono update would be handled. Then the CLR
   entry: player locate (CLR name offset), inventory, player stats,
   NPC/projectile reads. Main statics located per the phase 0 measurement.
4. **Code-patch cheats on CLR.** Re-derive each anchor from the clrjit output, cheat by
   cheat, as variants. Each cheat is confirmed in play by the maintainer before its anchor
   is marked verified for `1.4.5.8+24893155` on `netfx-4.8.1`. Stubs are re-checked for
   register assumptions (mono's `edi = this` is not a CLR guarantee) and every embedded
   field displacement is replaced with the CLR offset.
5. **GUI and packaging.** Panel on Windows (fyne GL with cgo), a zip release, README
   requirements for Windows.

Phase 4 is the large one and can ship incrementally: a Windows build with the read path
and value edits is useful before any code patch works, provided each unresolved cheat is
disabled with its reason (the existing degraded-build mechanism).

## Acceptance criteria

Phase 0
- [x] Runtime executing the game on Windows identified from its loaded modules.
- [x] Whether `locate.ValidBlock` finds the player on Windows, measured (only when the
      two life caps are equal -- a bug on both runtimes, from swapped mono names; see the
      correction under "Player").
- [x] Distance from statLife to the player name pointer and inventory `Item[]` pointer
      on CLR, measured (`-0x3E4`, `-0x39C`), and compared with the mono values
      (`-0x6C0`, `-0x664`).
- [x] Object header, string and array shapes measured.
- [x] Field offsets by name for Entity, Projectile, NPC, Item, Player, Main, from the
      runtime, cross-checked against the metadata and against the player scan.
- [x] Each code-patch anchor's match count on Windows, measured in a world (zero for all 17).
- [x] Main statics: reference block located and validated by three arrays.
- [x] Main statics: primitive block confirmed live (`Main.time` advancing at 60/s).
- [x] Findings recorded in `docs/discovery.md`.

Phase 1
- [x] `go build ./...` and `go test ./...` pass on Windows without the game running
      (27 packages, GUI included — cgo with MSYS2 gcc).
- [x] `make test` and `make lint` still pass on Linux (27 packages; 0 issues). The
      Windows-only files also lint clean with `GOOS=windows`.
- [x] No behaviour change on Linux: the `/proc` code moved verbatim, config and cache
      paths are byte-identical (pinned as literals in `paths_test`, with `XDG_*` set to
      prove they are ignored), `flock` semantics unchanged, cache filenames unchanged.
- [x] The Windows CLI identifies the live game: build from the executable, runtime from
      `clr.dll` (`build-check`, read-only).

Phase 2 (CLR field-offset tool)
- [x] The CLR offsets live once, in `internal/layout` (`CLRFields`, by declaring class),
      generated from the measurement and checked against `docs/clr-fields-1.4.5.8.txt`
      field for field, with a frozen `sha256`.
- [x] `clrfields --verify` checks that table against a running game and exits non-zero
      on any disagreement, a missing field, or a class without exactly one complete
      FieldDesc list.
- [x] `clrfields --verify` against the live game (a fresh process, not the phase 0
      one): 103 of 103 fields agree, exit 0. Built with one offset deliberately wrong
      (`statLife` 0x474), it exits 1 naming the field.

Phase 3, step 1 (the version table and the gate)
- [x] Version support is a table keyed by `(game build key, runtime)`
      (`layout.Entries`, `layout.Select`): a mono entry and a CLR entry, each with its
      shapes, builds, runtime versions and provenance, pinned as literals and by a
      frozen `sha256`.
- [x] Startup matching: exact → `supported`; same family, other build or runtime
      version → `candidate`, through the existing degraded-build flow; a family with no
      enabled entry → `unsupported`, memory writes refused even with `--force`;
      undetected → `unknown`, let through as an unreadable game version is.
- [x] `build-check` no longer reports a build as known-good or recognised under a
      runtime no enabled entry covers; it reports `support` and `runtime_entry`
      (additive JSON). `accept-build` refuses. The window marks every cheat unusable and
      asks nothing.
- [x] Windows runtime detection fails closed: a loaded `clr.dll` whose version cannot be
      read reports `netfx-unknown` (refused), not "nothing detected" (let through).
- [x] Linux behaviour unchanged: the mono entry accepts any wine-mono, as before.
- [x] Live check on Windows: `build-check` reports `netfx-4.8.9345.0 (unsupported)`,
      recognised and known-good false; `set-hp max --force` and `accept-build` are
      refused with the runtime named; nothing was written.
- [x] The mono entry's runtime measured on Linux, 2026-10-08: under wine-mono 11.3.0
      (Proton Experimental) in a world, both the installed v0.42.0 and this branch found
      the player by name, read stats and inventory, and resolved all 15 cheat anchors
      (at the menu, `pickup` and `spawn_rate` had not been JIT-compiled yet and did not).
      Recorded in `monoEntry.Confirmed` as a ledger, not a gate: `Versions` stays nil,
      so GE-Proton's 10.4.1 and proton-cachyos's 11.2.0 still match as before. The
      maintainer then confirmed the cheats still work in play under it. This branch's `build-check` there reported
      `support: supported`, `runtime_entry: wine-mono`, known and recognised unchanged.

Phase 3, step 2 (the CLR read path) — in progress, one feature at a time

The CLR entry cannot simply be "enabled" when its read path works: enabling it
would open the write gate for every command, including ones whose packages still
use mono offsets. So an entry declares what it can read (`Reads`, a list of
features) separately from whether it may write (`Writes`). A reader checks its
feature; a write checks `Writes`. The CLR entry's reads grow slice by slice, and
`Writes` stays false until every write path takes its numbers from the entry.

Slice 1 — the player (done):
- [x] `layout.Entry` gains `Reads`, `Writes` and `Player` (name and inventory
      offsets from statLife). Mono: every feature, writes. CLR: `ReadPlayer` only,
      no writes; its player fields checked equal to `CLRFields`' own differences.
- [x] `locate.With(entry)`: a locator with the entry's name offset, string shape and
      life-block order. The package-level `FindPlayers`/`ReadBlock`/`ReadMonoString`
      are the mono locator, so every existing call and test is unchanged; the
      duplicate `locate.NameOffset` (a second spelling of `layout.NamePtrOff`) is gone.
- [x] The service finds players with the entry's locator, skips the mono ground-truth
      anchor (`ReadLocalPlayer`) and the inventory-count fallback where the entry
      cannot read them, and leaves inventory out of a snapshot it cannot read.
- [x] Reads are gated per command: `status` needs `ReadPlayer`, `inventory`
      `ReadInventory`; `compendium` and `vein`, which walk many structures, need an
      entry that may write. `build-check`, `accept-build`, `version` and the raw
      `read ADDR` need none. The window's worker runs the same commands.
- [x] An undetected runtime selects the mono entry (the legacy behaviour); a
      detected runtime of an unknown family is unsupported.
- [x] Live on Windows: `status` reads "terrariabonker", HP 470/470, mana 200/200
      through the CLR path; `inventory` refuses naming the runtime; `build-check`
      says `supported, read-only`. Seven copies are found — the seven characters on
      the selection screen — and with the game paused, which copy is live is a
      guess (the first found) until slice 2's ground truth.

Next slices:
Slice 2 — ground truth on the CLR (done):
- [x] `layout.Entry.LocalPlayer` says how an entry finds the live copy: `ByAnchor`
      (mono's get_LocalPlayer JIT code) or `ByStatics` (the CLR). The CLR entry's
      `MainStatics` are differences of `CLRFields` (NPC and projectile slots −0x54
      and −0x48 from Main.player's; `Player.active` 0x70E, now pinned in the table)
      and the measured array lengths 256 / 201 / 1001.
- [x] `Locator.FindPlayerSlot`: from the copies a scan found, the `Player[]` that
      holds one, then the one static slot that holds it with an `NPC[201]` and a
      `Projectile[1001]` beside it. None or several is no answer.
      `Locator.LiveAt`: re-validates the slot on every call, then the array's one
      active element; two active is no answer. The service keeps the slot, not the
      players, and re-reads through it.
- [x] Live on Windows: seven copies, two of them "terrariabonker" (the live player
      and the selection-screen copy); one Main.player slot found; the active element
      is the copy at 0x5769DD54, the live one. Finding the slot took 8.5 s; a range
      prefilter in the scan brought it to 3.0 s with the same answer. Reads through
      the kept slot after that are effectively free.
- Equivalent mutant: the `ByStatics` guard in `FindPlayerSlot` changes no answer for
  the mono entry, whose zero statics make the search find nothing anyway. Defensive,
  recorded rather than tested.
Slice 3 — inventory on the CLR (done):
- [x] `layout.Entry.Item` (26 item fields; mono from its constants, CLR from
      `CLRFields` by name, checked) and `Player.SelectedItemFromLife` (mono −0x694;
      the CLR has no `selectedItem` field, so 0, "unmeasured").
- [x] `inventory.NewFor(entry, …)`; `inventory.New` is the mono inventory, unchanged.
      The passive-potion span is computed from the entry: under mono its four fields
      lie between favorited and buffType, under the CLR between stack and
      consumable. `SelectedSlot` says "cannot tell" without a measurement rather than
      read statLife. The modifier field table became a function of the item fields;
      `PrefixBaseFields`, read only by the still-mono template scan, stays at mono's.
- [x] The service builds every inventory through the selected entry; the CLR entry
      reads `ReadInventory`; `status` and `inventory` show items under the CLR.
- [x] Live on Windows: the inventory of "terrariabonker" read through the CLR
      offsets. Slots 0–3 match, field for field, what v0.42.0 read from the same
      character under wine-mono on Linux the same day (757 dmg 85, 1265 dmg 34,
      3473 dmg 105, 1294 dmg 39 useTime 5 pick 210) — two runtimes, two sets of
      offsets, one answer.
- Equivalent under today's table: gating `inventory` on `ReadPlayer` instead of
  `ReadInventory` changes nothing while every entry that reads one reads the other.

How the mono and CLR layouts relate (measured; useful when deriving the next
readers): field sizes are identical (the same assembly), and **the 4-byte fields keep
their order**, shifted by an offset that grows in steps wherever mono interleaved a
byte field or a reference that the CLR moved elsewhere. Item: fishingPole/bait
−0x10, type −0x1C, then useAnimation through healMana (eleven fields) all −0x24,
scale/defense −0x30, rare/shoot/shootSpeed −0x40, mana −0x48, buffType −0x54,
crit −0x64. Projectile: type/alpha −0x14, aiStyle/timeLeft/damage −0x18. Bools and
bytes do not follow: the CLR packs them at the end of each class (Projectile.active
0x078 → 0x102, Item.prefix 0x15C → 0x12E). A shift predicts a candidate; only
`clrfields` settles it.
Slice 4 — player-stat writes on the CLR (done):
- [x] Writes are gated per feature like reads: `Entry.WriteFeatures` and `CanWrite`;
      `Service.RequireWritable(feature, force)`; the CLI's `gameWriting`. The CLR
      entry allows `WritePlayerStats` only; every other write still needs an entry
      that may make every write (`Writes`), so the CLR refuses them.
- [x] `player.NewFor(entry, …)`, with the life and mana offsets on the entry. (After
      the life-cap correction the six fields sit at the same offsets on both
      runtimes, so the mono and CLR handles write the same bytes -- an equivalent
      mutant, recorded; the field stays for a runtime that differs.)
- [x] **Writes go to the live character only.** Every write used to go to every copy
      found. Under the CLR that is every character on the selection screen -- seven on
      the maintainer's game -- and a raised max HP there changes a save that may be
      played later. Now: with ground truth, the copies named as the live player is;
      without it, all copies only if they share one name; otherwise a refusal. On
      Linux, where the copies are one character's, nothing changes (live: `set-hp max`
      at full health went through, two copies, ground truth from the mono anchor).
- [x] Live on Windows: `set-hp 100` on the maintainer's game. Of seven copies in memory,
      the two of "terrariabonker" went 470 → 100 and the five other characters were
      unchanged; the maintainer saw 100/470 on the health bar. `set-hp max` put both
      back to 470.

Slice 5 — item-field edits on the CLR (done):
- [x] Write features `WriteItemFields` (in place: stack, damage, use speed, pick,
      reach, auto-reuse; the item keeps its type) and `WriteItemTemplates` (a new type
      or a modifier, which copy from the game's pristine template). The CLR entry has
      the first. `set-stack`, `set-item`, `fast-mining` and `long-reach` gate on it;
      `give` needs an entry that may make every write.
- [x] `SetItem` refuses a type change or a modifier where templates cannot be
      written, *before* writing anything: the template block is put down at mono's
      `CopyLo`, and at another runtime's offsets it would overwrite the item's fields.
- [x] Live on Windows: torches in slot 19 2578 → 2000 and the Terra Blade's damage
      85 → 200, read in every copy before and after -- the two copies of
      "terrariabonker" changed, the five other characters did not. The maintainer saw
      both in-game; both were put back.
- Open: `set-item` records the edit in the profile for auto-restore, and `restore`
  is still refused under the CLR -- it re-applies cheats as well as item edits -- so
  on Windows an edit is recorded and not yet re-applied on the next launch. The item
  half of `restore` now has everything it needs; the cheat half is phase 4.

Slice 6 — item templates on the CLR (done):
- [x] `ItemFields.CopyLo/CopyHi`, the span a type change copies from the pristine
      template. CLR: 0x18..0x144 -- past the five reference fields at 0x04..0x17,
      to the end of the object (the Item MethodTable's BaseSize 0x148 on the live
      game, less the sync block). Tested against the measured layout: no reference
      field in the span, which starts exactly after the last one. `Item` has no
      `Entity` fields in this build, under either runtime (its own fields start at
      the first word after the header).
- [x] The template scan, block copy, placement and the modifier's base stats
      (`inventory.PrefixBaseFieldsFor`) read the entry. The CLR entry writes
      templates; `give` gates on it.
- [x] Live on Windows: the scan found the game's pristine copies (low in the heap,
      prefix 0, base stats), not the player's own items. `set-item 0 757 --prefix
      81` made a Legendary Terra Blade, damage 85 → 98; `give 2 --stack 10` put 10
      Dirt Blocks in the first empty slot, with a placeable block's real stats (auto-
      reuse, use time 10). The maintainer confirmed both in-game; only the live
      character's copies changed. Restoring the gift was refused by `--expect-type`
      once the maintainer had moved the stack -- the guard working -- and it was
      cleared from its new slot.

Slice 7 — in-place cheats as entry data; mining and reach on the CLR:
- [x] Every in-place cheat's site is data on its entry (`layout.CheatSite`: anchor,
      patch offset, original and patched bytes, or a named encoder), with its anchors
      (`layout.AnchorDef`) and the player fields it sets (`Entry.PlayerValues`). The
      mono entry's are the mono tables, moved: their patterns, ledgers, offsets, bytes,
      encoder output and value fields print to the same SHA-256 as before the move
      (`TestTheMonoCheatsAreWhatTheyWere`).
- [x] `patch.Cheat` is what does not vary by runtime (name, label, value field, on and
      off values). The patcher takes an entry (`UseEntry`); the service equips it with
      the selected entry and the live character's copies as the targets of a cheat's
      value (`Service.Equip`), the same targets as every other write.
- [x] Each cheat is a write feature of its own (`layout.CheatFeature`). The CLR entry
      allows `mining` and `reach`; a cheat it has no site for reads "has no code site
      under netfx-4.8.1 yet" in `patch status`, and enabling it is refused with the game
      untouched.
- [x] CLR sites, from `ResetEffects` on the live game: the `reset_block` anchor; reach
      NOPs `mov [esi+0x570], edx` (blockRange, statLife + 0x100); mining replaces
      `fstp dword [esi+0x514]` (pickSpeed, statLife + 0xA4) with `fstp st0` and NOPs. A
      test checks each site's original instruction stores to the field its value is
      written to, under both entries.
- [x] Code that differs by runtime is a module selected by name, not a branch: the live
      player's finder (`locate.LiveFinder`: mono's get_LocalPlayer tail, the CLR's Main
      statics) replaces the `ByStatics` branch in `resolveLive`; the stub-based cheats
      are the injection set an entry names; encoders are a registry. `layout.Derive`
      makes a variant entry from a base without sharing its maps or lists. Recorded as a
      rule in `AGENTS.md`.
- [x] Build keys are declared once (`layout.Build1458s24893155` and 1.4.5.7's `Build1457s24825745`)
      and every table names the constant; a test fails on any key in the version table
      or an anchor ledger that is not a declared one. `1.4.5.7+24893155`, a key that
      named no real build (1.4.5.7's version with 1.4.5.8's build id, from a version
      detector since fixed), is gone from every ledger; each listed 1.4.5.8 beside it.
- [x] Live on Windows (2026-10-09): `build-check` resolved mining and reach at one site
      each and reported every other cheat as having no site under netfx-4.8.1. Both
      enabled (pickSpeed 0.2, reach 20); the maintainer confirmed faster mining and
      longer placement reach in play. Both disabled; the site read back from the game
      as `89 96 70 05 00 00 D9 E8 D9 9E 14 05 00 00`, the original bytes. 1.4.5.8 is
      added to the CLR `reset_block` ledger.

Slice 8 � the remaining in-place cheats on the CLR:
- [x] max_minions: ResetEffects' `maxMinions = 1; maxTurrets = 1` (0x30C, 0x5A8), the
      shape of mono's anchor with esi for edi; the immediate is the value.
- [x] fast_place: `ApplyItemTime(Item, float)`, found by its useTime load and
      truncation. The CLR's clamp is 15 bytes (`if (useTime > 0 && time <= 0) time =
      1`), both jumps landing past it, so the whole of it becomes `mov eax, N` and ten
      nops (encoder `mov-eax-imm32-min1`).
- [x] pylons: `PlacementPreviewHook_CheckIfCanPlace`, found among the code that loads
      `Main.PylonSystem`'s static slot (Main.player's + 0x48); its first instruction
      becomes `xor eax, eax; ret 0x10`. Compiled only after a pylon's placement preview.
- [x] **A site is written only if it holds the original bytes or the cheat's own
      patch** (a value-built patch is recognised by every byte its value does not
      change), on enable and disable, under every entry. Anchors wildcard exactly the
      bytes a cheat writes, so a match was no evidence of them. Every encoder's output
      is the length of the bytes it replaces (tested for every site).
- [x] Live on Windows (2026-10-09): a minion cap of 10, faster placement, and a
      second pylon of a type already placed, each confirmed in play by the
      maintainer; each disabled and its site read back as the original bytes. All
      five in-place cheats now work under .NET Framework; the stub-based ones need a
      CLR injection set.

Next slices:
- [ ] The stub-based cheats under the CLR: an injection set of its own (arena,
      springboard, bodies), selected by the entry's `Injections`.
- [ ] Then NPCs, projectiles, recipes and content, selling, buffs; then writes
      (phase 4), each with characterization tests pinned first.

Findings from slice 1:
- **A buffed player can be missed by the scan, on both runtimes (pre-existing).**
  `FindPlayers` prefilters with `life > lifeMax` on the *permanent* cap, before
  `ValidBlock`, which accepts life up to the boosted cap. A player whose current
  life is above their permanent cap is skipped. `TestFindPlayers` pins this — its
  420-of-400 copy is planted and expected not to be found. Not changed here: it is
  Linux behaviour and needs its own decision.
- Equivalent mutant: removing the CLR guard in front of the mono ground-truth
  anchor changes no answer, because the mono byte pattern does not occur in CLR
  memory. The guard saves an executable-memory scan; it is defensive, not
  load-bearing.
- With several indistinguishable copies and no ground truth, which copy the
  service picks is arbitrary (the first found). Not pinned by a test, since pinning
  it would make an arbitrary choice look like a contract.

Phases 4 and 5 get their criteria when phase 3 is complete.

## Risks & Assumptions

- `cmd/clrfields`' FieldDesc model is measured on this runtime build (`clr.dll` from
  .NET Framework 4.8.1). Another Framework servicing update could change it; the
  complete-list and static-flag checks are what would catch that, so they stay
  mandatory rather than diagnostic.
- MethodTable addresses are per process. Nothing may pin them; the tool re-reads them.
- .NET Framework does not tier-compile, so a method is JIT'd once per process. Unlike
  mono, two copies of one method's code are not expected — `Unique` anchors may behave
  differently. To measure.
- Anti-cheat: Terraria single-player has none. Writing to the process is the same
  operation the Linux build performs through `/proc`.
- `WriteProcessMemory` into JIT code pages: .NET Framework allocates JIT code
  `PAGE_EXECUTE_READWRITE`, so no `VirtualProtectEx` is expected. To measure.
- GUI build on Windows requires cgo (fyne's GL driver). The CLI does not. The
  maintainer's machine has MSYS2 gcc at `C:\msys64\ucrt64\bin`.
