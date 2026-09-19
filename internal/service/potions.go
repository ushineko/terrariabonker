package service

import (
	"github.com/ushineko/terrariabonker/internal/buffs"
)

/*
PotionTick renews the buff of every favorited potion in the inventory. One round.

Stateless on purpose, unlike the vein watcher: there is nothing to remember
between rounds, and re-reading the inventory each time is what makes favoriting a
potion take effect immediately rather than at the next rescan.
*/
func (s *Service) PotionTick(minStack, ticks int32) (map[string]any, error) {
	if ticks <= 0 {
		ticks = buffs.DefaultTicks
	}
	inv, err := s.liveInventory()
	if err != nil {
		return nil, err
	}
	live, err := s.LiveBlock()
	if err != nil {
		return nil, err
	}
	bar := buffs.New(s.Mem, live.LifeAddr)

	out := map[string][]map[string]int32{
		buffs.Added: {}, buffs.Renewed: {}, buffs.Kept: {}, buffs.Full: {},
	}
	carried := 0
	for _, potion := range inv.FavoritedPotions(minStack) {
		what, err := bar.Renew(potion.Buff, ticks)
		if err != nil {
			return nil, &Error{Message: err.Error()}
		}
		out[what] = append(out[what], map[string]int32{
			"slot": int32(potion.Slot), "buff": potion.Buff, //nolint:gosec // a slot index
		})
		carried++
	}
	result := map[string]any{"ticks": ticks, "min_stack": minStack, "carried": carried}
	for what, rows := range out {
		result[what] = rows
	}
	return result, nil
}
