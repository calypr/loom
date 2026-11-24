package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
)

type gcEdge struct {
	srcFile string
	srcObj  types.Object
	target  types.Object
}

type gcDecl struct {
	start int
	end   int
	objs  []types.Object
}

func runGroupcheck(root, dir, selectorText string) error {
	sel, err := parseSelector(selectorText)
	if err != nil {
		return err
	}
	absDir := dir
	if !filepath.IsAbs(absDir) {
		absDir = filepath.Join(root, dir)
	}
	packages, err := goList(absDir, "-export", "-deps", "-compiled", "-json", ".")
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return fmt.Errorf("no Go package in %s", absDir)
	}
	listed := packages[len(packages)-1]
	fileBases := append(listed.GoFiles, listed.CgoFiles...)
	if len(fileBases) == 0 {
		return fmt.Errorf("no active production Go files in %s", absDir)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range listed.CompiledGoFiles {
		if !filepath.IsAbs(name) {
			name = filepath.Join(listed.Dir, name)
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return err
		}
		files = append(files, f)
	}
	info := &types.Info{
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: newExportImporter(fset, exportMap(packages))}
	pkg, err := conf.Check(listed.ImportPath, fset, files, info)
	if err != nil {
		return err
	}

	declFile := map[types.Object]string{}
	declName := map[types.Object]string{}
	sourceNames := make(map[string]bool, len(fileBases))
	for _, name := range fileBases {
		sourceNames[name] = true
	}
	sourceNameAt := func(pos token.Pos) string {
		name := filepath.Base(fset.PositionFor(pos, false).Filename)
		if sourceNames[name] {
			return name
		}
		name = filepath.Base(fset.PositionFor(pos, true).Filename)
		if sourceNames[name] {
			return name
		}
		return ""
	}
	declFileByObj := func(obj types.Object) string {
		if obj == nil || !obj.Pos().IsValid() {
			return ""
		}
		return sourceNameAt(obj.Pos())
	}
	for obj := range info.Defs {
		o := info.Defs[obj]
		if o == nil {
			continue
		}
		if file := declFileByObj(o); file != "" {
			declFile[o] = file
			declName[o] = o.Name()
		}
	}

	fileSet := map[string]bool{}
	objSet := map[types.Object]bool{}
	switch sel.kind {
	case "prefix":
		for _, b := range fileBases {
			if prefixToken(b) == sel.value {
				fileSet[b] = true
			}
		}
	case "file":
		for _, b := range fileBases {
			if sel.matchesFile(b) {
				fileSet[b] = true
			}
		}
	case "name", "list":
		for o := range declFile {
			if sel.matchesName(declName[o]) {
				objSet[o] = true
			}
		}
		var typeNames []string
		for o := range objSet {
			if _, ok := o.(*types.TypeName); ok {
				typeNames = append(typeNames, o.Name())
			}
		}
		for o := range declFile {
			if fn, ok := o.(*types.Func); ok {
				if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
					recv := namedRecvName(sig.Recv().Type())
					for _, tn := range typeNames {
						if recv == tn {
							objSet[o] = true
						}
					}
				}
			}
		}
	}

	declsByFile := map[string][]gcDecl{}
	for _, f := range files {
		tf := fset.File(f.Pos())
		abs := tf.Name()
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						if obj := info.Defs[sp.Name]; obj != nil {
							declsByFile[abs] = append(declsByFile[abs], gcDecl{start: tf.Offset(sp.Pos()), end: tf.Offset(sp.End()), objs: []types.Object{obj}})
						}
					case *ast.ValueSpec:
						var objs []types.Object
						for _, n := range sp.Names {
							if obj := info.Defs[n]; obj != nil {
								objs = append(objs, obj)
							}
						}
						declsByFile[abs] = append(declsByFile[abs], gcDecl{start: tf.Offset(sp.Pos()), end: tf.Offset(sp.End()), objs: objs})
					}
				}
			case *ast.FuncDecl:
				if obj := info.Defs[d.Name]; obj != nil {
					declsByFile[abs] = append(declsByFile[abs], gcDecl{start: tf.Offset(d.Pos()), end: tf.Offset(d.End()), objs: []types.Object{obj}})
				}
			}
		}
	}
	sourceObjsAt := func(pos token.Pos) []types.Object {
		f := fset.File(pos)
		if f == nil {
			return nil
		}
		off := f.Offset(pos)
		for _, d := range declsByFile[f.Name()] {
			if off >= d.start && off < d.end {
				return d.objs
			}
		}
		return nil
	}
	anyIn := func(objs []types.Object, set map[types.Object]bool) bool {
		for _, o := range objs {
			if set[o] {
				return true
			}
		}
		return false
	}

	edges := map[gcEdge]bool{}
	add := func(from token.Pos, obj types.Object, srcObj types.Object) {
		if obj == nil || obj.Pkg() != pkg || !obj.Pos().IsValid() {
			return
		}
		if obj.Parent() != nil && obj.Parent() != pkg.Scope() {
			return
		}
		srcFile := sourceNameAt(from)
		tgtFile := declFileByObj(obj)
		if srcFile == "" || tgtFile == "" || srcFile == tgtFile {
			return
		}
		edges[gcEdge{srcFile: srcFile, srcObj: srcObj, target: obj}] = true
	}
	for id, obj := range info.Uses {
		if v, ok := obj.(*types.Var); ok && v.IsField() {
			continue
		}
		srcs := sourceObjsAt(id.Pos())
		var src types.Object
		if len(srcs) > 0 {
			src = srcs[0]
		}
		add(id.Pos(), obj, src)
	}
	for se, s := range info.Selections {
		if s.Kind() == types.FieldVal {
			continue
		}
		srcs := sourceObjsAt(se.Sel.Pos())
		var src types.Object
		if len(srcs) > 0 {
			src = srcs[0]
		}
		add(se.Sel.Pos(), s.Obj(), src)
	}

	module := modulePath(root)
	prefixCounts := map[string]int{}
	for _, b := range fileBases {
		prefixCounts[prefixToken(b)]++
	}
	clusterOf := func(base string) string {
		t := prefixToken(base)
		if prefixCounts[t] >= 3 {
			return t
		}
		return "(rest)"
	}
	self := map[string]bool{}
	out := map[string]bool{}
	in := map[string]bool{}
	inKeys := map[string]bool{}
	inFile := map[string]string{}
	for e := range edges {
		srcIn := false
		tgtIn := false
		tgtFile := declFileByObj(e.target)
		switch sel.kind {
		case "prefix", "file":
			srcIn = fileSet[e.srcFile]
			tgtIn = fileSet[tgtFile]
		case "name", "list":
			srcIn = anyIn([]types.Object{e.srcObj}, objSet)
			tgtIn = objSet[e.target]
		}
		key := symbolKey(e.target, module)
		if key == "" {
			key = declName[e.target]
		}
		if srcIn && tgtIn {
			self[key] = true
		} else if srcIn {
			out[key] = true
		} else if tgtIn {
			in[key] = true
			inFile[key] = tgtFile
			srcGroup := e.srcFile
			if sel.kind == "prefix" {
				srcGroup = clusterOf(e.srcFile)
			}
			inKeys[srcGroup+"\x00"+key] = true
		}
	}
	fileCount := 0
	switch sel.kind {
	case "prefix", "file":
		fileCount = len(fileSet)
	case "name", "list":
		fs := map[string]bool{}
		for o := range objSet {
			if f := declFile[o]; f != "" {
				fs[f] = true
			}
		}
		fileCount = len(fs)
	}
	fmt.Printf("files=%d self=%d out=%d in=%d\n", fileCount, len(self), len(out), len(inKeys))
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  <- %s (%s)\n", k, inFile[k])
	}
	return nil
}
