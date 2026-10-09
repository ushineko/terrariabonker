package layout

/*
The .NET Framework field offsets: the CLR entry's numbers (spec 052).

Read from the runtime by cmd/clrfields against Terraria 1.4.5.8+24893155 on
.NET Framework 4.8.1 (clr.dll 4.8.9345.0), 2026-10-08. Every class's FieldDesc
list was complete -- one record per stored field -- and every record agreed with
the metadata's static flag. The full measurement is docs/clr-fields-1.4.5.8.txt;
TestTheCLRTableIsTheMeasurement checks this table against it.

Grouped by the class that *declares* each field, as cmd/monofields groups the
mono ones: an inherited field is Entity's, not NPC's.

Instance offsets are object-relative, the 4-byte MethodTable pointer included,
so they add straight to an object's address. Main's offsets are within its static
blocks: the references (worldName .. npcFrameCount) in one, the primitives
(time .. gameMenu) in another, located separately (spec 052, "Main (static)").

Nothing reads memory with these yet -- clrEntry is not enabled -- and
cmd/clrfields --verify checks them against a running game.
*/

// CLRField is one field under .NET Framework.
type CLRField struct {
	Name   string
	Offset uint32
	Static bool
}

// CLRFields is the CLR entry's field offsets, by declaring class.
var CLRFields = map[string][]CLRField{
	"Entity": {
		{"whoAmI", 0x0004, false},
		{"direction", 0x000c, false},
		{"width", 0x0010, false},
		{"height", 0x0014, false},
		{"wet", 0x0018, false},
		{"position", 0x0020, false},
		{"velocity", 0x0028, false},
		{"oldPosition", 0x0030, false},
	},
	"Projectile": {
		{"ai", 0x0040, false},
		{"localAI", 0x0044, false},
		{"scale", 0x0078, false},
		{"type", 0x0080, false},
		{"alpha", 0x0084, false},
		{"owner", 0x008c, false},
		{"aiStyle", 0x0098, false},
		{"timeLeft", 0x009c, false},
		{"damage", 0x00a4, false},
		{"knockBack", 0x00b0, false},
		{"penetrate", 0x00b4, false},
		{"maxPenetrate", 0x00b8, false},
		{"extraUpdates", 0x00d0, false},
		{"active", 0x0102, false},
		{"bobber", 0x0104, false},
		{"hostile", 0x0109, false},
		{"friendly", 0x010b, false},
		{"tileCollide", 0x0112, false},
	},
	"Item": {
		{"fishingPole", 0x0048, false},
		{"bait", 0x004c, false},
		{"type", 0x0050, false},
		{"useAnimation", 0x005c, false},
		{"useTime", 0x0060, false},
		{"stack", 0x0064, false},
		{"pick", 0x006c, false},
		{"axe", 0x0070, false},
		{"hammer", 0x0074, false},
		{"tileBoost", 0x0078, false},
		{"damage", 0x0088, false},
		{"knockBack", 0x008c, false},
		{"healLife", 0x0090, false},
		{"healMana", 0x0094, false},
		{"scale", 0x009c, false},
		{"defense", 0x00a4, false},
		{"headSlot", 0x00a8, false},
		{"bodySlot", 0x00ac, false},
		{"legSlot", 0x00b0, false},
		{"rare", 0x00b8, false},
		{"shoot", 0x00bc, false},
		{"shootSpeed", 0x00c0, false},
		{"mana", 0x00d4, false},
		{"buffType", 0x00dc, false},
		{"crit", 0x00ec, false},
		{"armorPenetration", 0x00f0, false},
		{"bonusTagDamage", 0x00f4, false},
		{"reuseDelay", 0x00f8, false},
		{"favorited", 0x010c, false},
		{"accessory", 0x010e, false},
		{"consumable", 0x0110, false},
		{"autoReuse", 0x0111, false},
		{"prefix", 0x012e, false},
		{"melee", 0x012f, false},
		{"magic", 0x0130, false},
		{"ranged", 0x0131, false},
		{"summon", 0x0132, false},
	},
	"NPC": {
		{"type", 0x00f4, false},
		{"damage", 0x0108, false},
		{"defense", 0x010c, false},
		{"lifeMax", 0x0120, false},
		{"netID", 0x014c, false},
		{"active", 0x018c, false},
		{"boss", 0x01d1, false},
		{"townNPC", 0x01d6, false},
		{"color", 0x0208, false},
	},
	"Player": {
		{"name", 0x008c, false},
		{"buffType", 0x00c8, false},
		{"buffTime", 0x00cc, false},
		{"inventory", 0x00d4, false},
		{"bank", 0x00e0, false},
		{"bank2", 0x00e4, false},
		{"statLifeMax", 0x0468, false},
		{"statLifeMax2", 0x046c, false},
		{"statLife", 0x0470, false},
		{"statMana", 0x0474, false},
		{"statManaMax", 0x0478, false},
		{"statManaMax2", 0x047c, false},
		{"pickSpeed", 0x0514, false},
		{"wallSpeed", 0x0518, false},
		{"tileSpeed", 0x051c, false},
		{"blockRange", 0x0570, false},
		{"itemAnimation", 0x068c, false},
		{"active", 0x070e, false},
	},
	"Main": {
		{"worldName", 0x05f8, true},
		{"tile", 0x0810, true},
		{"npc", 0x0824, true},
		{"projectile", 0x0830, true},
		{"recipe", 0x0870, true},
		{"player", 0x0878, true},
		{"npcFrameCount", 0x0918, true},
		{"time", 0x1058, true},
		{"maxTilesX", 0x1228, true},
		{"maxTilesY", 0x122c, true},
		{"myPlayer", 0x1384, true},
		{"netMode", 0x1414, true},
		{"gamePaused", 0x160a, true},
		{"gameMenu", 0x1656, true},
	},
}
