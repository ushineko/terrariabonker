package patch

/*
What the window asks about a build it has not seen before.

Two questions that look alike and are not. Probe asks whether the patterns still
match on this build, which is a question about the build. Details asks what the
window should show for each cheat, which is a question about this session as
well: whether it is on, how many places it is installed at, and whether the AOB
was ever verified here.

Both have to work around the same thing: an injection overwrites its own anchor
with its jump, so a fresh scan misses a cheat that is applied. An applied cheat
therefore reports as resolving, because it demonstrably did.
*/

// CheatProbe is one cheat's verdict on the running build.
type CheatProbe struct {
	Resolved bool   `json:"resolved"`
	Applied  bool   `json:"applied"`
	Sites    int    `json:"sites"`
	Reason   string `json:"reason"`
}

// CheatDetail is one cheat as the window shows it.
type CheatDetail struct {
	On        bool   `json:"on"`
	Available bool   `json:"available"`
	Verified  bool   `json:"verified"`
	Reason    string `json:"reason"`
	Sites     int    `json:"sites"`
}

// AnchorKey is the anchor a patch resolves through, whichever table declares it.
func AnchorKey(name string) string {
	if inj, ok := Injections[name]; ok {
		return inj.Anchor
	}
	return Cheats[name].Anchor
}

/*
Probe resolves every cheat against a build, patching nothing.

An applied cheat is reported with the number of sites it is installed at, or one
when the record does not say -- it is applied, so it is somewhere.
*/
func (p *Patcher) Probe(build string) map[string]CheatProbe {
	out := make(map[string]CheatProbe, len(Cheats)+len(Injections))
	for _, info := range Catalog() {
		if p.IsEnabled(info.Name) {
			sites := len(p.state.Inj[info.Name].Sites)
			if sites == 0 {
				sites = 1
			}
			out[info.Name] = CheatProbe{Resolved: true, Applied: true, Sites: sites}
			continue
		}
		res := p.Scanner.Resolve(AnchorKey(info.Name), build)
		out[info.Name] = CheatProbe{
			Resolved: res.Available, Sites: len(res.Sites), Reason: res.Reason,
		}
	}
	return out
}

/*
Details is per-cheat availability for the window.

For an applied cheat the meaningful count is what is installed, not what the
anchor scan happens to have cached: an injection applied in an earlier process
leaves this one's cache empty, which read as "0 sites" for a cheat patched at
four of them.
*/
func (p *Patcher) Details(build string) map[string]CheatDetail {
	out := make(map[string]CheatDetail, len(Cheats)+len(Injections))
	for _, info := range Catalog() {
		key := AnchorKey(info.Name)
		verified := build != "" && contains(Anchors[key].Verified, build)
		if p.IsEnabled(info.Name) {
			sites := len(p.state.Inj[info.Name].Sites)
			if sites == 0 {
				sites = len(p.Scanner.Cached(key))
			}
			if sites == 0 {
				sites = len(p.state.Sites[key])
			}
			out[info.Name] = CheatDetail{On: true, Available: true,
				Verified: verified, Sites: sites}
			continue
		}
		res := p.Scanner.Resolve(key, build)
		out[info.Name] = CheatDetail{Available: res.Available, Verified: res.Verified,
			Reason: res.Reason, Sites: len(res.Sites)}
	}
	return out
}
