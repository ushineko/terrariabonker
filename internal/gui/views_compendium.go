package gui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	fd "github.com/ushineko/fynedesygn"
	"github.com/ushineko/fynedesygn/dialogs"
	"github.com/ushineko/fynedesygn/table"
	"github.com/ushineko/fynedesygn/widgets"

	"github.com/ushineko/terrariabonker/internal/gui/client"
)

// anyKind is the "no filter" entry of the kind picker.
const anyKind = "Everything"

// spawnDistance is how far away a spawned NPC appears, in tiles. Far enough not
// to land on the player, near enough to walk to.
const spawnDistance = 10

// compendiumState is the catalog and what the section is currently showing of it.
type compendiumState struct {
	entries []catalogEntry
	kinds   []string
	// kind and filter are the two controls' values, kept on the window so
	// rebuilding the section does not clear a search someone is in the middle
	// of typing.
	kind, filter string
	picked       int // index into the visible rows, -1 for none
}

/*
catalogEntry is one row of the compendium: an item or an NPC, flattened.

Both are drawn in one table because a player looking for "what is a Duke Fishron
and what does it drop" does not care which of the game's two catalogs it came
out of.
*/
type catalogEntry struct {
	ID    int
	NetID int
	Name  string
	Kind  string
	Wiki  string
	IsNPC bool
	Stats map[string]float64
}

// buildCompendium is everything the game knows about, searchable.
func (u *ui) buildCompendium() fyne.CanvasObject {
	kinds := append([]string{anyKind}, u.cp.kinds...)
	kind := widget.NewSelect(kinds, nil)
	if u.cp.kind == "" {
		u.cp.kind = anyKind
	}
	kind.SetSelected(u.cp.kind)

	filter := widget.NewEntry()
	filter.SetPlaceHolder("filter by name or ID")
	filter.SetText(u.cp.filter)

	rows := u.visibleEntries()
	count := widget.NewLabel(fmt.Sprintf("%d of %d entries match", len(rows), len(u.cp.entries)))

	refresh := func() {
		rows = u.visibleEntries()
		count.SetText(fmt.Sprintf("%d of %d entries match", len(rows), len(u.cp.entries)))
		u.cp.picked = -1
		u.sh.Refresh()
	}
	kind.OnChanged = func(v string) { u.cp.kind = v; refresh() }
	filter.OnChanged = func(v string) { u.cp.filter = v; refresh() }

	give := widget.NewButton("Give one", func() { u.giveOrSpawn(rows) })
	wiki := widget.NewButton("Open the wiki", func() { u.openWiki(rows) })
	rescan := widget.NewButton("Re-scan from the game", func() { u.rescanCompendium() })
	u.sh.Gate(give, wiki, rescan)

	body := u.compendiumTable(rows)
	head := container.NewBorder(nil, nil, widgets.FixedWidth(kind, kindWidth), nil, filter)

	return container.NewBorder(
		container.NewVBox(
			widgets.Heading("Compendium", "Browse every item and NPC."),
			head,
		),
		container.NewVBox(
			container.NewHBox(count, give, wiki,
				widgets.WithTip(rescan, "Stats are read from the game once and cached per "+
					"build. Re-scan after a game update.")),
			widgets.FixedHeight(u.logWidget(), logHeight),
		),
		nil, nil, body)
}

/*
kindWidth is the kind picker's width, wide enough for the longest kind the
catalog reports.

rowHeight is shorter than the table's own default, which is sized for a 64px
thumbnail. An item icon is 32px in the game and in the Qt panel, and the catalog
has some 7,000 rows: a taller row buys nothing and costs three of the nine rows
that fit on screen.
*/
const (
	kindWidth float32 = 200
	rowHeight float32 = 48
)

/*
compendiumTable draws the visible rows.

The library's detail table, with the icon as its thumbnail column. A row is one
entry and one verdict, which is what that table is for.
*/
func (u *ui) compendiumTable(rows []catalogEntry) fyne.CanvasObject {
	if len(u.cp.entries) == 0 {
		return container.NewVScroll(widgets.Card("Catalog",
			widgets.Wrapped("The catalog has not been read yet."),
			widgets.DimWrapped("It comes from the game, so Terraria has to be running. "+
				"The first read scans the item templates and takes a second or two."),
		))
	}

	t := table.New()
	t.Header("", "Name", "Kind", "Damage", "Defense", "Life", "Rarity", "ID")
	t.SetWidths(table.ThumbCellSize, 320, 150, 90, 90, 90, 90, 80)

	thumbs := make([][]byte, 0, len(rows))
	for _, e := range rows {
		t.Row(fd.StatusInfo, "", e.Name, e.Kind,
			statText(e.Stats, "damage"), statText(e.Stats, "defense"),
			statText(e.Stats, "life"), statText(e.Stats, "rare"),
			strconv.Itoa(e.ID))
		thumbs = append(thumbs, u.thumbFor(e))
	}
	t.SetThumbnails(0, thumbs)

	w := t.Widget()
	w.SetRowHeight(-1, rowHeight)
	for i := range rows {
		w.SetRowHeight(i, rowHeight)
	}
	w.OnSelected = func(id widget.TableCellID) { u.cp.picked = id.Row - 1 }
	return w
}

// thumbFor is an entry's icon, or nil when the cache has not got one. NPCs and
// items share the cache but not the namespace, so their ids do not collide.
func (u *ui) thumbFor(e catalogEntry) []byte {
	name := "Item_" + strconv.Itoa(e.ID) + ".png"
	if e.IsNPC {
		name = "NPC_" + strconv.Itoa(e.ID) + ".png"
	}
	return u.sprites.raw(name)
}

// statText renders one stat, or an em dash when the game never reported it: a
// damage of zero and a damage nobody read are different facts.
func statText(stats map[string]float64, key string) string {
	v, ok := client.Stat(stats, key)
	if !ok {
		return "—"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

/*
visibleEntries is the catalog narrowed by the two controls.

Matched on name and on id, because a player who knows an item by its number --
from a wiki page, or from this window's own cells -- should be able to type it.
*/
func (u *ui) visibleEntries() []catalogEntry {
	want := strings.ToLower(strings.TrimSpace(u.cp.filter))
	out := make([]catalogEntry, 0, len(u.cp.entries))
	for _, e := range u.cp.entries {
		if u.cp.kind != anyKind && u.cp.kind != "" && e.Kind != u.cp.kind {
			continue
		}
		if want != "" &&
			!strings.Contains(strings.ToLower(e.Name), want) &&
			!strings.Contains(strconv.Itoa(e.ID), want) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// giveOrSpawn acts on the picked row: an item is handed over, an NPC is spawned.
func (u *ui) giveOrSpawn(rows []catalogEntry) {
	e, ok := pick(rows, u.cp.picked)
	if !ok {
		u.sh.Flash("Pick a row first.", fd.StatusWarn)
		return
	}
	if e.IsNPC {
		u.do("Spawning "+e.Name, client.SpawnNPCArgv(e.NetID, spawnDistance))
		return
	}
	u.do("Giving "+e.Name, client.GiveArgv(e.ID, 1))
}

// openWiki opens the picked row's wiki page in the desktop's browser.
func (u *ui) openWiki(rows []catalogEntry) {
	e, ok := pick(rows, u.cp.picked)
	if !ok {
		u.sh.Flash("Pick a row first.", fd.StatusWarn)
		return
	}
	if e.Wiki == "" {
		u.sh.Flash("There is no wiki page for "+e.Name+".", fd.StatusWarn)
		return
	}
	if err := dialogs.OpenPath(e.Wiki); err != nil {
		u.note("could not open the wiki: " + err.Error())
	}
}

// pick reads the selected row, if there is one.
func pick(rows []catalogEntry, i int) (catalogEntry, bool) {
	if i < 0 || i >= len(rows) {
		return catalogEntry{}, false
	}
	return rows[i], true
}

// rescanCompendium reads the catalog again, for after a game update.
func (u *ui) rescanCompendium() {
	u.sh.Perform("Re-scanning the catalog...", func(ctx context.Context) error {
		out, err := u.run(ctx, client.CompendiumArgv(true))
		cat, ok := client.ParseCompendium(out)
		fyne.Do(func() {
			if !ok {
				u.note("catalog: " + firstLine(detail(out, err)))
				return
			}
			u.takeCatalog(cat)
			u.sh.Flash("Catalog re-read.", fd.StatusGood)
			u.sh.Refresh()
		})
		return nil
	})
}

/*
takeCatalog stores a freshly read catalog.

One list of items and NPCs together, sorted by name, with the kinds collected
for the picker. Sorted here rather than per draw: the catalog is read once and
filtered often.
*/
func (u *ui) takeCatalog(cat *client.Compendium) {
	entries := make([]catalogEntry, 0, len(cat.Items)+len(cat.NPCs))
	names := make(map[int]string, len(cat.Items))
	kinds := map[string]bool{}

	for _, it := range cat.Items {
		names[it.ID] = it.Name
		entries = append(entries, catalogEntry{
			ID: it.ID, Name: it.Name, Kind: it.Kind, Wiki: it.Wiki, Stats: it.Stats,
		})
		kinds[it.Kind] = true
	}
	for _, n := range cat.NPCs {
		entries = append(entries, catalogEntry{
			ID: n.ID, NetID: n.NetID, Name: n.Name, Kind: n.Kind, Wiki: n.Wiki,
			IsNPC: true, Stats: n.Stats,
		})
		kinds[n.Kind] = true
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	list := make([]string, 0, len(kinds))
	for k := range kinds {
		if k != "" {
			list = append(list, k)
		}
	}
	sort.Strings(list)

	u.cp.entries, u.cp.kinds, u.cp.picked = entries, list, -1
	u.iv.names, u.iv.namesOK = names, true
	u.redrawAllCells()
}
