using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection.Metadata;
using System.Reflection.PortableExecutable;
using System.Text;
using System.Text.Json;

// Extract an authoritative ItemID -> display-name map straight from Terraria.exe,
// with no dependency on any external decompile:
//   * ItemID const short fields     -> internalName -> id   (assembly metadata)
//   * embedded en-US.Items.json     -> internalName -> name (localization resource)
// then join and print {"id": "name", ...} JSON to stdout. Diagnostics go to stderr.
//
// Usage: dotnet run -- <path-to-Terraria.exe> > items.json
// Regenerate terrariabonker/terrariabonker/data/items.json after a game update.

class Program
{
    static int Main(string[] args)
    {
        bool prefixes = Array.IndexOf(args, "--prefixes") >= 0;
        string exe = Array.Find(args, a => !a.StartsWith("--"))
            ?? "/mnt/Data3/SteamLibrary/steamapps/common/Terraria/Terraria.exe";
        using var fs = File.OpenRead(exe);
        using var pe = new PEReader(fs);
        var md = pe.GetMetadataReader();

        if (prefixes)
        {
            var pid = ReadIdConsts(md, "PrefixID");
            Console.Error.WriteLine($"PrefixID consts: {pid.Count}");
            var pname = ReadLocalization(pe, md, "Prefix");
            Console.Error.WriteLine($"localization Prefix entries: {pname.Count}");
            var pmap = new SortedDictionary<string, string>(StringComparer.Ordinal);
            foreach (var kv in pid)
            {
                pname.TryGetValue(kv.Key, out var disp);
                pmap[kv.Value.ToString()] = string.IsNullOrEmpty(disp) ? Spaced(kv.Key) : disp;
            }
            Console.WriteLine(JsonSerializer.Serialize(pmap,
                new JsonSerializerOptions { WriteIndented = false }));
            Console.Error.WriteLine($"joined id->prefix: {pmap.Count}");
            return 0;
        }

        var idByInternal = ReadItemIdConsts(md);
        Console.Error.WriteLine($"ItemID consts: {idByInternal.Count}");

        var displayByInternal = ReadItemNames(pe, md);
        Console.Error.WriteLine($"localization ItemName entries: {displayByInternal.Count}");

        var outMap = new SortedDictionary<string, string>(StringComparer.Ordinal);
        foreach (var kv in idByInternal)
        {
            displayByInternal.TryGetValue(kv.Key, out var disp);
            outMap[kv.Value.ToString()] = string.IsNullOrEmpty(disp) ? Spaced(kv.Key) : disp;
        }
        Console.WriteLine(JsonSerializer.Serialize(outMap,
            new JsonSerializerOptions { WriteIndented = false }));
        Console.Error.WriteLine($"joined id->name: {outMap.Count}");
        return 0;
    }

    static Dictionary<string, int> ReadItemIdConsts(MetadataReader md)
    {
        var map = new Dictionary<string, int>();
        foreach (var th in md.TypeDefinitions)
        {
            var td = md.GetTypeDefinition(th);
            if (md.GetString(td.Name) != "ItemID") continue;
            if (md.GetString(td.Namespace) != "Terraria.ID") continue;
            foreach (var fh in td.GetFields())
            {
                var fd = md.GetFieldDefinition(fh);
                var cvh = fd.GetDefaultValue();
                if (cvh.IsNil) continue;
                var cv = md.GetConstant(cvh);
                if (cv.TypeCode != ConstantTypeCode.Int16) continue;
                short val = md.GetBlobReader(cv.Value).ReadInt16();
                if (val < 1) continue;
                string name = md.GetString(fd.Name);
                if (name == "Count") continue;               // not an item
                map[name] = val;
            }
        }
        return map;
    }

    // Generalized const reader: Terraria.ID.<typeName> fields (byte/short/int) -> name->id.
    static Dictionary<string, int> ReadIdConsts(MetadataReader md, string typeName)
    {
        var map = new Dictionary<string, int>();
        foreach (var th in md.TypeDefinitions)
        {
            var td = md.GetTypeDefinition(th);
            if (md.GetString(td.Name) != typeName) continue;
            if (md.GetString(td.Namespace) != "Terraria.ID") continue;
            foreach (var fh in td.GetFields())
            {
                var fd = md.GetFieldDefinition(fh);
                var cvh = fd.GetDefaultValue();
                if (cvh.IsNil) continue;
                var cv = md.GetConstant(cvh);
                var br = md.GetBlobReader(cv.Value);
                int val = cv.TypeCode switch
                {
                    ConstantTypeCode.Byte => br.ReadByte(),
                    ConstantTypeCode.SByte => br.ReadSByte(),
                    ConstantTypeCode.Int16 => br.ReadInt16(),
                    ConstantTypeCode.Int32 => br.ReadInt32(),
                    _ => -1,
                };
                if (val < 1) continue;
                string name = md.GetString(fd.Name);
                if (name == "Count") continue;
                map[name] = val;
            }
        }
        return map;
    }

    // Read a named localization section (e.g. "Prefix") from any embedded en-US.*.json.
    static Dictionary<string, string> ReadLocalization(PEReader pe, MetadataReader md, string section)
    {
        var result = new Dictionary<string, string>();
        var resDir = pe.PEHeaders.CorHeader.ResourcesDirectory;
        var data = pe.GetSectionData(resDir.RelativeVirtualAddress);
        foreach (var rh in md.ManifestResources)
        {
            var r = md.GetManifestResource(rh);
            if (!r.Implementation.IsNil) continue;
            var name = md.GetString(r.Name);
            if (!name.StartsWith("Terraria.Localization.Content.en-US.") || !name.EndsWith(".json"))
                continue;
            var reader = data.GetReader((int)r.Offset, data.Length - (int)r.Offset);
            int len = reader.ReadInt32();
            var json = Encoding.UTF8.GetString(reader.ReadBytes(len));
            using var doc = JsonDocument.Parse(json, new JsonDocumentOptions
            {
                CommentHandling = JsonCommentHandling.Skip,
                AllowTrailingCommas = true,
            });
            var root = doc.RootElement;
            if (root.TryGetProperty(section, out var obj) && obj.ValueKind == JsonValueKind.Object)
                foreach (var p in obj.EnumerateObject())
                    if (p.Value.ValueKind == JsonValueKind.String)
                        result[p.Name] = p.Value.GetString();
            foreach (var p in root.EnumerateObject())
                if (p.Name.StartsWith(section + ".") && p.Value.ValueKind == JsonValueKind.String)
                    result[p.Name.Substring(section.Length + 1)] = p.Value.GetString();
        }
        return result;
    }

    static Dictionary<string, string> ReadItemNames(PEReader pe, MetadataReader md)
    {
        var result = new Dictionary<string, string>();
        var resDir = pe.PEHeaders.CorHeader.ResourcesDirectory;
        var section = pe.GetSectionData(resDir.RelativeVirtualAddress);
        foreach (var rh in md.ManifestResources)
        {
            var r = md.GetManifestResource(rh);
            if (!r.Implementation.IsNil) continue;            // embedded only
            if (md.GetString(r.Name) != "Terraria.Localization.Content.en-US.Items.json")
                continue;
            var reader = section.GetReader((int)r.Offset, section.Length - (int)r.Offset);
            int len = reader.ReadInt32();                     // 4-byte length prefix
            var json = Encoding.UTF8.GetString(reader.ReadBytes(len));
            // Terraria's localization JSON allows comments and trailing commas.
            using var doc = JsonDocument.Parse(json, new JsonDocumentOptions
            {
                CommentHandling = JsonCommentHandling.Skip,
                AllowTrailingCommas = true,
            });
            var root = doc.RootElement;
            if (root.TryGetProperty("ItemName", out var itemName)
                && itemName.ValueKind == JsonValueKind.Object)
                foreach (var p in itemName.EnumerateObject())
                    if (p.Value.ValueKind == JsonValueKind.String)
                        result[p.Name] = p.Value.GetString();
            foreach (var p in root.EnumerateObject())          // flat "ItemName.X" fallback
                if (p.Name.StartsWith("ItemName.") && p.Value.ValueKind == JsonValueKind.String)
                    result[p.Name.Substring("ItemName.".Length)] = p.Value.GetString();
        }
        return result;
    }

    static string Spaced(string s)
    {
        var sb = new StringBuilder();
        for (int i = 0; i < s.Length; i++)
        {
            if (i > 0 && char.IsUpper(s[i]) && (char.IsLower(s[i - 1]) || char.IsDigit(s[i - 1])))
                sb.Append(' ');
            sb.Append(s[i]);
        }
        return sb.ToString();
    }
}
