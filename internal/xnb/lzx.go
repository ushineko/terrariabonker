/*
Package xnb reads Terraria's XNB sprites without any external tool.

The game's Windows content is XNB v5 with LZX compression wrapping a Texture2D,
and nothing in the Go ecosystem reads LZX -- it is a Microsoft compression from
the cabinet format, kept alive by XNA. So the container parser, the
decompressor and the texture decoder are all here, which is what lets the icon
cache be rebuilt on any machine from that machine's own game files.

Only what the icons need is implemented: LZX without the delta window,
SurfaceFormat.Color, the first mip. Anything else is an error rather than a
guess -- a sprite decoded from a format this does not understand is a wrong
picture that would sit in the cache until somebody noticed.

The decoder is a port of the canonical libmspack lzxd, by way of MonoGame's
LzxDecoder, which is what every XNB reader uses. The window is 64 KB, as XNA
emits.

Ported from terrariabonker/xnb.py (spec 051, step 8).
*/
package xnb

import (
	"errors"
	"fmt"
)

// Error is a file this cannot read, which the caller skips rather than guesses
// at.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func errf(format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// IsFormat reports whether a failure was this package refusing a file, as
// opposed to something going wrong underneath.
func IsFormat(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

// The LZX constants, spelled as libmspack spells them.
const (
	minMatch            = 2
	numChars            = 256
	blockVerbatim       = 1
	blockAligned        = 2
	blockUncompressed   = 3
	pretreeNumElements  = 20
	alignedNumElements  = 8
	numPrimaryLengths   = 7
	numSecondaryLengths = 249

	pretreeMaxSymbols  = pretreeNumElements
	pretreeTableBits   = 6
	maintreeMaxSymbols = numChars + 50*8
	maintreeTableBits  = 12
	lengthMaxSymbols   = numSecondaryLengths + 1
	lengthTableBits    = 12
	alignedMaxSymbols  = alignedNumElements
	alignedTableBits   = 7
)

/*
bitReader takes the input as little-endian 16-bit words and the bits from them
most-significant first, out of a 32-bit accumulator.

Both halves of that matter and neither is the obvious choice: the words are
little-endian and the bits inside them are not, which is what the format does
and what every other reader of it reproduces.
*/
type bitReader struct {
	data     []byte
	pos      int
	buf      uint32
	bitsLeft int
}

// ensure fills the accumulator to at least n bits, padding past the end with
// zeros: a frame can end mid-word.
func (b *bitReader) ensure(n int) {
	for b.bitsLeft < n {
		var lo, hi byte
		switch {
		case b.pos+1 < len(b.data):
			lo, hi = b.data[b.pos], b.data[b.pos+1]
		case b.pos < len(b.data):
			lo = b.data[b.pos]
		}
		b.pos += 2
		b.buf |= uint32(uint16(hi)<<8|uint16(lo)) << (32 - 16 - b.bitsLeft) //nolint:gosec // 16 bits into 32
		b.bitsLeft += 16
	}
}

func (b *bitReader) peek(n int) uint32 {
	if n == 0 {
		return 0
	}
	return (b.buf >> (32 - n)) & ((1 << n) - 1)
}

func (b *bitReader) remove(n int) {
	b.buf <<= n
	b.bitsLeft -= n
}

func (b *bitReader) read(n int) uint32 {
	if n == 0 {
		return 0
	}
	b.ensure(n)
	v := b.peek(n)
	b.remove(n)
	return v
}

// align16 skips to the next word boundary, which is where an uncompressed block
// starts.
func (b *bitReader) align16() {
	if odd := b.bitsLeft & 0xF; odd != 0 {
		b.remove(odd)
	}
}

/*
readU32Raw takes a plain little-endian uint32 out of the byte stream.

Used only by an uncompressed block, where the format stops being a bit stream.
Whatever whole bytes are still in the accumulator come first: after align16 it
sits on a word boundary, and dropping them would skip input.
*/
func (b *bitReader) readU32Raw() uint32 {
	var out [4]byte
	for i := range out {
		if b.bitsLeft >= 8 {
			out[i] = byte(b.peek(8))
			b.remove(8)
			continue
		}
		if b.pos < len(b.data) {
			out[i] = b.data[b.pos]
		}
		b.pos++
	}
	return uint32(out[0]) | uint32(out[1])<<8 | uint32(out[2])<<16 | uint32(out[3])<<24
}

/*
makeDecodeTable builds a canonical-Huffman fast-decode table.

The first half is a direct lookup on the next tableBits bits. Codes longer than
that do not fit, so the rest of the table is used as a binary tree walked one
bit at a time -- which is why the table is bigger than the lookup it serves.

Reports false rather than failing: a table that cannot be built means the stream
is not what it says it is, and the caller has a file to skip.
*/
func makeDecodeTable(nsyms, nbits int, length []byte, table []uint16) bool {
	pos := 0
	tableMask := 1 << nbits
	bitMask := tableMask >> 1
	nextSymbol := bitMask
	bitNum := 1

	for ; bitNum <= nbits; bitNum++ {
		for sym := range nsyms {
			if int(length[sym]) != bitNum {
				continue
			}
			leaf := pos
			pos += bitMask
			if pos > tableMask {
				return false
			}
			for fill := bitMask; fill > 0; fill-- {
				table[leaf] = uint16(sym) //nolint:gosec // a symbol, bounded by nsyms
				leaf++
			}
		}
		bitMask >>= 1
	}
	if pos == tableMask {
		return true
	}
	// Clear what is left, for the codes that did not fit in the lookup.
	for sym := pos; sym < tableMask; sym++ {
		table[sym] = 0
	}
	pos <<= 16
	tableMask <<= 16
	bitMask = 1 << 15

	for ; bitNum <= 16; bitNum++ {
		for sym := range nsyms {
			if int(length[sym]) != bitNum {
				continue
			}
			if (pos >> 16) >= tableMask {
				return false
			}
			leaf := pos >> 16
			for fill := range bitNum - nbits {
				if table[leaf] == 0 {
					table[nextSymbol<<1] = 0
					table[(nextSymbol<<1)+1] = 0
					table[leaf] = uint16(nextSymbol) //nolint:gosec // an index into the table
					nextSymbol++
				}
				leaf = int(table[leaf]) << 1
				if (pos>>(15-fill))&1 != 0 {
					leaf++
				}
			}
			table[leaf] = uint16(sym) //nolint:gosec // a symbol, bounded by nsyms
			pos += bitMask
		}
		bitMask >>= 1
	}
	return pos == tableMask
}

// Decoder is one LZX stream. It carries the sliding window between frames,
// which is why a file is decoded with one of these rather than frame by frame.
type Decoder struct {
	window     []byte
	windowSize int
	windowPosn int
	r0, r1, r2 uint32

	mainElements   int
	headerRead     bool
	blockRemaining uint32
	blockType      uint32
	intelFilesize  uint32

	extraBits    [52]int
	positionBase [51]uint32

	pretreeLen  []byte
	maintreeLen []byte
	lengthLen   []byte
	alignedLen  []byte

	pretreeTable  []uint16
	maintreeTable []uint16
	lengthTable   []uint16
	alignedTable  []uint16
}

// NewDecoder is a decoder over a window of 1<<windowBits bytes. XNA emits 16.
func NewDecoder(windowBits int) *Decoder {
	size := 1 << windowBits
	d := &Decoder{
		window: make([]byte, size), windowSize: size,
		r0: 1, r1: 1, r2: 1,
		pretreeLen:  make([]byte, pretreeMaxSymbols+1),
		maintreeLen: make([]byte, maintreeMaxSymbols+1),
		lengthLen:   make([]byte, lengthMaxSymbols+1),
		alignedLen:  make([]byte, alignedMaxSymbols+1),

		pretreeTable:  make([]uint16, (1<<pretreeTableBits)+(pretreeMaxSymbols<<1)),
		maintreeTable: make([]uint16, (1<<maintreeTableBits)+(maintreeMaxSymbols<<1)),
		lengthTable:   make([]uint16, (1<<lengthTableBits)+(lengthMaxSymbols<<1)),
		alignedTable:  make([]uint16, (1<<alignedTableBits)+(alignedMaxSymbols<<1)),
	}
	posnSlots := windowBits << 1
	switch windowBits {
	case 20:
		posnSlots = 42
	case 21:
		posnSlots = 50
	}
	d.mainElements = numChars + (posnSlots << 3)

	j := 0
	for i := 0; i < 52; i += 2 {
		d.extraBits[i] = j
		if i+1 < 52 {
			d.extraBits[i+1] = j
		}
		if i != 0 && j < 17 {
			j++
		}
	}
	var base uint32
	for i := range d.positionBase {
		d.positionBase[i] = base
		base += 1 << d.extraBits[i]
	}
	return d
}

// readHuffsym takes one symbol, through the lookup where it fits and down the
// tree where it does not.
func (d *Decoder) readHuffsym(br *bitReader, table []uint16, tableBits, maxSymbols int,
	lengths []byte) (int, error) {
	br.ensure(16)
	sym := int(table[br.peek(tableBits)])
	if sym >= maxSymbols {
		i := uint32(1) << (32 - tableBits)
		for {
			i >>= 1
			sym <<= 1
			if br.buf&i != 0 {
				sym |= 1
			}
			if i == 0 {
				return 0, errf("huffman decode overrun")
			}
			sym = int(table[sym])
			if sym < maxSymbols {
				break
			}
		}
	}
	br.remove(int(lengths[sym]))
	return sym, nil
}

/*
readLengths reads a run of code lengths, which are themselves Huffman-coded
against a pretree.

The lengths are stored as differences from the previous tree's, modulo
seventeen, which is why this reads into the array it is also reading from.
*/
func (d *Decoder) readLengths(br *bitReader, lengths []byte, first, last int) error {
	for i := range pretreeNumElements {
		d.pretreeLen[i] = byte(br.read(4))
	}
	if !makeDecodeTable(pretreeMaxSymbols, pretreeTableBits, d.pretreeLen, d.pretreeTable) {
		return errf("the pretree table could not be built")
	}
	for i := first; i < last; {
		z, err := d.readHuffsym(br, d.pretreeTable, pretreeTableBits,
			pretreeMaxSymbols, d.pretreeLen)
		if err != nil {
			return err
		}
		switch {
		case z == 17:
			run := int(br.read(4)) + 4
			for range run {
				if i >= last {
					break
				}
				lengths[i] = 0
				i++
			}
		case z == 18:
			run := int(br.read(5)) + 20
			for range run {
				if i >= last {
					break
				}
				lengths[i] = 0
				i++
			}
		case z == 19:
			run := int(br.read(1)) + 4
			next, err := d.readHuffsym(br, d.pretreeTable, pretreeTableBits,
				pretreeMaxSymbols, d.pretreeLen)
			if err != nil {
				return err
			}
			value := int(lengths[i]) - next
			if value < 0 {
				value += 17
			}
			for range run {
				if i >= last {
					break
				}
				lengths[i] = byte(value) //nolint:gosec // a length, 0..16
				i++
			}
		default:
			value := int(lengths[i]) - z
			if value < 0 {
				value += 17
			}
			lengths[i] = byte(value) //nolint:gosec // a length, 0..16
			i++
		}
	}
	return nil
}

// Decompress produces one frame of exactly outLen bytes.
func (d *Decoder) Decompress(in []byte, outLen int) ([]byte, error) {
	br := &bitReader{data: in}
	out := make([]byte, outLen)
	outPos := 0

	if !d.headerRead {
		if br.read(1) != 0 {
			hi, lo := br.read(16), br.read(16)
			d.intelFilesize = hi<<16 | lo
		}
		d.headerRead = true
	}

	for outPos < outLen {
		if d.blockRemaining == 0 {
			d.blockType = br.read(3)
			hi, lo := br.read(16), br.read(8)
			d.blockRemaining = hi<<8 | lo
			switch d.blockType {
			case blockAligned:
				for i := range alignedNumElements {
					d.alignedLen[i] = byte(br.read(3))
				}
				if !makeDecodeTable(alignedMaxSymbols, alignedTableBits,
					d.alignedLen, d.alignedTable) {
					return nil, errf("the aligned table could not be built")
				}
				// And then the same trees a verbatim block carries.
				if err := d.readMainAndLength(br); err != nil {
					return nil, err
				}
			case blockVerbatim:
				if err := d.readMainAndLength(br); err != nil {
					return nil, err
				}
			case blockUncompressed:
				br.align16()
				d.r0, d.r1, d.r2 = br.readU32Raw(), br.readU32Raw(), br.readU32Raw()
			default:
				return nil, errf("LZX block type %d is not one this reads", d.blockType)
			}
		}

		// This block, bounded by both the frame and what is left of the block.
		run := d.blockRemaining
		for run > 0 && outPos < outLen {
			amount := int(run)
			if amount > outLen-outPos {
				amount = outLen - outPos
			}
			produced, err := d.decodeRun(br, out, outPos, amount)
			if err != nil {
				return nil, err
			}
			outPos += produced
			run -= uint32(produced)              //nolint:gosec // bounded by the block
			d.blockRemaining -= uint32(produced) //nolint:gosec // bounded by the block
			if produced == 0 {
				break
			}
		}
	}
	return out, nil
}

// readMainAndLength reads the two trees a verbatim or aligned block carries.
func (d *Decoder) readMainAndLength(br *bitReader) error {
	if err := d.readLengths(br, d.maintreeLen, 0, numChars); err != nil {
		return err
	}
	if err := d.readLengths(br, d.maintreeLen, numChars, d.mainElements); err != nil {
		return err
	}
	if !makeDecodeTable(maintreeMaxSymbols, maintreeTableBits,
		d.maintreeLen, d.maintreeTable) {
		return errf("the main table could not be built")
	}
	if err := d.readLengths(br, d.lengthLen, 0, numSecondaryLengths); err != nil {
		return err
	}
	if !makeDecodeTable(lengthMaxSymbols, lengthTableBits, d.lengthLen, d.lengthTable) {
		return errf("the length table could not be built")
	}
	return nil
}

// decodeRun produces up to amount bytes, and is how many it made.
func (d *Decoder) decodeRun(br *bitReader, out []byte, outPos, amount int) (int, error) {
	start := outPos
	wp := d.windowPosn
	mask := d.windowSize - 1
	end := outPos + amount

	if d.blockType == blockUncompressed {
		for range amount {
			var b byte
			if br.pos < len(br.data) {
				b = br.data[br.pos]
			}
			br.pos++
			d.window[wp] = b
			out[outPos] = b
			wp = (wp + 1) & mask
			outPos++
		}
		d.windowPosn = wp
		return outPos - start, nil
	}

	for outPos < end {
		main, err := d.readHuffsym(br, d.maintreeTable, maintreeTableBits,
			maintreeMaxSymbols, d.maintreeLen)
		if err != nil {
			return 0, err
		}
		if main < numChars {
			d.window[wp] = byte(main) //nolint:gosec // a literal byte
			out[outPos] = byte(main)  //nolint:gosec // a literal byte
			wp = (wp + 1) & mask
			outPos++
			continue
		}
		main -= numChars
		matchLength := main & numPrimaryLengths
		if matchLength == numPrimaryLengths {
			extra, err := d.readHuffsym(br, d.lengthTable, lengthTableBits,
				lengthMaxSymbols, d.lengthLen)
			if err != nil {
				return 0, err
			}
			matchLength += extra
		}
		matchLength += minMatch

		slot := main >> 3
		var matchOffset uint32
		switch {
		case slot > 2:
			extra := d.extraBits[slot]
			if d.blockType == blockAligned {
				switch {
				case extra > 3:
					verbatim := br.read(extra-3) << 3
					aligned, err := d.readHuffsym(br, d.alignedTable, alignedTableBits,
						alignedMaxSymbols, d.alignedLen)
					if err != nil {
						return 0, err
					}
					matchOffset = d.positionBase[slot] - 2 + verbatim + uint32(aligned) //nolint:gosec // a small symbol
				case extra == 3:
					aligned, err := d.readHuffsym(br, d.alignedTable, alignedTableBits,
						alignedMaxSymbols, d.alignedLen)
					if err != nil {
						return 0, err
					}
					matchOffset = d.positionBase[slot] - 2 + uint32(aligned) //nolint:gosec // a small symbol
				case extra > 0:
					matchOffset = d.positionBase[slot] - 2 + br.read(extra)
				default:
					matchOffset = 1
				}
			} else {
				matchOffset = d.positionBase[slot] - 2 + br.read(extra)
			}
			d.r2, d.r1, d.r0 = d.r1, d.r0, matchOffset
		case slot == 0:
			matchOffset = d.r0
		case slot == 1:
			matchOffset = d.r1
			d.r1, d.r0 = d.r0, matchOffset
		default: // slot == 2
			matchOffset = d.r2
			d.r2, d.r0 = d.r0, matchOffset
		}

		src := (wp - int(matchOffset)) & mask //nolint:gosec // masked into the window
		for range matchLength {
			b := d.window[src]
			d.window[wp] = b
			out[outPos] = b
			src = (src + 1) & mask
			wp = (wp + 1) & mask
			outPos++
			if outPos >= end {
				break
			}
		}
	}
	d.windowPosn = wp
	return outPos - start, nil
}
