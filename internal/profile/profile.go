/*
Package profile is what the player *wants*, kept across game restarts.

Distinct from the patcher's per-pid record, which is correctly thrown away when
the game restarts: that one says what is installed in a process, this one says
what should be installed in the next one. It is updated on every mutating action
and read by the restore that runs when a fresh game turns up.

Writes are atomic and serialised under a lock, the same way the patch record is,
so two invocations cannot clobber each other.

Ported from terrariabonker/profile.py (spec 051, step 5).
*/
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
)

// Path is where the profile lives.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "terrariabonker", "profile.json")
}

/*
Restorable is the item fields worth saving: the ones the game regenerates from
the item type when a world loads.

Type, stack and prefix are written into the save file by the game itself, so
re-applying them achieves nothing -- and auto-restore used to record all three
and then report failures about items whose only "edit" was a prefix that had
survived perfectly well on its own.
*/
var Restorable = []string{"damage", "auto_reuse", "use_time", "use_anim", "pick",
	"tile_boost", "defense"}

// restorable reports whether a field is one of the above.
func restorable(name string) bool {
	for _, f := range Restorable {
		if f == name {
			return true
		}
	}
	return false
}

/*
Profile is the saved configuration.

Item edits are keyed by item *type* rather than by the slot the item happens to
be in: an item that moved used to lose its edit. The whitelist is keyed the same
way and for the same reason -- it has to keep applying to stacks that have not
arrived yet.
*/
type Profile struct {
	// Cheats maps a cheat's name to the value it was given, or null when it
	// carries none.
	Cheats map[string]*float64 `json:"cheats"`
	// ItemEdits maps an item type, as a string because JSON keys are strings,
	// to the fields that differ from that item's defaults.
	ItemEdits map[string]map[string]float64 `json:"item_edits"`
	// EmptySlots are slots the player deliberately cleared.
	EmptySlots []int `json:"empty_slots"`
	/*
		FishingRestore maps a rod's type to the power it had before a cheat
		raised it.

		The opposite of ItemEdits, which exists to re-apply an edit after a
		restart: this is a note to put something back. On disk rather than in
		memory because the hard case is the trainer being killed while the cheat
		is on.
	*/
	FishingRestore map[string]int32 `json:"fishing_restore"`
	// SellWhitelist is the item types marked for auto-selling.
	SellWhitelist []int32 `json:"sell_whitelist"`
	// Items is the old slot-keyed shape, read so an old profile can be folded
	// into the current one and then dropped. Never written.
	Items map[string]map[string]float64 `json:"items,omitempty"`
}

// Load reads the profile, or an empty one.
func Load() Profile {
	var p Profile
	if raw, err := os.ReadFile(Path()); err == nil {
		_ = json.Unmarshal(raw, &p)
	}
	return migrate(p.filled())
}

// filled gives a profile the empty collections the rest of this assumes.
func (p Profile) filled() Profile {
	if p.Cheats == nil {
		p.Cheats = map[string]*float64{}
	}
	if p.ItemEdits == nil {
		p.ItemEdits = map[string]map[string]float64{}
	}
	if p.EmptySlots == nil {
		p.EmptySlots = []int{}
	}
	if p.FishingRestore == nil {
		p.FishingRestore = map[string]int32{}
	}
	if p.SellWhitelist == nil {
		p.SellWhitelist = []int32{}
	}
	return p
}

/*
migrate folds a slot-keyed profile into the type-keyed one.

Edits used to be stored per slot with every field the dialog submitted, which
meant an item that moved lost its edit and an item whose only change was a prefix
was reported as a failure on every launch. Only the regenerated fields are
carried over; whether those differ from the item's own defaults needs the game's
templates, so that pruning happens on the next restore rather than here.
*/
func migrate(p Profile) Profile {
	if len(p.Items) == 0 {
		p.Items = nil
		return p
	}
	for slotKey, fields := range p.Items {
		itype := int(fields["type"])
		if itype == 0 {
			slot, err := strconv.Atoi(slotKey)
			if err == nil && !hasInt(p.EmptySlots, slot) {
				p.EmptySlots = append(p.EmptySlots, slot)
			}
			continue
		}
		kept := map[string]float64{}
		for name, v := range fields {
			if restorable(name) {
				kept[name] = v
			}
		}
		if len(kept) == 0 {
			continue
		}
		key := strconv.Itoa(itype)
		if p.ItemEdits[key] == nil {
			p.ItemEdits[key] = map[string]float64{}
		}
		for name, v := range kept {
			p.ItemEdits[key][name] = v
		}
	}
	p.Items = nil
	return p
}

// Save writes the profile atomically, so a reader never sees half of one.
func (p Profile) Save() error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("make the profile directory: %w", err)
	}
	raw, err := json.Marshal(p.filled())
	if err != nil {
		return fmt.Errorf("encode the profile: %w", err)
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, raw, 0o644); err != nil { //nolint:gosec // the user's own settings
		return fmt.Errorf("write the profile: %w", err)
	}
	return os.Rename(tmp, path)
}

/*
Update runs a change with the profile held exclusively, re-reading it under the
lock.

The same reason the patch record has one: several invocations can be in flight at
once, and without it the second saves a profile that does not know about the
first.
*/
func Update(change func(*Profile) error) error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("make the profile directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644) //nolint:gosec // a lock beside the profile
	if err != nil {
		return fmt.Errorf("open the profile lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("take the profile lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	p := Load()
	if err := change(&p); err != nil {
		return err
	}
	return p.Save()
}

// SetCheat records, or clears, a desired cheat and its value.
func SetCheat(name string, on bool, value *float64) error {
	return Update(func(p *Profile) error {
		if on {
			p.Cheats[name] = value
		} else {
			delete(p.Cheats, name)
		}
		return nil
	})
}

/*
SetItemEdit records an edit against the item *type*.

fields should already be narrowed to what differs from the item's defaults; an
empty set removes the entry rather than storing something with nothing to
restore.
*/
func SetItemEdit(itemType int32, fields map[string]float64) error {
	kept := map[string]float64{}
	for name, v := range fields {
		if restorable(name) {
			kept[name] = v
		}
	}
	return Update(func(p *Profile) error {
		key := strconv.Itoa(int(itemType))
		if len(kept) == 0 {
			delete(p.ItemEdits, key)
			return nil
		}
		p.ItemEdits[key] = kept
		return nil
	})
}

// ForgetItemEdit drops a saved edit, for when a restore finds nothing left that
// differs.
func ForgetItemEdit(itemType int32) error {
	return Update(func(p *Profile) error {
		delete(p.ItemEdits, strconv.Itoa(int(itemType)))
		return nil
	})
}

// ClearItem records that a slot was emptied.
func ClearItem(slot int) error {
	return Update(func(p *Profile) error {
		if !hasInt(p.EmptySlots, slot) {
			p.EmptySlots = append(p.EmptySlots, slot)
		}
		return nil
	})
}

// SetSellWhitelist adds or removes one item type from the auto-sell whitelist.
func SetSellWhitelist(itemType int32, on bool) error {
	return Update(func(p *Profile) error {
		out := p.SellWhitelist[:0]
		found := false
		for _, t := range p.SellWhitelist {
			if t == itemType {
				found = true
				if !on {
					continue
				}
			}
			out = append(out, t)
		}
		p.SellWhitelist = out
		if on && !found {
			p.SellWhitelist = append(p.SellWhitelist, itemType)
		}
		return nil
	})
}

// SellWhitelist is the item types marked for auto-selling.
func SellWhitelist() []int32 {
	out := append([]int32{}, Load().SellWhitelist...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

/*
RememberRodPower records a rod's original fishing power, once. A later call does
not overwrite it.

Overwriting would be the bug: switch the cheat on twice without a restore in
between and the second call would record the *cheated* power as the original, so
the rod could never be put back.
*/
func RememberRodPower(itemType, power int32) error {
	return Update(func(p *Profile) error {
		key := strconv.Itoa(int(itemType))
		if _, known := p.FishingRestore[key]; !known {
			p.FishingRestore[key] = power
		}
		return nil
	})
}

// RodPowersToRestore is the rods still owed a restore, and what to put back.
func RodPowersToRestore() map[int32]int32 {
	out := map[int32]int32{}
	for key, power := range Load().FishingRestore {
		if t, err := strconv.Atoi(key); err == nil {
			out[int32(t)] = power //nolint:gosec // an item type
		}
	}
	return out
}

// ForgetRodPower drops a rod's note once it has been put back.
func ForgetRodPower(itemType int32) error {
	return Update(func(p *Profile) error {
		delete(p.FishingRestore, strconv.Itoa(int(itemType)))
		return nil
	})
}

// Cheats is the cheats the player wants on, and the values they were given.
func Cheats() map[string]*float64 { return Load().Cheats }

// ItemEdits is every saved edit, by item type.
func ItemEdits() map[int32]map[string]float64 {
	out := map[int32]map[string]float64{}
	for key, fields := range Load().ItemEdits {
		if t, err := strconv.Atoi(key); err == nil {
			out[int32(t)] = fields //nolint:gosec // an item type
		}
	}
	return out
}

// EmptySlots is the slots the player deliberately cleared.
func EmptySlots() []int { return Load().EmptySlots }

// hasInt reports whether a list already has a value.
func hasInt(list []int, want int) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
