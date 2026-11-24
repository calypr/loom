package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type mvFile struct {
	path string
	src  []byte
}

type mvDecl struct {
	Kind     string
	Name     string
	Receiver string
	File     string
	Hash     string
	Loose    string
}

func declKey(kind, name, receiver string) string {
	return kind + "\x00" + lowerFirst(name) + "\x00" + lowerFirst(receiver)
}

func runMoveverify(root, baseRef string) error {
	baseFiles, err := gitBaseFiles(root, baseRef)
	if err != nil {
		return err
	}
	var baseInput []mvFile
	for _, p := range baseFiles {
		src, err := gitShow(root, baseRef, p)
		if err != nil {
			return err
		}
		baseInput = append(baseInput, mvFile{path: p, src: src})
	}
	headInput, err := headFiles(root)
	if err != nil {
		return err
	}
	base, err := extractDecls(baseInput)
	if err != nil {
		return fmt.Errorf("base %s: %w", baseRef, err)
	}
	head, err := extractDecls(headInput)
	if err != nil {
		return fmt.Errorf("HEAD: %w", err)
	}
	missing, added, changed := compareDecls(base, head)
	if len(missing)+len(added)+len(changed) == 0 {
		fmt.Printf("moveverify: no declaration differences (%d base declarations, %d head declarations)\n", len(base), len(head))
		return nil
	}
	for _, d := range missing {
		fmt.Printf("missing: %s %s (%s)\n", d.Kind, d.Name, d.File)
	}
	for _, d := range added {
		fmt.Printf("added: %s %s (%s)\n", d.Kind, d.Name, d.File)
	}
	for _, c := range changed {
		fmt.Printf("changed: %s %s (%s -> %s)\n", c.base.Kind, c.base.Name, c.base.File, c.head.File)
	}
	return fmt.Errorf("moveverify: %d missing, %d added, %d changed", len(missing), len(added), len(changed))
}

func gitBaseFiles(root, ref string) ([]string, error) {
	cmd := exec.Command("git", "ls-tree", "-r", "--name-only", ref, "--", "internal")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %s: %w", ref, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasSuffix(line, ".go") || strings.HasSuffix(line, "_test.go") {
			continue
		}
		files = append(files, line)
	}
	sort.Strings(files)
	return files, nil
}

func gitShow(root, ref, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref+":"+path)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", ref, path, err)
	}
	return out, nil
}

func headFiles(root string) ([]mvFile, error) {
	var files []mvFile
	base := filepath.Join(root, "internal")
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, mvFile{path: filepath.ToSlash(rel), src: src})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

func extractDecls(files []mvFile) ([]mvDecl, error) {
	type typeUnit struct {
		hash     string
		loose    string
		methods  []string
		lmethods []string
	}
	byDir := map[string][]mvFile{}
	for _, f := range files {
		dir := filepath.Dir(f.path)
		byDir[dir] = append(byDir[dir], f)
	}
	var dirs []string
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var decls []mvDecl
	for _, dir := range dirs {
		types := map[string]*typeUnit{}
		typeFile := map[string]string{}
		var methods []struct {
			recv  string
			hash  string
			loose string
			name  string
			file  string
		}
		for _, f := range byDir[dir] {
			fset := token.NewFileSet()
			af, err := parser.ParseFile(fset, f.path, f.src, 0)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", f.path, err)
			}
			aliases := importAliases(af)
			for _, d := range af.Decls {
				switch d := d.(type) {
				case *ast.GenDecl:
					switch d.Tok {
					case token.TYPE:
						for _, spec := range d.Specs {
							ts := spec.(*ast.TypeSpec)
							name := ts.Name.Name
							normalizeNode(ts, aliases, nil)
							h := hashNode(fset, ts)
							types[name] = &typeUnit{hash: h, loose: looseHashNode(fset, ts)}
							typeFile[name] = f.path
						}
					case token.VAR, token.CONST:
						kind := "var"
						if d.Tok == token.CONST {
							kind = "const"
						}
						for _, spec := range d.Specs {
							vs := spec.(*ast.ValueSpec)
							names := make([]string, 0, len(vs.Names))
							for _, n := range vs.Names {
								names = append(names, n.Name)
							}
							normalizeNode(vs, aliases, nil)
							h := hashNode(fset, vs)
							lh := looseHashNode(fset, vs)
							for _, n := range names {
								decls = append(decls, mvDecl{Kind: kind, Name: n, File: f.path, Hash: h, Loose: lh})
							}
						}
					}
				case *ast.FuncDecl:
					if d.Recv == nil {
						name := d.Name.Name
						normalizeNode(d, aliases, localNames(d))
						decls = append(decls, mvDecl{Kind: "func", Name: name, File: f.path, Hash: hashNode(fset, d), Loose: looseHashNode(fset, d)})
						continue
					}
					recv := receiverBase(d.Recv.List[0].Type)
					name := d.Name.Name
					normalizeNode(d, aliases, localNames(d))
					methods = append(methods, struct {
						recv  string
						hash  string
						loose string
						name  string
						file  string
					}{recv: recv, hash: hashNode(fset, d), loose: looseHashNode(fset, d), name: name, file: f.path})
				}
			}
		}
		for _, m := range methods {
			if t := types[m.recv]; t != nil {
				t.methods = append(t.methods, m.hash)
				t.lmethods = append(t.lmethods, m.loose)
			} else {
				decls = append(decls, mvDecl{Kind: "method", Name: m.recv + "." + m.name, Receiver: m.recv, File: m.file, Hash: m.hash, Loose: m.loose})
			}
		}
		for name, t := range types {
			sort.Strings(t.methods)
			parts := append([]string{t.hash}, t.methods...)
			sort.Strings(t.lmethods)
			lparts := append([]string{t.loose}, t.lmethods...)
			decls = append(decls, mvDecl{Kind: "type", Name: name, File: typeFile[name], Hash: hashString(strings.Join(parts, "\n")), Loose: hashString(strings.Join(lparts, "\n"))})
		}
	}
	return decls, nil
}

func importAliases(f *ast.File) map[string]bool {
	aliases := map[string]bool{}
	for _, imp := range f.Imports {
		if imp.Name != nil {
			if imp.Name.Name != "_" && imp.Name.Name != "." {
				aliases[imp.Name.Name] = true
			}
			continue
		}
		path := strings.Trim(imp.Path.Value, "\"")
		if i := strings.LastIndex(path, "/"); i >= 0 {
			path = path[i+1:]
		}
		aliases[path] = true
	}
	return aliases
}

func normalizeNode(n ast.Node, aliases map[string]bool, locals map[string]bool) {
	ast.Inspect(n, func(x ast.Node) bool {
		if sel, ok := x.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && aliases[id.Name] && !locals[id.Name] {
				id.Name = "pkg"
			}
		}
		if id, ok := x.(*ast.Ident); ok {
			id.Name = lowerFirst(id.Name)
		}
		return true
	})
}

func localNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	for _, f := range fn.Type.Params.List {
		for _, n := range f.Names {
			out[n.Name] = true
		}
	}
	if fn.Type.Results != nil {
		for _, f := range fn.Type.Results.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	if fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, e := range x.Lhs {
					if id, ok := e.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range x.Names {
				out[id.Name] = true
			}
		case *ast.RangeStmt:
			if id, ok := x.Key.(*ast.Ident); ok {
				out[id.Name] = true
			}
			if id, ok := x.Value.(*ast.Ident); ok {
				out[id.Name] = true
			}
		case *ast.FuncLit:
			for _, f := range x.Type.Params.List {
				for _, id := range f.Names {
					out[id.Name] = true
				}
			}
			if x.Type.Results != nil {
				for _, f := range x.Type.Results.List {
					for _, id := range f.Names {
						out[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return out
}

func hashNode(fset *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return hashString(buf.String())
}

func looseHashNode(fset *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	var sc scanner.Scanner
	file := token.NewFileSet().AddFile("", -1, len(buf.Bytes()))
	sc.Init(file, buf.Bytes(), nil, 0)
	var out []string
	var toks []token.Token
	var lits []string
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		_ = pos
		if tok == token.COMMENT || tok == token.SEMICOLON {
			continue
		}
		toks = append(toks, tok)
		lits = append(lits, lit)
	}
	for i := 0; i < len(toks); i++ {
		if toks[i] == token.PERIOD {
			continue
		}
		if toks[i] == token.IDENT && i+1 < len(toks) && toks[i+1] == token.PERIOD {
			continue
		}
		if toks[i] == token.IDENT {
			out = append(out, lowerFirst(lits[i]))
			continue
		}
		out = append(out, string(lits[i]))
	}
	return hashString(strings.Join(out, "\x00"))
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:])
}

func compareDecls(base, head []mvDecl) (missing, added []mvDecl, changed []struct{ base, head mvDecl }) {
	baseMap := map[string][]mvDecl{}
	headMap := map[string][]mvDecl{}
	for _, d := range base {
		k := declKey(d.Kind, d.Name, d.Receiver)
		baseMap[k] = append(baseMap[k], d)
	}
	for _, d := range head {
		k := declKey(d.Kind, d.Name, d.Receiver)
		headMap[k] = append(headMap[k], d)
	}
	var keys []string
	seen := map[string]bool{}
	for k := range baseMap {
		keys = append(keys, k)
		seen[k] = true
	}
	for k := range headMap {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		bs := append([]mvDecl{}, baseMap[k]...)
		hs := append([]mvDecl{}, headMap[k]...)
		for i := 0; i < len(bs); {
			matched := -1
			for j := 0; j < len(hs); j++ {
				if bs[i].Hash == hs[j].Hash || (bs[i].Loose != "" && bs[i].Loose == hs[j].Loose) {
					matched = j
					break
				}
			}
			if matched >= 0 {
				bs = append(bs[:i], bs[i+1:]...)
				hs = append(hs[:matched], hs[matched+1:]...)
				continue
			}
			i++
		}
		for len(bs) > 0 && len(hs) > 0 {
			changed = append(changed, struct{ base, head mvDecl }{base: bs[0], head: hs[0]})
			bs = bs[1:]
			hs = hs[1:]
		}
		missing = append(missing, bs...)
		added = append(added, hs...)
	}
	sort.Slice(missing, func(i, j int) bool {
		return missing[i].File+"\x00"+missing[i].Name < missing[j].File+"\x00"+missing[j].Name
	})
	sort.Slice(added, func(i, j int) bool { return added[i].File+"\x00"+added[i].Name < added[j].File+"\x00"+added[j].Name })
	sort.Slice(changed, func(i, j int) bool {
		return changed[i].base.File+"\x00"+changed[i].base.Name < changed[j].base.File+"\x00"+changed[j].base.Name
	})
	return
}
