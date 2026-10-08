//go:build windows

package main

import (
	"fmt"
	"strconv"
	"strings"
)

// aobSites is every address in executable memory where pat matches, with the
// bytes around it, for deriving a CLR anchor by hand.
func aobSites(g game, exec []region, pat string, limit int) {
	var want []int
	for _, tok := range strings.Fields(pat) {
		if tok == "??" {
			want = append(want, -1)
			continue
		}
		b, _ := strconv.ParseUint(tok, 16, 8)
		want = append(want, int(b))
	}
	n := 0
	for _, r := range exec {
		buf := g.read(r.start, int(r.end-r.start))
	scan:
		for i := 0; i+len(want) <= len(buf); i++ {
			for k, w := range want {
				if w >= 0 && int(buf[i+k]) != w {
					continue scan
				}
			}
			n++
			if n <= limit {
				lo, hi := i-24, i+len(want)+24
				if lo < 0 {
					lo = 0
				}
				if hi > len(buf) {
					hi = len(buf)
				}
				fmt.Printf("  %08x: % x\n", r.start+uint32(i), buf[lo:hi])
			}
		}
	}
	fmt.Printf("  %d matches for %q\n", n, pat)
}
