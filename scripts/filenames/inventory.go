package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Essential: start/end are byte offsets so apply can cut a declaration and
// its doc comment verbatim.
type part struct {
	file      string
	abs       string
	start     int
	end       int
	startLine int
	endLine   int
	node      ast.Node
}

type unit struct {
	ID        string
	Pkg       string
	PkgPath   string
	Kind      string
	Name      string
	Receiver  string
	File      string
	Parts     []part
	Emitted   bool
	RawTokens string
	NormToken string
}

func (u *unit) symbol() string {
	return u.Pkg + "::" + u.Name
}

type pkgData struct {
	list     listedPackage
	files    []*ast.File
	fset     *token.FileSet
	pkg      *types.Package
	checkErr error
	info     *types.Info
	units    []*unit
	defUnit  map[types.Object]*unit
	byFile   map[string][]interval
}

type interval struct {
	start int
	end   int
	unit  *unit
}

func (u *unit) addPart(fset *token.FileSet, file, abs string, node ast.Node, start, end token.Pos, root string) {
	tf := fset.File(start)
	if tf == nil {
		return
	}
	sp := fset.PositionFor(start, false)
	ep := fset.PositionFor(end, false)
	u.Parts = append(u.Parts, part{
		file:      file,
		abs:       abs,
		start:     tf.Offset(start),
		end:       tf.Offset(end),
		startLine: sp.Line,
		endLine:   ep.Line,
		node:      node,
	})
}

func docStart(d *ast.GenDecl, fallback token.Pos) token.Pos {
	if d.Doc != nil {
		return d.Doc.Pos()
	}
	return fallback
}

func buildUnits(pd *pkgData, module, short, root string) {
	byType := map[string]*unit{}
	type methodInfo struct {
		decl *ast.FuncDecl
		file string
		abs  string
	}
	var methods []methodInfo
	for _, f := range pd.files {
		abs := pd.fset.File(f.Pos()).Name()
		file := relPath(root, abs)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				switch d.Tok {
				case token.TYPE:
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						start := ts.Pos()
						if len(d.Specs) == 1 {
							start = docStart(d, d.Pos())
						}
						u := &unit{
							ID:       fmt.Sprintf("%s::type::%s::%s", short, ts.Name.Name, file),
							Pkg:      short,
							PkgPath:  pd.list.ImportPath,
							Kind:     "type",
							Name:     ts.Name.Name,
							Receiver: ts.Name.Name,
							File:     file,
						}
						u.addPart(pd.fset, file, abs, ts, start, ts.End(), root)
						pd.units = append(pd.units, u)
						byType[ts.Name.Name] = u
					}
				case token.VAR, token.CONST:
					names := valueSpecNames(d)
					if len(names) == 0 {
						continue
					}
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					u := &unit{
						ID:      fmt.Sprintf("%s::%s::%s::%s", short, kind, strings.Join(names, ","), file),
						Pkg:     short,
						PkgPath: pd.list.ImportPath,
						Kind:    kind,
						Name:    strings.Join(names, ","),
						File:    file,
					}
					u.addPart(pd.fset, file, abs, d, docStart(d, d.Pos()), d.End(), root)
					pd.units = append(pd.units, u)
				}
			case *ast.FuncDecl:
				abs := pd.fset.File(d.Pos()).Name()
				file := relPath(root, abs)
				if d.Recv != nil {
					methods = append(methods, methodInfo{decl: d, file: file, abs: abs})
					continue
				}
				start := d.Pos()
				if d.Doc != nil {
					start = d.Doc.Pos()
				}
				u := &unit{
					ID:      fmt.Sprintf("%s::func::%s::%s", short, d.Name.Name, file),
					Pkg:     short,
					PkgPath: pd.list.ImportPath,
					Kind:    "func",
					Name:    d.Name.Name,
					File:    file,
				}
				u.addPart(pd.fset, file, abs, d, start, d.End(), root)
				pd.units = append(pd.units, u)
			}
		}
	}
	for _, m := range methods {
		recv := receiverBase(m.decl.Recv.List[0].Type)
		u := byType[recv]
		if u == nil {
			name := m.decl.Name.Name
			if recv != "" {
				name = recv + "." + name
			}
			u = &unit{
				ID:       fmt.Sprintf("%s::method::%s::%s", short, name, m.file),
				Pkg:      short,
				PkgPath:  pd.list.ImportPath,
				Kind:     "method",
				Name:     name,
				Receiver: recv,
				File:     m.file,
			}
			pd.units = append(pd.units, u)
		}
		start := m.decl.Pos()
		if m.decl.Doc != nil {
			start = m.decl.Doc.Pos()
		}
		u.addPart(pd.fset, m.file, m.abs, m.decl, start, m.decl.End(), root)
	}
	sort.Slice(pd.units, func(i, j int) bool {
		if pd.units[i].File != pd.units[j].File {
			return pd.units[i].File < pd.units[j].File
		}
		if pd.units[i].Parts[0].start != pd.units[j].Parts[0].start {
			return pd.units[i].Parts[0].start < pd.units[j].Parts[0].start
		}
		return pd.units[i].ID < pd.units[j].ID
	})
}

func valueSpecNames(d *ast.GenDecl) []string {
	var names []string
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, n := range vs.Names {
			names = append(names, n.Name)
		}
	}
	return names
}

func mapDefs(pd *pkgData) {
	pd.defUnit = map[types.Object]*unit{}
	for _, u := range pd.units {
		for _, p := range u.Parts {
			switch n := p.node.(type) {
			case *ast.TypeSpec:
				if obj := pd.info.Defs[n.Name]; obj != nil {
					pd.defUnit[obj] = u
				}
			case *ast.FuncDecl:
				if obj := pd.info.Defs[n.Name]; obj != nil {
					pd.defUnit[obj] = u
				}
			case *ast.GenDecl:
				for _, spec := range n.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range vs.Names {
						if obj := pd.info.Defs[name]; obj != nil {
							pd.defUnit[obj] = u
						}
					}
				}
			}
		}
	}
}

func mapIntervals(pd *pkgData) {
	pd.byFile = map[string][]interval{}
	for _, u := range pd.units {
		for _, p := range u.Parts {
			pd.byFile[p.abs] = append(pd.byFile[p.abs], interval{start: p.start, end: p.end, unit: u})
		}
	}
	for _, ints := range pd.byFile {
		sort.Slice(ints, func(i, j int) bool { return ints[i].start < ints[j].start })
	}
}

func unitAt(pd *pkgData, pos token.Pos) *unit {
	f := pd.fset.File(pos)
	if f == nil {
		return nil
	}
	off := f.Offset(pos)
	for _, in := range pd.byFile[f.Name()] {
		if off >= in.start && off < in.end {
			return in.unit
		}
	}
	return nil
}

func loadPackage(pd *pkgData, imp types.Importer, root string) error {
	pd.fset = token.NewFileSet()
	var files []*ast.File
	for _, name := range pd.list.GoFiles {
		abs := filepath.Join(pd.list.Dir, name)
		f, err := parser.ParseFile(pd.fset, abs, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", abs, err)
		}
		files = append(files, f)
	}
	pd.files = files
	pd.info = &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: imp, Error: func(error) {}, DisableUnusedImportCheck: true}
	pkg, checkErr := conf.Check(pd.list.ImportPath, pd.fset, files, pd.info)
	pd.pkg = pkg
	pd.checkErr = checkErr
	short := shortPath(pd.list.ImportPath, modulePath(root))
	buildUnits(pd, modulePath(root), short, root)
	mapDefs(pd)
	mapIntervals(pd)
	return nil
}

func unitEmitted(u *unit) bool {
	for _, p := range u.Parts {
		base := filepath.Base(p.file)
		stem := strings.TrimSuffix(base, ".go")
		if strings.Contains(stem, "_") && !strings.HasSuffix(base, "_test.go") {
			return true
		}
	}
	return false
}

func primaryFile(u *unit) string {
	for _, p := range u.Parts {
		stem := strings.TrimSuffix(filepath.Base(p.file), ".go")
		if strings.Contains(stem, "_") {
			return p.file
		}
	}
	return u.Parts[0].file
}

func tokenizeName(name string) []string {
	stem := strings.TrimSuffix(filepath.Base(name), ".go")
	stem = strings.TrimSuffix(stem, ".gen")
	parts := strings.FieldsFunc(stem, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for i := range parts {
		parts[i] = strings.ToLower(parts[i])
	}
	return parts
}

var foldVariants = map[string]string{
	"validation": "validate",
	"validates":  "validate",
	"validated":  "validate",
	"aliases":    "alias",
	"groups":     "group",
	"rows":       "row",
	"files":      "file",
	"sources":    "source",
	"types":      "type",
	"values":     "value",
}

func normalizeTokens(file, pkgShort string) (string, string) {
	raw := tokenizeName(file)
	pkgParts := strings.Split(filepath.Base(pkgShort), "/")
	var pkgTokens []string
	for _, p := range pkgParts {
		pkgTokens = append(pkgTokens, strings.Split(p, "_")...)
	}
	drop := map[string]bool{}
	for _, p := range pkgTokens {
		if p != "" {
			drop[p] = true
		}
	}
	pkgBase := strings.ReplaceAll(filepath.Base(pkgShort), "_", "")
	var norm []string
	for i := 0; i < len(raw); i++ {
		tok := raw[i]
		if foldVariants[tok] != "" {
			tok = foldVariants[tok]
		} else if strings.HasSuffix(tok, "s") && len(tok) > 3 {
			singular := strings.TrimSuffix(tok, "s")
			if foldVariants[singular] != "" {
				tok = foldVariants[singular]
			}
		}
		if drop[tok] || tok == pkgBase {
			continue
		}
		if i+1 < len(raw) && raw[i]+raw[i+1] == pkgBase {
			i++
			continue
		}
		if len(norm) == 0 || norm[len(norm)-1] != tok {
			norm = append(norm, tok)
		}
	}
	return strings.Join(raw, ";"), strings.Join(norm, ";")
}

func isExportedUnit(u *unit) bool {
	switch u.Kind {
	case "type":
		return ast.IsExported(u.Name)
	case "func":
		return ast.IsExported(u.Name)
	case "var", "const":
		parts := strings.Split(u.Name, ",")
		for _, p := range parts {
			if ast.IsExported(p) {
				return true
			}
		}
	}
	return false
}

func lineSummary(u *unit) string {
	if len(u.Parts) == 0 {
		return ""
	}
	minLine, maxLine := u.Parts[0].startLine, u.Parts[0].endLine
	for _, p := range u.Parts[1:] {
		if p.startLine < minLine {
			minLine = p.startLine
		}
		if p.endLine > maxLine {
			maxLine = p.endLine
		}
	}
	return fmt.Sprintf("%d-%d", minLine, maxLine)
}

func rangeSummary(u *unit) string {
	var out []string
	for _, p := range u.Parts {
		out = append(out, fmt.Sprintf("%s:%d-%d", p.file, p.start, p.end))
	}
	return strings.Join(out, ";")
}

type edgeTarget struct {
	key string
	pkg string
}

type inventoryResult struct {
	rows                    [][]string
	fileRows                [][]string
	total                   int
	multiDestinationFiles   int
	fileDestinations        map[string]map[string]int
	filePackages            map[string]string
	referencesFromNonLedger int
}

func runInventory(root string) error {
	pkgs, err := goList(root, "-export", "-deps", "-json", "-e", "./internal/...")
	if err != nil {
		return err
	}
	module := modulePath(root)
	exports := exportMap(pkgs)
	fset := token.NewFileSet()
	imp := newExportImporter(fset, exports)

	var data []*pkgData
	for _, p := range pkgs {
		if !strings.HasPrefix(p.ImportPath, module+"/internal/") {
			continue
		}
		if len(p.GoFiles) == 0 {
			continue
		}
		pd := &pkgData{list: p}
		if err := loadPackage(pd, imp, root); err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", p.ImportPath, err)
			continue
		}
		data = append(data, pd)
	}
	sort.Slice(data, func(i, j int) bool { return data[i].list.ImportPath < data[j].list.ImportPath })

	allUnits := map[string]*unit{}
	symbolUnit := map[string]*unit{}
	for _, pd := range data {
		for _, u := range pd.units {
			allUnits[u.ID] = u
		}
		for obj, u := range pd.defUnit {
			if key := symbolKey(obj, module); key != "" {
				symbolUnit[key] = u
			}
		}
	}

	outgoing := map[string]map[string]edgeTarget{}
	incoming := map[string]map[string]bool{}
	selfRefs := map[string]map[string]bool{}
	addEdge := func(src *unit, targetKey, targetPkg string, targetUnit *unit) {
		if src == nil {
			return
		}
		if targetKey == "" {
			return
		}
		if targetUnit == src {
			if selfRefs[src.ID] == nil {
				selfRefs[src.ID] = map[string]bool{}
			}
			selfRefs[src.ID][targetKey] = true
			return
		}
		if outgoing[src.ID] == nil {
			outgoing[src.ID] = map[string]edgeTarget{}
		}
		outgoing[src.ID][targetKey] = edgeTarget{key: targetKey, pkg: targetPkg}
		if targetUnit != nil {
			if incoming[targetUnit.ID] == nil {
				incoming[targetUnit.ID] = map[string]bool{}
			}
			incoming[targetUnit.ID][src.symbol()] = true
		}
	}
	for _, pd := range data {
		for id, obj := range pd.info.Uses {
			if obj == nil || obj.Pkg() == nil {
				continue
			}
			if v, ok := obj.(*types.Var); ok && v.IsField() {
				continue
			}
			if obj.Parent() != nil && obj.Parent() != pd.pkg.Scope() {
				continue
			}
			if obj.Pkg().Path() != pd.list.ImportPath && !strings.HasPrefix(obj.Pkg().Path(), module+"/") {
				continue
			}
			src := unitAt(pd, id.Pos())
			key := symbolKey(obj, module)
			addEdge(src, key, shortPath(obj.Pkg().Path(), module), symbolUnit[key])
		}
		for se, sel := range pd.info.Selections {
			if sel.Kind() == types.FieldVal {
				continue
			}
			obj := sel.Obj()
			if obj == nil || obj.Pkg() == nil {
				continue
			}
			if obj.Pkg().Path() != pd.list.ImportPath && !strings.HasPrefix(obj.Pkg().Path(), module+"/") {
				continue
			}
			src := unitAt(pd, se.Sel.Pos())
			key := symbolKey(obj, module)
			addEdge(src, key, shortPath(obj.Pkg().Path(), module), symbolUnit[key])
		}
	}

	res := inventoryResult{
		fileDestinations: map[string]map[string]int{},
		filePackages:     map[string]string{},
	}
	var rows [][]string
	for _, pd := range data {
		for _, u := range pd.units {
			u.Emitted = unitEmitted(u)
			if !u.Emitted {
				continue
			}
			u.File = primaryFile(u)
			u.RawTokens, u.NormToken = normalizeTokens(u.File, u.Pkg)
			aff := map[string]int{}
			for _, t := range outgoing[u.ID] {
				aff[t.pkg]++
			}
			if _, ok := aff[u.Pkg]; !ok {
				aff[u.Pkg] = 0
			}
			total := 0
			for _, c := range aff {
				total += c
			}
			var affParts []string
			for _, p := range sortedIntKeys(aff) {
				affParts = append(affParts, fmt.Sprintf("%s=%d/%d", escapeTSVValue(p), aff[p], total))
			}
			refs := map[string]bool{}
			for k := range outgoing[u.ID] {
				refs[k] = true
			}
			var inRefs, outRefs, selfCount int
			outRefs = len(outgoing[u.ID])
			selfCount = len(selfRefs[u.ID])
			referencing := incoming[u.ID]
			inRefs = len(referencing)
			row := []string{
				u.ID,
				u.File,
				u.Pkg,
				u.Kind,
				u.Name,
				u.Receiver,
				lineSummary(u),
				fmt.Sprintf("%t", isExportedUnit(u)),
				escapeField(u.RawTokens),
				escapeField(u.NormToken),
				escapeField(escapeSymbols(refs)),
				escapeField(escapeSymbols(referencing)),
				escapeField(strings.Join(affParts, ";")),
				fmt.Sprintf("%d", inRefs),
				fmt.Sprintf("%d", outRefs),
				fmt.Sprintf("%d", selfCount),
				escapeField(rangeSummary(u)),
				"",
				"",
				"",
				"",
				"",
				"",
			}
			rows = append(rows, row)
			res.total++
			top := topPackage(aff, u.Pkg)
			seenFile := map[string]bool{}
			for _, p := range u.Parts {
				base := filepath.Base(p.file)
				stem := strings.TrimSuffix(base, ".go")
				if !strings.Contains(stem, "_") || strings.HasSuffix(base, "_test.go") || seenFile[p.file] {
					continue
				}
				seenFile[p.file] = true
				if res.fileDestinations[p.file] == nil {
					res.fileDestinations[p.file] = map[string]int{}
				}
				res.fileDestinations[p.file][top]++
				res.filePackages[p.file] = u.Pkg
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i][1] != rows[j][1] {
			return rows[i][1] < rows[j][1]
		}
		return rows[i][0] < rows[j][0]
	})
	res.rows = rows

	var fileRows [][]string
	var fileKeys []string
	for f := range res.fileDestinations {
		fileKeys = append(fileKeys, f)
	}
	sort.Strings(fileKeys)
	for _, f := range fileKeys {
		dests := res.fileDestinations[f]
		var parts []string
		var names []string
		for p := range dests {
			names = append(names, p)
		}
		sort.Strings(names)
		for _, p := range names {
			parts = append(parts, fmt.Sprintf("%s=%d", p, dests[p]))
		}
		outcome := "RENAME"
		if len(names) == 1 {
			if names[0] != res.filePackages[f] {
				outcome = "MOVE"
			}
		} else {
			outcome = "SPLIT"
		}
		if len(names) > 1 {
			res.multiDestinationFiles++
		}
		fileRows = append(fileRows, []string{
			f, res.filePackages[f], fmt.Sprintf("%d", totalCount(dests)),
			fmt.Sprintf("%d", len(names)), strings.Join(parts, ";"), outcome,
		})
	}
	res.fileRows = fileRows
	ledgerPath := filepath.Join(root, ".cache", "filenames", "ledger.tsv")
	header := strings.Split(ledgerHeader, "\t")
	if err := writeTSV(ledgerPath, header, rows); err != nil {
		return err
	}
	fileHeader := []string{"file", "package", "decl_count", "distinct_top_destinations", "top_destinations", "outcome_hint"}
	if err := writeTSV(filepath.Join(root, ".cache", "filenames", "files.tsv"), fileHeader, fileRows); err != nil {
		return err
	}
	fmt.Printf("inventory: %d declarations in %d underscore files; %d files have more than one top-affinity destination\n", res.total, len(fileRows), res.multiDestinationFiles)
	fmt.Printf("ledger: %s\n", relPath(root, ledgerPath))
	return nil
}

func escapeTSVValue(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ").Replace(s)
}

func sortedIntKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func topPackage(aff map[string]int, own string) string {
	best := ""
	bestCount := -1
	for _, p := range sortedIntKeys(aff) {
		c := aff[p]
		if c > bestCount {
			best, bestCount = p, c
		} else if c == bestCount && p == own {
			best = p
		}
	}
	return best
}

func totalCount(m map[string]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}
