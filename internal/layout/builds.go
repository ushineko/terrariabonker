package layout

/*
The game builds this program has seen, as build keys (version.BuildKey: the
game's version and Steam's build id).

A build key is a lookup key -- into the version table, the anchors' ledgers, and
this machine's build decisions -- so it is declared once, here, and every table
names the constant. A misspelt literal is not an error anywhere: it is a key that
quietly matches nothing.
*/
const (
	// Build1457s24825745 is 1.4.5.7, the build the cheats were first derived on.
	Build1457s24825745 = "1.4.5.7+24825745"
	/*
		Build1457s24893155 never existed: 1.4.5.7's version with 1.4.5.8's build
		id, from a version detector since fixed (patch/anchors.go has the story).
		Kept because verifications were recorded under it.
	*/
	Build1457s24893155 = "1.4.5.7+24893155"
	// Build1458s24893155 is 1.4.5.8, the build running on Linux and Windows today.
	Build1458s24893155 = "1.4.5.8+24893155"
)

// SeenBuilds is every declared build key. A key in a table that is not one of
// these was spelt there rather than named.
var SeenBuilds = []string{Build1457s24825745, Build1457s24893155, Build1458s24893155}
