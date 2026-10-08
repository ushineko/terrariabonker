package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
)

/*
The assembly's own field list: ECMA-335 metadata, read from the #~ stream.

Only the first five tables are read (Module, TypeRef, TypeDef, FieldPtr,
Field), which is everything up to and including Field. Their row widths depend
on heap sizes and on coded-index widths, and those depend on row counts of
other tables, which the stream header gives for all of them.
*/

// Field is one FieldDef row.
type Field struct {
	RID    uint32
	Name   string
	Flags  uint16
	Static bool
	// Literal fields are compile-time constants. They have no storage, so the
	// runtime builds no FieldDesc for them.
	Literal bool
}

// Type is one TypeDef row and the fields it declares.
type Type struct {
	Namespace, Name string
	Fields          []Field
}

// FullName is Namespace.Name.
func (t Type) FullName() string {
	if t.Namespace == "" {
		return t.Name
	}
	return t.Namespace + "." + t.Name
}

const (
	tblModule      = 0x00
	tblTypeRef     = 0x01
	tblTypeDef     = 0x02
	tblFieldPtr    = 0x03
	tblField       = 0x04
	tblMethodDef   = 0x06
	tblModuleRef   = 0x1A
	tblTypeSpec    = 0x1B
	tblAssemblyRef = 0x23
)

// LoadTypes reads every TypeDef and its fields out of a .NET assembly.
func LoadTypes(path string) ([]Type, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var clr pe.DataDirectory
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		clr = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_COM_DESCRIPTOR]
	case *pe.OptionalHeader64:
		clr = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_COM_DESCRIPTOR]
	}
	if clr.VirtualAddress == 0 {
		return nil, fmt.Errorf("%s is not a .NET assembly", path)
	}
	rva := func(addr, size uint32) ([]byte, error) {
		for _, s := range f.Sections {
			if addr >= s.VirtualAddress && addr+size <= s.VirtualAddress+s.VirtualSize {
				b := make([]byte, size)
				if _, err := s.ReadAt(b, int64(addr-s.VirtualAddress)); err != nil {
					return nil, fmt.Errorf("reading rva %#x: %w", addr, err)
				}
				return b, nil
			}
		}
		return nil, fmt.Errorf("rva %#x not in any section", addr)
	}
	cor, err := rva(clr.VirtualAddress, 72)
	if err != nil {
		return nil, err
	}
	md, err := rva(binary.LittleEndian.Uint32(cor[8:]), binary.LittleEndian.Uint32(cor[12:]))
	if err != nil {
		return nil, err
	}
	return parseMetadata(md)
}

func parseMetadata(md []byte) ([]Type, error) {
	le := binary.LittleEndian
	if le.Uint32(md) != 0x424A5342 {
		return nil, fmt.Errorf("no metadata signature")
	}
	verLen := le.Uint32(md[12:])
	p := 16 + verLen + 2
	nStreams := int(le.Uint16(md[p:]))
	p += 2
	streams := map[string][]byte{}
	for i := 0; i < nStreams; i++ {
		off, size := le.Uint32(md[p:]), le.Uint32(md[p+4:])
		p += 8
		end := uint32(bytes.IndexByte(md[p:], 0)) //nolint:gosec // an index into a slice far smaller than 4 GB; -1 is caught below
		if end > uint32(len(md))-p {              //nolint:gosec // a metadata blob far smaller than 4 GB
			return nil, fmt.Errorf("metadata stream name is not terminated")
		}
		name := string(md[p : p+end])
		p += (end + 4) &^ 3
		streams[name] = md[off : off+size]
	}
	tabs, strs := streams["#~"], streams["#Strings"]
	if tabs == nil || strs == nil {
		return nil, fmt.Errorf("metadata has no #~ or #Strings stream")
	}
	heap := tabs[6]
	valid := le.Uint64(tabs[8:])
	rows := make([]uint32, 64)
	q := 24
	for i := 0; i < 64; i++ {
		if valid&(1<<i) != 0 {
			rows[i] = le.Uint32(tabs[q:])
			q += 4
		}
	}
	strW, guidW, blobW := 2, 2, 2
	if heap&1 != 0 {
		strW = 4
	}
	if heap&2 != 0 {
		guidW = 4
	}
	if heap&4 != 0 {
		blobW = 4
	}
	idx := func(t int) int {
		if rows[t] > 0xFFFF {
			return 4
		}
		return 2
	}
	coded := func(bits uint, ts ...int) int {
		for _, t := range ts {
			if rows[t] >= 1<<(16-bits) {
				return 4
			}
		}
		return 2
	}
	width := map[int]int{
		tblModule:   2 + strW + 3*guidW,
		tblTypeRef:  coded(2, tblModule, tblModuleRef, tblAssemblyRef, tblTypeRef) + 2*strW,
		tblTypeDef:  4 + 2*strW + coded(2, tblTypeDef, tblTypeRef, tblTypeSpec) + idx(tblField) + idx(tblMethodDef),
		tblFieldPtr: idx(tblField),
		tblField:    2 + strW + blobW,
	}
	start := map[int]int{}
	for t := tblModule; t <= tblField; t++ {
		start[t] = q
		q += int(rows[t]) * width[t]
	}
	read := func(at, w int) uint32 {
		if w == 2 {
			return uint32(le.Uint16(tabs[at:]))
		}
		return le.Uint32(tabs[at:])
	}
	str := func(i uint32) string {
		end := bytes.IndexByte(strs[i:], 0)
		return string(strs[i : int(i)+end])
	}
	if rows[tblFieldPtr] != 0 {
		return nil, fmt.Errorf("uncompressed metadata (FieldPtr table) is not supported")
	}

	fields := make([]Field, rows[tblField])
	for i := range fields {
		at := start[tblField] + i*width[tblField]
		flags := le.Uint16(tabs[at:])
		fields[i] = Field{
			RID: uint32(i + 1), Name: str(read(at+2, strW)), Flags: flags,
			Static: flags&0x10 != 0, Literal: flags&0x40 != 0,
		}
	}
	types := make([]Type, rows[tblTypeDef])
	fieldListAt := 4 + 2*strW + coded(2, tblTypeDef, tblTypeRef, tblTypeSpec)
	firsts := make([]uint32, len(types)+1)
	for i := range types {
		at := start[tblTypeDef] + i*width[tblTypeDef]
		types[i].Name = str(read(at+4, strW))
		types[i].Namespace = str(read(at+4+strW, strW))
		firsts[i] = read(at+fieldListAt, idx(tblField))
	}
	firsts[len(types)] = rows[tblField] + 1
	for i := range types {
		lo, hi := firsts[i], firsts[i+1]
		if hi > rows[tblField]+1 {
			hi = rows[tblField] + 1
		}
		if lo >= 1 && lo < hi {
			types[i].Fields = fields[lo-1 : hi-1]
		}
	}
	return types, nil
}
