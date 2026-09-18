package gui

import (
	"github.com/ushineko/fynedesygn/shell"
)

/*
What the window remembers between runs.

The Effects switches and the projectile editor, which is what the Qt panel kept
in ~/.cache/terrariabonker/window.json. It goes in the shell's settings file
now, which is the same idea with one file instead of two.

The settings file is not in ~/.config/terrariabonker, and that is deliberate.
That directory is created by the CLI under sudo and can be root-owned, which is
why the sprite cache lives under ~/.cache; the unprivileged window could not
write into it. The shell's default -- a directory named for the app id, created
by the window itself -- is the user's, so it works.
*/
const stateKey = "window"

// uiState is the saved shape of the window's own settings. Named fields rather
// than a map, so a setting that is renamed is a compile error here instead of a
// value that silently stops being restored.
type uiState struct {
	God     bool `json:"god"`
	Mana    bool `json:"mana"`
	Potions bool `json:"potions"`
	Fishing bool `json:"fishing"`
	Catch   bool `json:"catch"`
	Recast  bool `json:"recast"`
	Sell    bool `json:"sell"`

	BuffPower bool `json:"buffPower"`
	BuffSonar bool `json:"buffSonar"`
	BuffCrate bool `json:"buffCrate"`

	Bait  int `json:"bait"`
	Power int `json:"power"`
	Stack int `json:"stack"`

	// Projectiles is the editor's overrides, and Weapon the item last picked.
	// Whether the editor was running is not kept: it writes to whatever is in
	// flight fifty times a second, and starting that from a window that has
	// just opened onto a game nobody has looked at yet is too much to do
	// unasked.
	Projectiles map[int]map[string]float64 `json:"projectiles,omitempty"`
	Weapon      int                        `json:"weapon,omitempty"`
}

// saveState records the window's settings. Called when the window closes; the
// store writes them and everything else waiting in one go.
func (u *ui) saveState() {
	st := uiState{
		God: u.fx.god, Mana: u.fx.mana,
		Potions: u.potions.running(), Fishing: u.fishing.running(),
		Catch: u.catch.running(), Recast: u.fx.recast, Sell: u.sell.running(),
		BuffPower: u.fx.buffPower, BuffSonar: u.fx.buffSonar, BuffCrate: u.fx.buffCrate,
		Bait: u.fx.bait, Power: u.fx.power, Stack: u.fx.stack,
		Projectiles: u.pj.overrides, Weapon: u.pj.weapon,
	}
	if err := u.sh.Settings().Set(stateKey, st); err != nil {
		u.note("could not save the window's settings: " + err.Error())
	}
}

/*
loadState puts the window back the way it was left.

Numbers before switches: a watch reads the number beside it on its first round,
so arming one before restoring its value would run a round at the default.

The freezes come back too. They start a privileged process rather than a timer,
which is a louder thing to do at start-up -- but leaving godmode ticked and
finding it off is the bug this fixes, and the patches on the other section
already come back this way.
*/
func (u *ui) loadState() {
	var st uiState
	if !u.sh.Settings().Get(stateKey, &st) {
		return
	}

	if st.Bait > 0 {
		u.fx.bait = st.Bait
	}
	if st.Power > 0 {
		u.fx.power = st.Power
	}
	if st.Stack > 0 {
		u.fx.stack = st.Stack
	}
	u.fx.recast = st.Recast
	u.fx.buffPower, u.fx.buffSonar, u.fx.buffCrate = st.BuffPower, st.BuffSonar, st.BuffCrate
	u.pj.overrides, u.pj.weapon = st.Projectiles, st.Weapon

	u.fx.god, u.fx.mana = st.God, st.Mana
	if st.God || st.Mana {
		u.freeze.set(st.God, st.Mana)
	}
	u.potions.set(st.Potions)
	u.fishing.set(st.Fishing)
	u.buffs.set(st.BuffPower || st.BuffSonar || st.BuffCrate)
	u.catch.set(st.Catch)
	u.sell.set(st.Sell)
}

/*
The navigation shapes this window offers.

All of them. The inventory grid and the two tables are the reason: each would
rather have the width than the list of seven words beside it, and the grid is
the one section whose natural size decides how wide the window has to be.
*/
var (
	navModes      = []shell.NavMode{shell.NavLabels, shell.NavIcons, shell.NavHidden}
	navPlacements = []shell.NavPlacement{shell.NavLeft, shell.NavTop}
)
