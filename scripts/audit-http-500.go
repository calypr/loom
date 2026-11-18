//go:build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type row struct {
	File         string `json:"file"`
	Line         int    `json:"line"`
	Function     string `json:"function"`
	Mapper       string `json:"mapper"`
	BodyVariable string `json:"bodyVariable"`
}
type audit struct {
	fs          *token.FileSet
	funcs       map[string]*ast.FuncDecl
	factories   map[string]*ast.FuncDecl
	rows        []row
	problems    []string
	active      map[string]bool
	operation   string
	closures    map[string]*ast.FuncLit
	invalidated map[string]bool
}

var mappers = map[string]int{"authoringErrorForOpenAPI": 1, "explorerErrorResponse": 1, "explorerLegacyResponse": 1, "mapServiceError": 1}

func name(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if p, ok := x.X.(*ast.Ident); ok {
			return p.Name + "." + x.Sel.Name
		}
	}
	return ""
}
func copyEnv(e map[string]string) map[string]string {
	o := map[string]string{}
	for k, v := range e {
		o[k] = v
	}
	return o
}

func (a *audit) liveProvenance(origin string) string {
	if a.invalidated[origin] {
		return ""
	}
	return origin
}
func (a *audit) invalidateOrigin(origin string) {
	if origin != "" {
		a.invalidated[origin] = true
	}
}
func (a *audit) provenance(e ast.Expr, env map[string]string) string {
	switch x := e.(type) {
	case *ast.Ident:
		return a.liveProvenance(env[x.Name])
	case *ast.ParenExpr:
		return a.provenance(x.X, env)
	case *ast.UnaryExpr:
		return a.provenance(x.X, env)
	case *ast.SelectorExpr:
		return a.liveProvenance(env[rootBinding(x)])
	case *ast.IndexExpr:
		return a.liveProvenance(env[rootBinding(x)])
	case *ast.CallExpr:
		n := name(x.Fun)
		if n == "openAPIErrorResponseBody" && len(x.Args) == 2 {
			return a.provenance(x.Args[1], env)
		}
		if (n == "rawJSONBytes" || n == "authoringUnauthorizedResponse" || strings.HasPrefix(n, "loomapi.")) && len(x.Args) == 1 {
			return a.provenance(x.Args[0], env)
		}

	}
	return ""
}

func knownInteger(e ast.Expr, statusName string) (int, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.INT {
			v, err := strconv.Atoi(x.Value)
			return v, err == nil
		}
	case *ast.Ident:
		if x.Name == statusName {
			return 500, true
		}
	case *ast.SelectorExpr:
		if name(x) == "http.StatusInternalServerError" {
			return 500, true
		}
	}
	return 0, false
}
func knownCondition(e ast.Expr, statusName string) (bool, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		if x.Name == "true" {
			return true, true
		}
		if x.Name == "false" {
			return false, true
		}
	case *ast.UnaryExpr:
		if x.Op == token.NOT {
			v, ok := knownCondition(x.X, statusName)
			return !v, ok
		}
	case *ast.BinaryExpr:
		l, lok := knownInteger(x.X, statusName)
		r, rok := knownInteger(x.Y, statusName)
		if lok && rok {
			switch x.Op {
			case token.EQL:
				return l == r, true
			case token.NEQ:
				return l != r, true
			}
		}
	}
	return false, false
}

func returnedExpressions(block *ast.BlockStmt, statusName string, visit func(ast.Expr)) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.List {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			for _, e := range s.Results {
				visit(e)
			}
			return true
		case *ast.IfStmt:
			cond, known := knownCondition(s.Cond, statusName)
			left, right := false, false
			if !known || cond {
				left = returnedExpressions(s.Body, statusName, visit)
			}
			if !known || !cond {
				switch e := s.Else.(type) {
				case *ast.BlockStmt:
					right = returnedExpressions(e, statusName, visit)
				case *ast.IfStmt:
					right = returnedExpressions(&ast.BlockStmt{List: []ast.Stmt{e}}, statusName, visit)
				}
			}
			if known && ((cond && left) || (!cond && right)) || !known && left && right {
				return true
			}
		case *ast.SwitchStmt:
			if tag, known := knownInteger(s.Tag, statusName); known {
				var selected, otherwise *ast.CaseClause
				for _, branch := range s.Body.List {
					c := branch.(*ast.CaseClause)
					if c.List == nil {
						otherwise = c
					}
					for _, expr := range c.List {
						if v, ok := knownInteger(expr, statusName); ok && v == tag {
							selected = c
						}
					}
				}
				if selected == nil {
					selected = otherwise
				}
				if selected != nil && returnedExpressions(&ast.BlockStmt{List: selected.Body}, statusName, visit) {
					return true
				}
			} else {
				all, hasDefault := true, false
				for _, branch := range s.Body.List {
					c := branch.(*ast.CaseClause)
					hasDefault = hasDefault || c.List == nil
					all = returnedExpressions(&ast.BlockStmt{List: c.Body}, statusName, visit) && all
				}
				if all && hasDefault {
					return true
				}
			}
		case *ast.BlockStmt:
			if returnedExpressions(s, statusName, visit) {
				return true
			}
		}
	}
	return false
}
func (a *audit) factory500(n string) bool {
	f := a.factories[n]
	if f == nil || len(f.Type.Params.List) == 0 || len(f.Type.Params.List[0].Names) != 1 {
		return false
	}
	statusName := f.Type.Params.List[0].Names[0].Name
	found := false
	returnedExpressions(f.Body, statusName, func(e ast.Expr) {
		var typ ast.Expr
		switch x := e.(type) {
		case *ast.CompositeLit:
			typ = x.Type
		case *ast.CallExpr:
			typ = x.Fun
		}
		if name(typ) == a.operation+"500JSONResponse" {
			found = true
		}
	})
	return found
}

func (a *audit) returned(e ast.Expr, env map[string]string) {
	switch x := e.(type) {
	case *ast.CallExpr:
		n := name(x.Fun)
		if f := a.closures[n]; f != nil {
			if a.active[n] {
				return
			}
			a.active[n] = true
			defer delete(a.active, n)
			a.block(f.Body, copyEnv(env))
			return
		}
		if strings.HasPrefix(n, "loomapi.New") && strings.HasSuffix(n, "ErrorResponse") {
			factory := strings.TrimPrefix(n, "loomapi.")
			if factory != "New"+a.operation+"ErrorResponse" {
				a.problems = append(a.problems, fmt.Sprintf("%s returns factory for another operation: %s", a.operation, n))
				return
			}
			if len(x.Args) > 0 {
				if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Value != "500" {
					return
				}
				if status := name(x.Args[0]); strings.HasPrefix(status, "http.Status") && status != "http.StatusInternalServerError" {
					return
				}
			}
			if !a.factory500(factory) {
				return
			}
			if len(x.Args) != 2 {
				a.problems = append(a.problems, a.operation+" factory has unexpected arguments")
				return
			}
			a.record(x.Pos(), x.Args[1], env)
			return
		}
		if n == "loomapi."+a.operation+"500JSONResponse" && len(x.Args) == 1 {
			a.record(x.Pos(), x.Args[0], env)
			return
		}
		if f := a.funcs[n]; f != nil && f.Recv == nil {
			if a.active[n] {
				return
			}
			a.active[n] = true
			defer delete(a.active, n)
			bound := map[string]string{}
			i := 0
			for _, p := range f.Type.Params.List {
				for _, id := range p.Names {
					if i < len(x.Args) {
						bound[id.Name] = a.provenance(x.Args[i], env)
					}
					i++
				}
			}
			a.block(f.Body, bound)
		}
	case *ast.CompositeLit:
		if name(x.Type) == "loomapi."+a.operation+"500JSONResponse" {
			a.record(x.Pos(), x, env)
		}
	}
}
func (a *audit) record(pos token.Pos, body ast.Expr, env map[string]string) {
	p := a.fs.Position(pos)
	mapper := strings.SplitN(a.provenance(body, env), "@", 2)[0]
	a.rows = append(a.rows, row{filepath.Base(p.Filename), p.Line, a.operation, mapper, name(body)})
	if mapper == "" {
		a.problems = append(a.problems, fmt.Sprintf("%s:%d %s: 500 return has no recognized mapped body", filepath.Base(p.Filename), p.Line, a.operation))
	}
}

func (a *audit) invalidateCallEffects(e ast.Expr, env map[string]string) {
	ast.Inspect(e, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		n := name(call.Fun)
		if _, ok := mappers[n]; ok {
			return false
		}
		if n == "rawJSONBytes" || n == "authoringUnauthorizedResponse" || n == "openAPIErrorResponseBody" || strings.HasPrefix(n, "loomapi.") {
			return true
		}
		for key, origin := range env {
			a.invalidateOrigin(origin)
			env[key] = ""
		}
		return false
	})
}
func rootBinding(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return rootBinding(x.X)
	case *ast.IndexExpr:
		return rootBinding(x.X)
	case *ast.StarExpr:
		return rootBinding(x.X)
	case *ast.ParenExpr:
		return rootBinding(x.X)
	}
	return ""
}
func (a *audit) invalidateAliases(env map[string]string, e ast.Expr) {
	source := env[rootBinding(e)]
	a.invalidateOrigin(source)
	if source == "" {
		return
	}
	for key, value := range env {
		if value == source {
			env[key] = ""
		}
	}
}

func (a *audit) scopedBlock(b *ast.BlockStmt, env map[string]string) bool {
	if b == nil {
		return false
	}
	saved := map[string]string{}
	existed := map[string]bool{}
	save := func(n string) {
		if _, ok := existed[n]; ok {
			return
		}
		v, ok := env[n]
		saved[n] = v
		existed[n] = ok
	}
	done := false
	for _, stmt := range b.List {
		switch x := stmt.(type) {
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, lhs := range x.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						save(id.Name)
					}
				}
			}
		case *ast.DeclStmt:
			if d, ok := x.Decl.(*ast.GenDecl); ok {
				for _, spec := range d.Specs {
					if v, ok := spec.(*ast.ValueSpec); ok {
						for _, id := range v.Names {
							save(id.Name)
						}
					}
				}
			}
		}
		if a.block(&ast.BlockStmt{List: []ast.Stmt{stmt}}, env) {
			done = true
			break
		}
	}
	for n, present := range existed {
		if present {
			env[n] = a.liveProvenance(saved[n])
		} else {
			delete(env, n)
		}
	}
	return done
}
func copyInvalidated(values map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range values {
		out[k] = v
	}
	return out
}
func mergeInvalidated(dst, src map[string]bool) {
	for k, v := range src {
		if v {
			dst[k] = true
		}
	}
}
func mergeChanged(env, before, after map[string]string) {
	for k, v := range before {
		if after[k] != v {
			env[k] = ""
		}
	}
}
func (a *audit) block(b *ast.BlockStmt, env map[string]string) bool {
	if b == nil {
		return false
	}
	for _, statement := range b.List {
		switch s := statement.(type) {
		case *ast.AssignStmt:
			if len(s.Rhs) == 1 && len(s.Lhs) == 1 {
				if f, ok := s.Rhs[0].(*ast.FuncLit); ok {
					if id, ok := s.Lhs[0].(*ast.Ident); ok {
						a.closures[id.Name] = f
						continue
					}
				}
			}
			for _, rhs := range s.Rhs {
				a.invalidateCallEffects(rhs, env)
			}
			values := make([]string, len(s.Lhs))
			if len(s.Rhs) == 1 {
				if call, ok := s.Rhs[0].(*ast.CallExpr); ok {
					n := name(call.Fun)
					if index, ok := mappers[n]; ok && index < len(values) {
						values[index] = n + "@" + strconv.Itoa(int(call.Pos()))
					} else if len(values) > 0 {
						values[0] = a.provenance(call, env)
					}
				} else if len(values) > 0 {
					values[0] = a.provenance(s.Rhs[0], env)
				}
			} else {
				for i, e := range s.Rhs {
					if i < len(values) {
						values[i] = a.provenance(e, env)
					}
				}
			}
			for i, e := range s.Lhs {
				if id, ok := e.(*ast.Ident); ok {
					delete(a.closures, id.Name)
					env[id.Name] = values[i]
				} else {
					a.invalidateAliases(env, e)
				}
			}
		case *ast.ExprStmt:
			a.invalidateCallEffects(s.X, env)
		case *ast.IncDecStmt:
			a.invalidateAliases(env, s.X)
		case *ast.DeclStmt:
			if d, ok := s.Decl.(*ast.GenDecl); ok {
				for _, spec := range d.Specs {
					if v, ok := spec.(*ast.ValueSpec); ok {
						for i, id := range v.Names {
							env[id.Name] = ""
							if i < len(v.Values) {
								env[id.Name] = a.provenance(v.Values[i], env)
							}
						}
					}
				}
			}
		case *ast.ReturnStmt:
			for _, e := range s.Results {
				a.returned(e, env)
			}
			return true
		case *ast.IfStmt:
			before := copyEnv(env)
			initialized := copyEnv(env)
			a.block(&ast.BlockStmt{List: []ast.Stmt{s.Init}}, initialized)
			baseInvalid := copyInvalidated(a.invalidated)
			branch := copyEnv(initialized)
			thenDone := a.scopedBlock(s.Body, branch)
			thenInvalid := copyInvalidated(a.invalidated)
			a.invalidated = copyInvalidated(baseInvalid)
			other := copyEnv(initialized)
			elseDone := false
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				elseDone = a.scopedBlock(e, other)
			case *ast.IfStmt:
				elseDone = a.block(&ast.BlockStmt{List: []ast.Stmt{e}}, other)
			}
			elseInvalid := copyInvalidated(a.invalidated)
			a.invalidated = baseInvalid
			if thenDone && elseDone {
				return true
			}
			if !thenDone {
				mergeInvalidated(a.invalidated, thenInvalid)
				mergeChanged(env, before, branch)
			}
			if !elseDone {
				mergeInvalidated(a.invalidated, elseInvalid)
				mergeChanged(env, before, other)
			}
		case *ast.SwitchStmt:
			before := copyEnv(env)
			baseInvalid := copyInvalidated(a.invalidated)
			continuingInvalid := copyInvalidated(baseInvalid)
			allDone, hasDefault := true, false
			for _, c := range s.Body.List {
				if c, ok := c.(*ast.CaseClause); ok {
					a.invalidated = copyInvalidated(baseInvalid)
					branch := copyEnv(before)
					done := a.scopedBlock(&ast.BlockStmt{List: c.Body}, branch)
					hasDefault = hasDefault || c.List == nil
					allDone = allDone && done
					if !done {
						mergeInvalidated(continuingInvalid, a.invalidated)
						mergeChanged(env, before, branch)
					}
				}
			}
			a.invalidated = continuingInvalid
			if allDone && hasDefault {
				return true
			}

		case *ast.BlockStmt:
			if a.scopedBlock(s, env) {
				return true
			}
		case *ast.ForStmt, *ast.RangeStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:

			for k, origin := range env {
				a.invalidateOrigin(origin)
				env[k] = ""
			}
		}
	}
	return false
}

func main() {
	root := flag.String("root", "internal/server", "")
	ops := flag.String("operations", "", "")
	flag.Parse()
	a := audit{fs: token.NewFileSet(), funcs: map[string]*ast.FuncDecl{}, factories: map[string]*ast.FuncDecl{}, active: map[string]bool{}}
	files, _ := filepath.Glob(filepath.Join(*root, "openapi_*.go"))
	generated, _ := filepath.Glob(filepath.Join(*root, "../../api/loomapi/*.go"))
	parse := func(paths []string, dst map[string]*ast.FuncDecl) {
		for _, p := range paths {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			f, e := parser.ParseFile(a.fs, p, nil, 0)
			if e != nil {
				a.problems = append(a.problems, e.Error())
				continue
			}
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
					dst[fn.Name.Name] = fn
				}
			}
		}
	}
	parse(files, a.funcs)
	parse(generated, a.factories)
	for _, op := range strings.Split(*ops, ",") {
		if op == "" {
			continue
		}
		a.closures = map[string]*ast.FuncLit{}
		a.invalidated = map[string]bool{}
		a.operation = strings.ToUpper(op[:1]) + op[1:]
		start := len(a.rows)
		fn := a.funcs[a.operation]
		if fn != nil {
			a.block(fn.Body, map[string]string{})
		}
		if len(a.rows) == start {
			a.problems = append(a.problems, op+" missing reachable mapped 500 response")
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"cases": a.rows, "problems": a.problems})
}
