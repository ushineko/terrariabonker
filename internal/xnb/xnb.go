package xnb

import (
	"encoding/binary"
	"fmt"
	"image"
	"os"
)

/*
The container, and the one texture format the icons need.
*/

// read7BitInt is the length prefix the XNA content format uses everywhere.
func read7BitInt(data []byte, pos int) (int, int, bool) {
	result, shift := 0, 0
	for {
		if pos >= len(data) {
			return 0, pos, false
		}
		b := data[pos]
		pos++
		result |= int(b&0x7F) << shift
		if b&0x80 == 0 {
			return result, pos, true
		}
		shift += 7
		if shift > 28 {
			return 0, pos, false
		}
	}
}

/*
Decompress is the content stream of an XNB file: everything after the header,
with the LZX framing undone.

A file that is not compressed is handed back as it is, which is what the flag
means.
*/
func Decompress(raw []byte) ([]byte, error) {
	if len(raw) < 14 || string(raw[:3]) != "XNB" {
		return nil, errf("not an XNB file")
	}
	flags := raw[5]
	if flags&0x40 != 0 {
		return nil, errf("this is an LZ4 XNB, which FNA writes and this does not read")
	}
	fileSize := int(binary.LittleEndian.Uint32(raw[6:]))
	if flags&0x80 == 0 {
		if fileSize > len(raw) || fileSize < 10 {
			return nil, errf("the XNB says it is %d bytes and it is not", fileSize)
		}
		return raw[10:fileSize], nil
	}

	want := int(binary.LittleEndian.Uint32(raw[10:]))
	pos := 14
	out := make([]byte, 0, want)
	dec := NewDecoder(16)
	for len(out) < want && pos < len(raw) {
		/*
			Each chunk carries its compressed size, and optionally its
			decompressed one: a leading 0xFF means both are written out, and
			otherwise the frame is the full 32 KB.
		*/
		hi, lo := raw[pos], raw[pos+1]
		pos += 2
		frameSize, blockSize := 0x8000, int(hi)<<8|int(lo)
		if hi == 0xFF {
			if pos+2 >= len(raw) {
				break
			}
			frameSize = int(lo)<<8 | int(raw[pos])
			blockSize = int(raw[pos+1])<<8 | int(raw[pos+2])
			pos += 3
		}
		if blockSize == 0 || frameSize == 0 || pos+blockSize > len(raw) {
			break
		}
		chunk, err := dec.Decompress(raw[pos:pos+blockSize], frameSize)
		if err != nil {
			return nil, err
		}
		pos += blockSize
		out = append(out, chunk...)
	}
	if len(out) < want {
		return nil, errf("the XNB stream ended %d bytes short", want-len(out))
	}
	return out[:want], nil
}

// SurfaceColor is SurfaceFormat.Color: eight bits each of R, G, B and A, which
// is what every item sprite is.
const SurfaceColor = 0

/*
ReadTexture decodes an item or NPC sprite file into an image.

Only the first mip is read, which is the one at full size. Anything that is not
SurfaceFormat.Color is refused rather than reinterpreted: the alternative is a
wrong picture in the cache that nobody notices until they look at that item.
*/
func ReadTexture(path string) (*image.NRGBA, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a path the caller chose
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	content, err := Decompress(raw)
	if err != nil {
		return nil, err
	}

	pos := 0
	readers, pos, ok := read7BitInt(content, pos)
	if !ok {
		return nil, errf("the content stream has no reader table")
	}
	for range readers {
		var nameLen int
		nameLen, pos, ok = read7BitInt(content, pos)
		if !ok {
			return nil, errf("a reader name is unreadable")
		}
		pos += nameLen + 4 // the name, then the reader's version
	}
	if _, pos, ok = read7BitInt(content, pos); !ok { // shared resource count
		return nil, errf("the shared resource count is unreadable")
	}
	var typeID int
	if typeID, pos, ok = read7BitInt(content, pos); !ok {
		return nil, errf("the primary asset's type is unreadable")
	}
	if typeID == 0 {
		return nil, errf("the XNB has no primary asset")
	}
	if pos+20 > len(content) {
		return nil, errf("the texture header is truncated")
	}
	format := int32(binary.LittleEndian.Uint32(content[pos:])) //nolint:gosec // a signed field
	width := int(binary.LittleEndian.Uint32(content[pos+4:]))
	height := int(binary.LittleEndian.Uint32(content[pos+8:]))
	// content[pos+12:] is the mip count; only the first mip is read.
	dataSize := int(binary.LittleEndian.Uint32(content[pos+16:]))
	pos += 20

	if format != SurfaceColor {
		return nil, errf("SurfaceFormat %d is not one this reads", format)
	}
	if width <= 0 || height <= 0 {
		return nil, errf("the texture is %dx%d", width, height)
	}
	need := width * height * 4
	if dataSize < need || pos+need > len(content) {
		return nil, errf("the texture data is truncated")
	}
	/*
		Copied rather than aliased: the pixels are a window into a buffer this
		function is the only owner of, and handing that out would tie the
		image's lifetime to the whole decompressed file.
	*/
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	copy(img.Pix, content[pos:pos+need])
	return img, nil
}
