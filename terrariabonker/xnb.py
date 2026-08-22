"""Read Terraria's XNB item sprites (``Content/Images/Item_<id>.xnb``) with no external
tools — a self-contained XNB container parser, LZX decompressor, and Texture2D decoder,
so the icon cache can be reconstituted from the game's own files on any machine.

Terraria's Windows/XNA content is XNB v5 with LZX compression (flag ``0x80``) wrapping a
``Texture2D`` (SurfaceFormat.Color = 32-bit RGBA). The LZX decoder is a Python port of
the canonical libmspack ``lzxd`` / MonoGame ``LzxDecoder`` used by every XNB reader; the
window is 64 KB (``window_bits = 16``) as XNA emits.

Only what the item icons need is implemented: LZX (no delta), SurfaceFormat.Color, the
first mip. Other formats raise ``XnbError`` (logged, sprite skipped) rather than guessing.
"""

from __future__ import annotations

import struct

MIN_MATCH = 2
NUM_CHARS = 256
BLOCKTYPE_VERBATIM = 1
BLOCKTYPE_ALIGNED = 2
BLOCKTYPE_UNCOMPRESSED = 3
PRETREE_NUM_ELEMENTS = 20
ALIGNED_NUM_ELEMENTS = 8
NUM_PRIMARY_LENGTHS = 7
NUM_SECONDARY_LENGTHS = 249

PRETREE_MAXSYMBOLS = PRETREE_NUM_ELEMENTS
PRETREE_TABLEBITS = 6
MAINTREE_MAXSYMBOLS = NUM_CHARS + 50 * 8
MAINTREE_TABLEBITS = 12
LENGTH_MAXSYMBOLS = NUM_SECONDARY_LENGTHS + 1
LENGTH_TABLEBITS = 12
ALIGNED_MAXSYMBOLS = ALIGNED_NUM_ELEMENTS
ALIGNED_TABLEBITS = 7


class XnbError(RuntimeError):
    pass


class _BitReader:
    """LZX bit reader: input consumed as little-endian 16-bit words, bits taken MSB-first
    from a 32-bit accumulator (matches MonoGame's BitBuffer exactly)."""

    def __init__(self, data: bytes):
        self.data = data
        self.pos = 0
        self.buf = 0
        self.bitsleft = 0

    def ensure(self, n: int) -> None:
        while self.bitsleft < n:
            if self.pos + 1 < len(self.data):
                lo = self.data[self.pos]
                hi = self.data[self.pos + 1]
            elif self.pos < len(self.data):
                lo = self.data[self.pos]
                hi = 0
            else:
                lo = hi = 0
            self.pos += 2
            self.buf |= ((hi << 8) | lo) << (32 - 16 - self.bitsleft)
            self.buf &= 0xFFFFFFFF
            self.bitsleft += 16

    def peek(self, n: int) -> int:
        return (self.buf >> (32 - n)) & ((1 << n) - 1) if n else 0

    def remove(self, n: int) -> None:
        self.buf = (self.buf << n) & 0xFFFFFFFF
        self.bitsleft -= n

    def read(self, n: int) -> int:
        if n == 0:
            return 0
        self.ensure(n)
        v = self.peek(n)
        self.remove(n)
        return v

    def align_16(self) -> None:
        """Skip to the next 16-bit boundary (uncompressed-block alignment)."""
        if self.bitsleft & 0xF:
            self.remove(self.bitsleft & 0xF)

    def read_u32_le_raw(self) -> int:
        """Read a raw little-endian uint32 from the byte stream (uncompressed block)."""
        # after align_16 the accumulator is on a word boundary; drain remaining whole
        # words from the buffer first, then the byte stream.
        bytes_out = []
        while len(bytes_out) < 4:
            if self.bitsleft >= 8:
                bytes_out.append(self.peek(8))
                self.remove(8)
            else:
                bytes_out.append(self.data[self.pos] if self.pos < len(self.data) else 0)
                self.pos += 1
        return bytes_out[0] | (bytes_out[1] << 8) | (bytes_out[2] << 16) | (bytes_out[3] << 24)


def _make_decode_table(nsyms, nbits, length, table):
    """Build a canonical-Huffman fast-decode table (libmspack make_decode_table)."""
    pos = 0
    table_mask = 1 << nbits
    bit_mask = table_mask >> 1
    next_symbol = bit_mask
    bit_num = 1
    while bit_num <= nbits:
        for sym in range(nsyms):
            if length[sym] == bit_num:
                leaf = pos
                pos += bit_mask
                if pos > table_mask:
                    return False
                fill = bit_mask
                while fill > 0:
                    table[leaf] = sym
                    leaf += 1
                    fill -= 1
        bit_mask >>= 1
        bit_num += 1
    if pos == table_mask:
        return True
    # clear the remainder of the table for codes longer than nbits
    for sym in range(pos, table_mask):
        table[sym] = 0
    pos <<= 16
    table_mask <<= 16
    bit_mask = 1 << 15
    while bit_num <= 16:
        for sym in range(nsyms):
            if length[sym] == bit_num:
                if (pos >> 16) >= table_mask:
                    return False
                leaf = pos >> 16
                for fill in range(bit_num - nbits):
                    if table[leaf] == 0:
                        table[(next_symbol << 1)] = 0
                        table[(next_symbol << 1) + 1] = 0
                        table[leaf] = next_symbol
                        next_symbol += 1
                    leaf = table[leaf] << 1
                    if (pos >> (15 - fill)) & 1:
                        leaf += 1
                table[leaf] = sym
                pos += bit_mask
        bit_mask >>= 1
        bit_num += 1
    return pos == table_mask


class LzxDecoder:
    """Port of the MonoGame/libmspack LZX decompressor for XNB (window_bits = 16)."""

    def __init__(self, window_bits: int = 16):
        wndsize = 1 << window_bits
        self.window_size = wndsize
        self.window = bytearray(wndsize)
        self.window_posn = 0
        self.R0 = self.R1 = self.R2 = 1
        if window_bits == 20:
            posn_slots = 42
        elif window_bits == 21:
            posn_slots = 50
        else:
            posn_slots = window_bits << 1
        self.main_elements = NUM_CHARS + (posn_slots << 3)
        self.header_read = False
        self.block_remaining = 0
        self.block_type = 0
        self.intel_filesize = 0

        # extra_bits[] and position_base[]
        self.extra_bits = [0] * 52
        j = 0
        for i in range(0, 52, 2):
            self.extra_bits[i] = j
            if i + 1 < 52:
                self.extra_bits[i + 1] = j
            if i != 0 and j < 17:
                j += 1
        self.position_base = [0] * 51
        j = 0
        for i in range(51):
            self.position_base[i] = j
            j += 1 << self.extra_bits[i]

        self.PRETREE_len = bytearray(PRETREE_MAXSYMBOLS + 1)
        self.MAINTREE_len = bytearray(MAINTREE_MAXSYMBOLS + 1)
        self.LENGTH_len = bytearray(LENGTH_MAXSYMBOLS + 1)
        self.ALIGNED_len = bytearray(ALIGNED_MAXSYMBOLS + 1)
        self.PRETREE_table = [0] * ((1 << PRETREE_TABLEBITS) + (PRETREE_MAXSYMBOLS << 1))
        self.MAINTREE_table = [0] * ((1 << MAINTREE_TABLEBITS) + (MAINTREE_MAXSYMBOLS << 1))
        self.LENGTH_table = [0] * ((1 << LENGTH_TABLEBITS) + (LENGTH_MAXSYMBOLS << 1))
        self.ALIGNED_table = [0] * ((1 << ALIGNED_TABLEBITS) + (ALIGNED_MAXSYMBOLS << 1))

    def _read_huffsym(self, br, table, tablebits, maxsymbols, lengths):
        br.ensure(16)
        sym = table[br.peek(tablebits)]
        if sym >= maxsymbols:
            i = 1 << (32 - tablebits)
            while True:
                i >>= 1
                sym <<= 1
                sym |= 1 if (br.buf & i) else 0
                if i == 0:
                    raise XnbError("huffman decode overrun")
                sym = table[sym]
                if sym < maxsymbols:
                    break
        length = lengths[sym]
        br.remove(length)
        return sym

    def _read_lengths(self, br, lengths, first, last):
        # pretree: 20 * 4-bit code lengths
        for i in range(PRETREE_NUM_ELEMENTS):
            self.PRETREE_len[i] = br.read(4)
        if not _make_decode_table(PRETREE_MAXSYMBOLS, PRETREE_TABLEBITS,
                                  self.PRETREE_len, self.PRETREE_table):
            raise XnbError("pretree table build failed")
        i = first
        while i < last:
            z = self._read_huffsym(br, self.PRETREE_table, PRETREE_TABLEBITS,
                                   PRETREE_MAXSYMBOLS, self.PRETREE_len)
            if z == 17:
                y = br.read(4) + 4
                for _ in range(y):
                    if i >= last:
                        break
                    lengths[i] = 0
                    i += 1
            elif z == 18:
                y = br.read(5) + 20
                for _ in range(y):
                    if i >= last:
                        break
                    lengths[i] = 0
                    i += 1
            elif z == 19:
                y = br.read(1) + 4
                z = self._read_huffsym(br, self.PRETREE_table, PRETREE_TABLEBITS,
                                       PRETREE_MAXSYMBOLS, self.PRETREE_len)
                z = lengths[i] - z
                if z < 0:
                    z += 17
                for _ in range(y):
                    if i >= last:
                        break
                    lengths[i] = z
                    i += 1
            else:
                z = lengths[i] - z
                if z < 0:
                    z += 17
                lengths[i] = z
                i += 1

    def decompress(self, in_bytes: bytes, out_len: int) -> bytes:
        br = _BitReader(in_bytes)
        out = bytearray(out_len)
        out_pos = 0
        window = self.window
        wsize = self.window_size

        if not self.header_read:
            if br.read(1):
                hi = br.read(16)
                lo = br.read(16)
                self.intel_filesize = (hi << 16) | lo
            self.header_read = True

        while out_pos < out_len:
            if self.block_remaining == 0:
                self.block_type = br.read(3)
                hi = br.read(16)
                lo = br.read(8)
                self.block_remaining = (hi << 8) | lo
                bt = self.block_type
                if bt == BLOCKTYPE_ALIGNED:
                    for i in range(ALIGNED_NUM_ELEMENTS):
                        self.ALIGNED_len[i] = br.read(3)
                    if not _make_decode_table(ALIGNED_MAXSYMBOLS, ALIGNED_TABLEBITS,
                                              self.ALIGNED_len, self.ALIGNED_table):
                        raise XnbError("aligned table build failed")
                    # falls through into verbatim tree reading
                    self._read_main_and_length(br)
                elif bt == BLOCKTYPE_VERBATIM:
                    self._read_main_and_length(br)
                elif bt == BLOCKTYPE_UNCOMPRESSED:
                    br.align_16()
                    self.R0 = br.read_u32_le_raw()
                    self.R1 = br.read_u32_le_raw()
                    self.R2 = br.read_u32_le_raw()
                else:
                    raise XnbError(f"unknown LZX block type {bt}")

            # decode this block, bounded by the frame (out_len) and block_remaining
            this_run = self.block_remaining
            while this_run > 0 and out_pos < out_len:
                amount = this_run
                if amount > (out_len - out_pos):
                    amount = out_len - out_pos
                produced = self._decode_run(br, out, out_pos, amount, window, wsize)
                out_pos += produced
                this_run -= produced
                self.block_remaining -= produced
                if produced == 0:
                    break

        return bytes(out)

    def _read_main_and_length(self, br):
        self._read_lengths(br, self.MAINTREE_len, 0, NUM_CHARS)
        self._read_lengths(br, self.MAINTREE_len, NUM_CHARS, self.main_elements)
        if not _make_decode_table(MAINTREE_MAXSYMBOLS, MAINTREE_TABLEBITS,
                                  self.MAINTREE_len, self.MAINTREE_table):
            raise XnbError("maintree table build failed")
        self._read_lengths(br, self.LENGTH_len, 0, NUM_SECONDARY_LENGTHS)
        if not _make_decode_table(LENGTH_MAXSYMBOLS, LENGTH_TABLEBITS,
                                  self.LENGTH_len, self.LENGTH_table):
            raise XnbError("length table build failed")

    def _decode_run(self, br, out, out_pos, amount, window, wsize):
        """Decode up to ``amount`` output bytes (bounded by frame + block). Returns the
        count produced."""
        start = out_pos
        wp = self.window_posn
        end = out_pos + amount
        if self.block_type == BLOCKTYPE_UNCOMPRESSED:
            for _ in range(amount):
                b = br.data[br.pos] if br.pos < len(br.data) else 0
                br.pos += 1
                window[wp] = b
                out[out_pos] = b
                wp = (wp + 1) & (wsize - 1)
                out_pos += 1
            self.window_posn = wp
            return out_pos - start

        while out_pos < end:
            main = self._read_huffsym(br, self.MAINTREE_table, MAINTREE_TABLEBITS,
                                      MAINTREE_MAXSYMBOLS, self.MAINTREE_len)
            if main < NUM_CHARS:
                window[wp] = main
                out[out_pos] = main
                wp = (wp + 1) & (wsize - 1)
                out_pos += 1
                continue
            main -= NUM_CHARS
            match_length = main & NUM_PRIMARY_LENGTHS
            if match_length == NUM_PRIMARY_LENGTHS:
                lf = self._read_huffsym(br, self.LENGTH_table, LENGTH_TABLEBITS,
                                        LENGTH_MAXSYMBOLS, self.LENGTH_len)
                match_length += lf
            match_length += MIN_MATCH
            slot = main >> 3
            if slot > 2:
                if self.block_type == BLOCKTYPE_ALIGNED:
                    extra = self.extra_bits[slot]
                    if extra > 3:
                        verbatim = br.read(extra - 3) << 3
                        aligned = self._read_huffsym(br, self.ALIGNED_table, ALIGNED_TABLEBITS,
                                                     ALIGNED_MAXSYMBOLS, self.ALIGNED_len)
                        match_offset = self.position_base[slot] - 2 + verbatim + aligned
                    elif extra == 3:
                        aligned = self._read_huffsym(br, self.ALIGNED_table, ALIGNED_TABLEBITS,
                                                     ALIGNED_MAXSYMBOLS, self.ALIGNED_len)
                        match_offset = self.position_base[slot] - 2 + aligned
                    elif extra > 0:
                        verbatim = br.read(extra)
                        match_offset = self.position_base[slot] - 2 + verbatim
                    else:
                        match_offset = 1
                else:
                    extra = self.extra_bits[slot]
                    verbatim = br.read(extra)
                    match_offset = self.position_base[slot] - 2 + verbatim
                self.R2 = self.R1
                self.R1 = self.R0
                self.R0 = match_offset
            elif slot == 0:
                match_offset = self.R0
            elif slot == 1:
                match_offset = self.R1
                self.R1 = self.R0
                self.R0 = match_offset
            else:  # slot == 2
                match_offset = self.R2
                self.R2 = self.R0
                self.R0 = match_offset

            src = (wp - match_offset) & (wsize - 1)
            for _ in range(match_length):
                b = window[src]
                window[wp] = b
                out[out_pos] = b
                src = (src + 1) & (wsize - 1)
                wp = (wp + 1) & (wsize - 1)
                out_pos += 1
                if out_pos >= end:
                    break

        self.window_posn = wp
        return out_pos - start


def _read_7bit_int(data, pos):
    result = 0
    shift = 0
    while True:
        b = data[pos]
        pos += 1
        result |= (b & 0x7F) << shift
        if not (b & 0x80):
            break
        shift += 7
    return result, pos


def decompress_xnb(raw: bytes) -> bytes:
    """Return the decompressed XNB content stream (everything after the 4-byte header +
    sizes), handling the LZX chunk framing. Raw (uncompressed) XNBs are returned as-is."""
    if raw[:3] != b"XNB":
        raise XnbError("not an XNB file")
    flags = raw[5]
    compressed = bool(flags & 0x80)
    lz4 = bool(flags & 0x40)
    if lz4:
        raise XnbError("LZ4 (FNA) XNB not supported")
    file_size = struct.unpack_from("<I", raw, 6)[0]
    if not compressed:
        return raw[10:file_size]
    decomp_size = struct.unpack_from("<I", raw, 10)[0]
    pos = 14
    out = bytearray()
    dec = LzxDecoder(16)
    while len(out) < decomp_size and pos < len(raw):
        hi = raw[pos]
        lo = raw[pos + 1]
        pos += 2
        if hi == 0xFF:
            # explicit frame size then block size, both 16-bit big-endian
            frame_size = (lo << 8) | raw[pos]
            block_size = (raw[pos + 1] << 8) | raw[pos + 2]
            pos += 3
        else:
            block_size = (hi << 8) | lo
            frame_size = 0x8000
        if block_size == 0 or frame_size == 0:
            break
        chunk = raw[pos:pos + block_size]
        pos += block_size
        out += dec.decompress(chunk, frame_size)
    return bytes(out[:decomp_size])


def read_item_texture(path: str):
    """Decode ``Item_<id>.xnb`` to an RGBA ``PIL.Image``. Raises ``XnbError`` on any
    unsupported format so the caller can skip that sprite."""
    from PIL import Image

    with open(path, "rb") as f:
        raw = f.read()
    content = decompress_xnb(raw)

    pos = 0
    reader_count, pos = _read_7bit_int(content, pos)
    for _ in range(reader_count):
        slen, pos = _read_7bit_int(content, pos)
        pos += slen                                   # reader type name
        pos += 4                                      # reader version (int32)
    shared_count, pos = _read_7bit_int(content, pos)
    type_id, pos = _read_7bit_int(content, pos)       # primary object's reader index
    if type_id == 0:
        raise XnbError("null primary asset")
    surface_format = struct.unpack_from("<i", content, pos)[0]
    pos += 4
    width = struct.unpack_from("<I", content, pos)[0]
    pos += 4
    height = struct.unpack_from("<I", content, pos)[0]
    pos += 4
    pos += 4                                          # mip count (only the first mip read)
    data_size = struct.unpack_from("<I", content, pos)[0]
    pos += 4
    if surface_format != 0:                           # 0 == SurfaceFormat.Color (RGBA)
        raise XnbError(f"unsupported SurfaceFormat {surface_format}")
    pixels = content[pos:pos + data_size]
    if len(pixels) < width * height * 4:
        raise XnbError("truncated texture data")
    return Image.frombytes("RGBA", (width, height), bytes(pixels[:width * height * 4]))
