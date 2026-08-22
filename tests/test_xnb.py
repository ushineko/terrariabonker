"""XNB decode: container parse + Texture2D reader (deterministic, uncompressed) and a
real-file LZX check that skips when the game isn't installed."""

import struct

import pytest

from terrariabonker import xnb


def _7bit(n: int) -> bytes:
    out = bytearray()
    while True:
        b = n & 0x7F
        n >>= 7
        if n:
            out.append(b | 0x80)
        else:
            out.append(b)
            return bytes(out)


def _uncompressed_xnb(width, height, pixels, surface_format=0) -> bytes:
    reader = b"Microsoft.Xna.Framework.Content.Texture2DReader"
    content = bytearray()
    content += _7bit(1)                                   # one type reader
    content += _7bit(len(reader)) + reader
    content += struct.pack("<i", 0)                       # reader version
    content += _7bit(0)                                   # shared resources
    content += _7bit(1)                                   # primary object -> reader #1
    content += struct.pack("<i", surface_format)
    content += struct.pack("<II", width, height)
    content += struct.pack("<I", 1)                       # mip count
    content += struct.pack("<I", len(pixels)) + pixels
    header = b"XNBw" + bytes([5, 0x00])                   # platform w, version 5, flags 0
    file_size = 10 + len(content)
    return header + struct.pack("<I", file_size) + bytes(content)


def test_read_7bit_int_multibyte():
    data = _7bit(300) + b"\xff"
    val, pos = xnb._read_7bit_int(data, 0)
    assert val == 300 and pos == 2


def _decode_bytes(raw, tmp_path):
    """Decode an in-memory XNB via the real ``read_item_texture`` path (writes a temp)."""
    p = tmp_path / "sample.xnb"
    p.write_bytes(raw)
    return xnb.read_item_texture(str(p))


def test_uncompressed_texture_roundtrip(tmp_path):
    # 2x2 RGBA: red, green, blue, opaque white
    px = bytes([255, 0, 0, 255,  0, 255, 0, 255,  0, 0, 255, 255,  255, 255, 255, 255])
    img = _decode_bytes(_uncompressed_xnb(2, 2, px), tmp_path)
    assert img.size == (2, 2)
    assert img.mode == "RGBA"
    assert img.getpixel((0, 0)) == (255, 0, 0, 255)
    assert img.getpixel((1, 1)) == (255, 255, 255, 255)


def test_unsupported_surface_format_raises(tmp_path):
    raw = _uncompressed_xnb(2, 2, bytes(16), surface_format=4)   # not Color
    with pytest.raises(xnb.XnbError):
        _decode_bytes(raw, tmp_path)


def test_not_xnb_raises():
    with pytest.raises(xnb.XnbError):
        xnb.decompress_xnb(b"NOPE" + bytes(20))


def test_real_game_sprite_lzx_decodes():
    """Covers the LZX path on real content when the game is installed; skips otherwise."""
    import os

    from terrariabonker import sprites
    src = sprites.content_images_dir()
    if not src:
        pytest.skip("Terraria content dir not available")
    sample = os.path.join(src, "Item_1.xnb")
    if not os.path.exists(sample):
        pytest.skip("sample sprite not present")
    img = xnb.read_item_texture(sample)
    assert img.mode == "RGBA"
    assert img.width > 0 and img.height > 0
    # a real icon has some opaque pixels (alpha channel = every 4th byte)
    assert any(img.tobytes()[3::4])
