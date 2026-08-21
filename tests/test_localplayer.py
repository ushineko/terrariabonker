"""resolve_local_player: ground-truth live player via Main.player[myPlayer]."""

import struct

from terrariabonker import locate as L


def test_resolve_local_player(monkeypatch):
    from conftest import FakeMem
    BASE = 0x40000000
    m = FakeMem(BASE, 0x10000)
    CODE = BASE + 0x2000            # get_LocalPlayer
    ARR = BASE + 0x4000            # Main.player szarray object
    OBJ = BASE + 0x5000            # the live Player object
    LIFE = OBJ + L.STATLIFE_FROM_OBJ
    NAME = BASE + 0x60
    PLAYER_STATIC = BASE + 0x100
    MYPLAYER_STATIC = BASE + 0x104

    # get_LocalPlayer: mov eax,[Main.player]; mov ecx,[Main.myPlayer]; <index+ret tail>
    code = (b"\x8b\x05" + struct.pack("<I", PLAYER_STATIC)
            + b"\x8b\x0d" + struct.pack("<I", MYPLAYER_STATIC)
            + L._LOCALPLAYER_TAIL)
    m.write(CODE, code)
    m.write(PLAYER_STATIC, struct.pack("<I", ARR))
    m.write(MYPLAYER_STATIC, struct.pack("<i", 0))     # myPlayer = 0
    m.write(ARR + 0x10, struct.pack("<I", OBJ))         # player[0] -> OBJ
    m.plant_mono_string(NAME, "hero")
    m.plant_player(LIFE, [100, 100, 100, 20, 20, 20], NAME)

    monkeypatch.setattr(L, "_exec_regions", lambda mem: [(CODE, CODE + len(code) + 16)])

    lp = L.resolve_local_player(m)
    assert lp is not None
    assert lp.life_addr == LIFE
    assert lp.name == "hero"


def test_resolve_local_player_missing_pattern(monkeypatch):
    from conftest import FakeMem
    m = FakeMem(0x40000000, 0x2000)                     # no get_LocalPlayer planted
    monkeypatch.setattr(L, "_exec_regions", lambda mem: [(0x40000000, 0x40002000)])
    assert L.resolve_local_player(m) is None            # -> callers fall back to scan
