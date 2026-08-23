using System;
using System.Collections.Generic;
using System.Linq;
using System.IO;
using System.Reflection.Metadata;
using System.Reflection.Metadata.Ecma335;
using System.Reflection.PortableExecutable;
using System.Text;

// Dump IL for named methods straight out of Terraria.exe, with tokens resolved to
// readable member names. No external decompiler needed (System.Reflection.Metadata
// only), same approach as tools/extract_item_names.
//
// usage: dotnet run -- <exe> list  <substring>        # find types/methods by name
//        dotnet run -- <exe> il    <Type::Method>...  # dump IL of matching methods
//        dotnet run -- <exe> calls <substring>        # methods whose IL calls a match
//        dotnet run -- <exe> field <Type>             # field list in declaration order

class Program
{
    static MetadataReader md;
    static PEReader pe;

    static int Main(string[] args)
    {
        string exe = args.Length > 0 ? args[0]
            : "/mnt/Data3/SteamLibrary/steamapps/common/Terraria/Terraria.exe";
        string mode = args.Length > 1 ? args[1] : "list";
        using var fs = File.OpenRead(exe);
        pe = new PEReader(fs);
        md = pe.GetMetadataReader();

        var rest = new List<string>();
        for (int i = 2; i < args.Length; i++) rest.Add(args[i]);

        switch (mode)
        {
            case "list": DoList(rest); break;
            case "il": DoIl(rest); break;
            case "calls": DoCalls(rest); break;
            case "field": DoFields(rest); break;
            default: Console.Error.WriteLine("unknown mode"); return 1;
        }
        return 0;
    }

    static string TypeName(TypeDefinition td)
    {
        string ns = md.GetString(td.Namespace);
        string n = md.GetString(td.Name);
        return string.IsNullOrEmpty(ns) ? n : ns + "." + n;
    }

    static void DoList(List<string> pats)
    {
        foreach (var h in md.TypeDefinitions)
        {
            var td = md.GetTypeDefinition(h);
            string tn = TypeName(td);
            foreach (var mh in td.GetMethods())
            {
                var mdf = md.GetMethodDefinition(mh);
                string name = md.GetString(mdf.Name);
                string full = tn + "::" + name;
                bool hit = pats.Count == 0;
                foreach (var p in pats)
                    if (full.IndexOf(p, StringComparison.OrdinalIgnoreCase) >= 0) hit = true;
                if (hit)
                {
                    var body = mdf.RelativeVirtualAddress != 0
                        ? pe.GetMethodBody(mdf.RelativeVirtualAddress) : default;
                    int size = mdf.RelativeVirtualAddress != 0 ? body.GetILContent().Length : 0;
                    Console.WriteLine($"{full}   (IL {size} bytes)");
                }
            }
        }
    }

    static void DoFields(List<string> pats)
    {
        foreach (var h in md.TypeDefinitions)
        {
            var td = md.GetTypeDefinition(h);
            string tn = TypeName(td);
            bool hit = false;
            foreach (var p in pats)
                if (tn.IndexOf(p, StringComparison.OrdinalIgnoreCase) >= 0) hit = true;
            if (!hit) continue;
            Console.WriteLine($"--- {tn} ---");
            int i = 0;
            foreach (var fh in td.GetFields())
            {
                var fd = md.GetFieldDefinition(fh);
                Console.WriteLine($"  [{i++}] {md.GetString(fd.Name)}  {fd.Attributes}");
            }
        }
    }

    static void DoCalls(List<string> pats)
    {
        foreach (var h in md.TypeDefinitions)
        {
            var td = md.GetTypeDefinition(h);
            foreach (var mh in td.GetMethods())
            {
                var mdf = md.GetMethodDefinition(mh);
                if (mdf.RelativeVirtualAddress == 0) continue;
                var body = pe.GetMethodBody(mdf.RelativeVirtualAddress);
                var text = Disassemble(body.GetILContent().ToArray(), false);
                foreach (var p in pats)
                {
                    if (text.IndexOf(p, StringComparison.OrdinalIgnoreCase) >= 0)
                    {
                        Console.WriteLine($"{TypeName(td)}::{md.GetString(mdf.Name)}");
                        break;
                    }
                }
            }
        }
    }

    static void DoIl(List<string> specs)
    {
        foreach (var h in md.TypeDefinitions)
        {
            var td = md.GetTypeDefinition(h);
            string tn = TypeName(td);
            foreach (var mh in td.GetMethods())
            {
                var mdf = md.GetMethodDefinition(mh);
                string full = tn + "::" + md.GetString(mdf.Name);
                bool hit = false;
                foreach (var s in specs)
                    if (full.Equals(s, StringComparison.OrdinalIgnoreCase) ||
                        full.EndsWith("::" + s, StringComparison.OrdinalIgnoreCase)) hit = true;
                if (!hit) continue;
                Console.WriteLine($"=================== {full} ===================");
                if (mdf.RelativeVirtualAddress == 0) { Console.WriteLine("  (no body)"); continue; }
                var body = pe.GetMethodBody(mdf.RelativeVirtualAddress);
                Console.WriteLine(Disassemble(body.GetILContent().ToArray(), true));
            }
        }
    }

    // --- token resolution ---------------------------------------------------
    static string Member(int token)
    {
        try
        {
            var kind = (TableIndex)(token >> 24);
            int rid = token & 0x00FFFFFF;
            if (kind == TableIndex.MethodDef)
            {
                var m = md.GetMethodDefinition(MetadataTokens.MethodDefinitionHandle(rid));
                var dt = md.GetTypeDefinition(m.GetDeclaringType());
                return TypeName(dt) + "::" + md.GetString(m.Name);
            }
            if (kind == TableIndex.Field)
            {
                var f = md.GetFieldDefinition(MetadataTokens.FieldDefinitionHandle(rid));
                var dt = md.GetTypeDefinition(f.GetDeclaringType());
                return TypeName(dt) + "::" + md.GetString(f.Name);
            }
            if (kind == TableIndex.MemberRef)
            {
                var mr = md.GetMemberReference(MetadataTokens.MemberReferenceHandle(rid));
                string parent = "?";
                if (mr.Parent.Kind == HandleKind.TypeReference)
                {
                    var tr = md.GetTypeReference((TypeReferenceHandle)mr.Parent);
                    string ns = md.GetString(tr.Namespace);
                    parent = string.IsNullOrEmpty(ns) ? md.GetString(tr.Name)
                                                      : ns + "." + md.GetString(tr.Name);
                }
                else if (mr.Parent.Kind == HandleKind.TypeDefinition)
                    parent = TypeName(md.GetTypeDefinition((TypeDefinitionHandle)mr.Parent));
                return parent + "::" + md.GetString(mr.Name);
            }
            if (kind == TableIndex.TypeDef)
                return TypeName(md.GetTypeDefinition(MetadataTokens.TypeDefinitionHandle(rid)));
            if (kind == TableIndex.TypeRef)
            {
                var tr = md.GetTypeReference(MetadataTokens.TypeReferenceHandle(rid));
                string ns = md.GetString(tr.Namespace);
                return string.IsNullOrEmpty(ns) ? md.GetString(tr.Name)
                                                : ns + "." + md.GetString(tr.Name);
            }
            if (kind == TableIndex.MemberRef + 0) return $"token:{token:X8}";
            if ((token >> 24) == 0x70)   // UserString
                return "\"" + md.GetUserString(MetadataTokens.UserStringHandle(rid)) + "\"";
            if (kind == TableIndex.MethodSpec)
            {
                var ms = md.GetMethodSpecification(MetadataTokens.MethodSpecificationHandle(rid));
                return Member(MetadataTokens.GetToken(ms.Method)) + "<>";
            }
        }
        catch (Exception e) { return $"token:{token:X8}({e.GetType().Name})"; }
        return $"token:{token:X8}";
    }

    // --- minimal IL disassembler -------------------------------------------
    struct Op { public string Name; public int Size; public bool Tok;
                public Op(string n, int s, bool t = false) { Name = n; Size = s; Tok = t; } }

    static Dictionary<int, Op> One = new Dictionary<int, Op>();
    static Dictionary<int, Op> Two = new Dictionary<int, Op>();

    static Program()
    {
        void A(int c, string n, int s, bool t = false) => One[c] = new Op(n, s, t);
        void B(int c, string n, int s, bool t = false) => Two[c] = new Op(n, s, t);

        A(0x00, "nop", 0); A(0x01, "break", 0);
        A(0x02, "ldarg.0", 0); A(0x03, "ldarg.1", 0); A(0x04, "ldarg.2", 0); A(0x05, "ldarg.3", 0);
        A(0x06, "ldloc.0", 0); A(0x07, "ldloc.1", 0); A(0x08, "ldloc.2", 0); A(0x09, "ldloc.3", 0);
        A(0x0A, "stloc.0", 0); A(0x0B, "stloc.1", 0); A(0x0C, "stloc.2", 0); A(0x0D, "stloc.3", 0);
        A(0x0E, "ldarg.s", 1); A(0x0F, "ldarga.s", 1); A(0x10, "starg.s", 1);
        A(0x11, "ldloc.s", 1); A(0x12, "ldloca.s", 1); A(0x13, "stloc.s", 1);
        A(0x14, "ldnull", 0); A(0x15, "ldc.i4.m1", 0);
        for (int i = 0; i <= 8; i++) A(0x16 + i, "ldc.i4." + i, 0);
        A(0x1F, "ldc.i4.s", 1); A(0x20, "ldc.i4", 4); A(0x21, "ldc.i8", 8);
        A(0x22, "ldc.r4", 4); A(0x23, "ldc.r8", 8);
        A(0x25, "dup", 0); A(0x26, "pop", 0); A(0x27, "jmp", 4, true);
        A(0x28, "call", 4, true); A(0x29, "calli", 4, true); A(0x2A, "ret", 0);
        A(0x2B, "br.s", 1); A(0x2C, "brfalse.s", 1); A(0x2D, "brtrue.s", 1);
        A(0x2E, "beq.s", 1); A(0x2F, "bge.s", 1); A(0x30, "bgt.s", 1); A(0x31, "ble.s", 1);
        A(0x32, "blt.s", 1); A(0x33, "bne.un.s", 1); A(0x34, "bge.un.s", 1); A(0x35, "bgt.un.s", 1);
        A(0x36, "ble.un.s", 1); A(0x37, "blt.un.s", 1);
        A(0x38, "br", 4); A(0x39, "brfalse", 4); A(0x3A, "brtrue", 4);
        A(0x3B, "beq", 4); A(0x3C, "bge", 4); A(0x3D, "bgt", 4); A(0x3E, "ble", 4); A(0x3F, "blt", 4);
        A(0x40, "bne.un", 4); A(0x41, "bge.un", 4); A(0x42, "bgt.un", 4); A(0x43, "ble.un", 4);
        A(0x44, "blt.un", 4); A(0x45, "switch", -1);
        string[] ind = {"ldind.i1","ldind.u1","ldind.i2","ldind.u2","ldind.i4","ldind.u4",
                        "ldind.i8","ldind.i","ldind.r4","ldind.r8","ldind.ref","stind.ref",
                        "stind.i1","stind.i2","stind.i4","stind.i8","stind.r4","stind.r8"};
        for (int i = 0; i < ind.Length; i++) A(0x46 + i, ind[i], 0);
        string[] arith = {"add","sub","mul","div","div.un","rem","rem.un","and","or","xor",
                          "shl","shr","shr.un","neg","not"};
        for (int i = 0; i < arith.Length; i++) A(0x58 + i, arith[i], 0);
        string[] conv = {"conv.i1","conv.i2","conv.i4","conv.i8","conv.r4","conv.r8",
                         "conv.u4","conv.u8"};
        for (int i = 0; i < conv.Length; i++) A(0x67 + i, conv[i], 0);
        A(0x6F, "callvirt", 4, true); A(0x70, "cpobj", 4, true); A(0x71, "ldobj", 4, true);
        A(0x72, "ldstr", 4, true); A(0x73, "newobj", 4, true); A(0x74, "castclass", 4, true);
        A(0x75, "isinst", 4, true); A(0x76, "conv.r.un", 0);
        A(0x79, "unbox", 4, true); A(0x7A, "throw", 0);
        A(0x7B, "ldfld", 4, true); A(0x7C, "ldflda", 4, true); A(0x7D, "stfld", 4, true);
        A(0x7E, "ldsfld", 4, true); A(0x7F, "ldsflda", 4, true); A(0x80, "stsfld", 4, true);
        A(0x81, "stobj", 4, true);
        string[] convovf = {"conv.ovf.i1.un","conv.ovf.i2.un","conv.ovf.i4.un","conv.ovf.i8.un",
                            "conv.ovf.u1.un","conv.ovf.u2.un","conv.ovf.u4.un","conv.ovf.u8.un",
                            "conv.ovf.i.un","conv.ovf.u.un"};
        for (int i = 0; i < convovf.Length; i++) A(0x82 + i, convovf[i], 0);
        A(0x8C, "box", 4, true); A(0x8D, "newarr", 4, true); A(0x8E, "ldlen", 0);
        A(0x8F, "ldelema", 4, true);
        string[] ldelem = {"ldelem.i1","ldelem.u1","ldelem.i2","ldelem.u2","ldelem.i4","ldelem.u4",
                           "ldelem.i8","ldelem.i","ldelem.r4","ldelem.r8","ldelem.ref",
                           "stelem.i","stelem.i1","stelem.i2","stelem.i4","stelem.i8",
                           "stelem.r4","stelem.r8","stelem.ref"};
        for (int i = 0; i < ldelem.Length; i++) A(0x90 + i, ldelem[i], 0);
        A(0xA3, "ldelem", 4, true); A(0xA4, "stelem", 4, true); A(0xA5, "unbox.any", 4, true);
        A(0xB3, "conv.ovf.i1", 0); A(0xB4, "conv.ovf.u1", 0); A(0xB5, "conv.ovf.i2", 0);
        A(0xB6, "conv.ovf.u2", 0); A(0xB7, "conv.ovf.i4", 0); A(0xB8, "conv.ovf.u4", 0);
        A(0xB9, "conv.ovf.i8", 0); A(0xBA, "conv.ovf.u8", 0);
        A(0xC2, "refanyval", 4, true); A(0xC3, "ckfinite", 0); A(0xC6, "mkrefany", 4, true);
        A(0xD0, "ldtoken", 4, true); A(0xD1, "conv.u2", 0); A(0xD2, "conv.u1", 0);
        A(0xD3, "conv.i", 0); A(0xD4, "conv.ovf.i", 0); A(0xD5, "conv.ovf.u", 0);
        A(0xD6, "add.ovf", 0); A(0xD7, "add.ovf.un", 0); A(0xD8, "mul.ovf", 0);
        A(0xD9, "mul.ovf.un", 0); A(0xDA, "sub.ovf", 0); A(0xDB, "sub.ovf.un", 0);
        A(0xDC, "endfinally", 0); A(0xDD, "leave", 4); A(0xDE, "leave.s", 1);
        A(0xDF, "stind.i", 0); A(0xE0, "conv.u", 0);

        B(0x00, "arglist", 0); B(0x01, "ceq", 0); B(0x02, "cgt", 0); B(0x03, "cgt.un", 0);
        B(0x04, "clt", 0); B(0x05, "clt.un", 0); B(0x06, "ldftn", 4, true);
        B(0x07, "ldvirtftn", 4, true); B(0x09, "ldarg", 2); B(0x0A, "ldarga", 2);
        B(0x0B, "starg", 2); B(0x0C, "ldloc", 2); B(0x0D, "ldloca", 2); B(0x0E, "stloc", 2);
        B(0x0F, "localloc", 0); B(0x11, "endfilter", 0); B(0x12, "unaligned.", 1);
        B(0x13, "volatile.", 0); B(0x14, "tail.", 0); B(0x15, "initobj", 4, true);
        B(0x16, "constrained.", 4, true); B(0x17, "cpblk", 0); B(0x18, "initblk", 0);
        B(0x1A, "rethrow", 0); B(0x1C, "sizeof", 4, true); B(0x1D, "refanytype", 0);
        B(0x1E, "readonly.", 0);
    }

    static string Disassemble(byte[] il, bool pretty)
    {
        var sb = new StringBuilder();
        int p = 0;
        while (p < il.Length)
        {
            int off = p;
            int code = il[p++];
            Op op;
            if (code == 0xFE)
            {
                int c2 = il[p++];
                if (!Two.TryGetValue(c2, out op)) { sb.AppendLine($"IL_{off:X4}: <fe {c2:X2}?>"); break; }
            }
            else if (!One.TryGetValue(code, out op))
            {
                sb.AppendLine($"IL_{off:X4}: <{code:X2}?>"); break;
            }

            string arg = "";
            if (op.Size == -1)                       // switch
            {
                int n = BitConverter.ToInt32(il, p); p += 4;
                int endOfInstr = p + 4 * n;          // targets are relative to the END
                var targets = new List<string>();
                for (int i = 0; i < n; i++)
                { targets.Add($"IL_{BitConverter.ToInt32(il, p) + endOfInstr:X4}"); p += 4; }
                arg = "(" + string.Join(", ", targets) + ")";
            }
            else if (op.Size == 1)
            {
                sbyte v = (sbyte)il[p]; p += 1;
                arg = op.Name.StartsWith("b") || op.Name.StartsWith("leave")
                    ? $"IL_{p + v:X4}" : v.ToString();
            }
            else if (op.Size == 2) { arg = BitConverter.ToInt16(il, p).ToString(); p += 2; }
            else if (op.Size == 4)
            {
                int v = BitConverter.ToInt32(il, p); p += 4;
                if (op.Tok) arg = Member(v);
                else if (op.Name.StartsWith("b") || op.Name.StartsWith("leave")) arg = $"IL_{p + v:X4}";
                else if (op.Name == "ldc.r4") arg = BitConverter.ToSingle(BitConverter.GetBytes(v), 0).ToString();
                else arg = v.ToString();
            }
            else if (op.Size == 8)
            {
                if (op.Name == "ldc.r8") arg = BitConverter.ToDouble(il, p).ToString();
                else arg = BitConverter.ToInt64(il, p).ToString();
                p += 8;
            }
            sb.AppendLine($"IL_{off:X4}: {op.Name} {arg}".TrimEnd());
        }
        return sb.ToString();
    }
}
