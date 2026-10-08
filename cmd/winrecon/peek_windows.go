//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// peekF64 reads a double at each address twice, a second apart: which of
// several candidates is live, when the value is one the game advances.
func peekF64(g game, list string) {
	var addrs []uint32
	for _, s := range strings.Split(list, ",") {
		v, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(s), "0x"), 16, 32)
		if err == nil {
			addrs = append(addrs, uint32(v))
		}
	}
	read := func(a uint32) float64 {
		b := g.read(a, 8)
		if len(b) < 8 {
			return math.NaN()
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	}
	first := make([]float64, len(addrs))
	for i, a := range addrs {
		first[i] = read(a)
	}
	time.Sleep(time.Second)
	for i, a := range addrs {
		fmt.Printf("  %08x: %.2f -> %.2f\n", a, first[i], read(a))
	}
}
