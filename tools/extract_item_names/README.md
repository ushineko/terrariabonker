# extract_item_names

Regenerates `terrariabonker/data/items.json` (the ItemID → display-name map) by
reading it straight out of the game's own `Terraria.exe`. No external decompile or
community list is needed — and this is the only way to get names for a build so new
that no `ItemID.cs` has been published yet (which was the case for 1.4.5.7).

It reads two things from the managed assembly, via `System.Reflection.Metadata`
(no NuGet dependencies):

1. the `Terraria.ID.ItemID` constant `short` fields → `internalName → id`
2. the embedded `Terraria.Localization.Content.en-US.Items.json` resource →
   `internalName → display name` (Terraria's JSON allows comments and trailing
   commas, so it is parsed leniently)

and joins them into `{ "id": "name" }`.

It can also emit the modifier (prefix) map with `--prefixes`: `Terraria.ID.PrefixID`
consts joined with the `Prefix` localization section → `data/prefixes.json`.

## Usage

```bash
cd tools/extract_item_names
dotnet run -- /path/to/Terraria.exe > /tmp/items.json
# modifier names:
dotnet run -- --prefixes /path/to/Terraria.exe > /tmp/prefixes.json
# then, compacted into the package:
python3 -c "import json;d=json.load(open('/tmp/items.json'));\
json.dump(d,open('../../terrariabonker/data/items.json','w'),separators=(',',':'),sort_keys=True)"
```

Requires the .NET SDK (`dotnet`). The default exe path points at the Steam install
on this machine; pass your own as the first argument.

Extracted from build 1.4.5.7: 6,195 items (max id 6195).
