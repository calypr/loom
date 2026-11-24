//go:build ignore

package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
)

type declaration struct {
	Name  string
	Names []string
	Start int
	End   int
	Uses  []string
}

func main() {
	out := map[string][]declaration{}
	for _, path := range os.Args[1:] {
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		out[path] = nil
		for _, node := range file.Decls {
			start := node.Pos()
			var names []string
			switch x := node.(type) {
			case *ast.FuncDecl:
				if x.Recv != nil {
					continue
				}
				names = []string{x.Name.Name}
				if x.Doc != nil {
					start = x.Doc.Pos()
				}
			case *ast.GenDecl:
				if x.Tok == token.IMPORT {
					continue
				}
				if x.Doc != nil {
					start = x.Doc.Pos()
				}
				for _, spec := range x.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names = append(names, s.Name.Name)
					case *ast.ValueSpec:
						for _, name := range s.Names {
							names = append(names, name.Name)
						}
					}
				}
			}
			ignored := map[*ast.Ident]bool{}
			uses := map[string]bool{}
			ast.Inspect(node, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					ignored[x.Sel] = true
				case *ast.Ident:
					if !ignored[x] && (x.Obj == nil || x.Obj.Decl != nil) {
						uses[x.Name] = true
					}
				}
				return true
			})
			list := make([]string, 0, len(uses))
			for name := range uses {
				list = append(list, name)
			}
			slices.Sort(list)
			out[path] = append(out[path], declaration{strings.Join(names, ","), names, positions.Position(start).Offset, positions.Position(node.End()).Offset, list})
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
