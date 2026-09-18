package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ushineko/terrariabonker/internal/locate"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/proc"
)

func main() {
	pid, err := proc.FindPID()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mem := proc.New(pid)
	arena, ok := patch.FindArena(mem)
	if !ok {
		fmt.Fprintln(os.Stderr, "no arena")
		os.Exit(1)
	}
	b := &patch.Builder{Scanner: patch.NewScanner(mem), Mem: mem, Arena: arena}
	out := map[string]any{"arena": arena}

	for _, name := range []string{"inventory_accs", "ore_extract", "auto_use"} {
		inj := patch.Injections[name]
		body, err := inj.BuildBody(b, inj)
		if err != nil {
			out[name] = "ERR: " + err.Error()
			continue
		}
		out[name] = hex.EncodeToString(body)
	}

	// The teleport stub, built the same way the enable path would.
	inj := patch.Injections["teleport"]
	res := b.Scanner.Resolve(inj.CallAnchor, "")
	if res.Available {
		target := res.Sites[0] - uint32(inj.CallTargetOff)
		if blk, ok := locate.ResolveLocalPlayer(mem); ok {
			base := blk.LifeAddr - locate.StatLifeFromObj
			out["teleport"] = hex.EncodeToString(patch.TeleportBody(base, target))
			out["player_base"] = base
			out["teleport_target"] = target
		}
	}
	for _, c := range []struct {
		name   string
		anchor string
		off    int
	}{{"apply", "equip_apply", 15}, {"prefix", "equip_benefits", 20}, {"armor", "equip_benefits", 36}} {
		if t, err := patch.CallTarget(b, c.anchor, c.off); err == nil {
			out["call_"+c.name] = t
		}
	}
	j, _ := json.Marshal(out)
	fmt.Println(string(j))
}
