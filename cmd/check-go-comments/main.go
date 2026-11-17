package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"
)

func main() {
	if err := checkRepository(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func checkRepository() error {
	output, err := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go").Output()
	if err != nil {
		return fmt.Errorf("list repository Go files: %w", err)
	}

	var violations int
	files := token.NewFileSet()
	for _, path := range bytes.Split(output, []byte{0}) {
		if len(path) == 0 {
			continue
		}
		found, err := checkFile(files, string(path), nil)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, position := range found {
			fmt.Fprintf(os.Stderr, "%s: remove this comment or explain an external requirement with // Essential: <reason>\n", position)
		}
		violations += len(found)
	}
	if violations > 0 {
		return fmt.Errorf("%d Go comment groups violate the comment policy", violations)
	}
	return nil
}

func checkFile(files *token.FileSet, filename string, source any) ([]token.Position, error) {
	file, err := parser.ParseFile(files, filename, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	if ast.IsGenerated(file) {
		return nil, nil
	}

	required := requiredComments(file, strings.HasSuffix(filename, "_test.go"))
	var violations []token.Position
	for _, group := range file.Comments {
		if required[group] || essential(group) {
			continue
		}
		for _, comment := range group.List {
			if !directive(comment.Text) {
				violations = append(violations, files.PositionFor(comment.Pos(), false))
				break
			}
		}
	}
	return violations, nil
}

func requiredComments(file *ast.File, testFile bool) map[*ast.CommentGroup]bool {
	required := make(map[*ast.CommentGroup]bool)
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				spec, ok := spec.(*ast.ImportSpec)
				if !ok || spec.Path.Value != `"C"` {
					continue
				}
				if spec.Doc != nil {
					required[spec.Doc] = true
				} else if len(decl.Specs) == 1 && decl.Doc != nil {
					required[decl.Doc] = true
				}
			}
		case *ast.FuncDecl:
			if !testFile || decl.Recv != nil || !strings.HasPrefix(decl.Name.Name, "Example") || decl.Body == nil {
				continue
			}
			suffix := strings.TrimPrefix(decl.Name.Name, "Example")
			first, _ := utf8.DecodeRuneInString(suffix)
			if unicode.IsLower(first) {
				continue
			}
			for i := len(file.Comments) - 1; i >= 0; i-- {
				group := file.Comments[i]
				if group.Pos() <= decl.Body.Lbrace {
					break
				}
				if group.End() >= decl.Body.Rbrace {
					continue
				}
				text := strings.ToLower(strings.TrimSpace(group.Text()))
				if strings.HasPrefix(text, "output:") || strings.HasPrefix(text, "unordered output:") {
					required[group] = true
				}
				break
			}
		}
	}
	return required
}

func essential(group *ast.CommentGroup) bool {
	reason, ok := strings.CutPrefix(group.List[0].Text, "// Essential:")
	return ok && strings.TrimSpace(reason) != ""
}

func directive(comment string) bool {
	if name, ok := strings.CutPrefix(comment, "//go:"); ok {
		if end := strings.IndexAny(name, " \t"); end >= 0 {
			name = name[:end]
		}
		switch name {
		case "build", "cgo_unsafe_args", "debug", "embed", "generate", "linkname",
			"nocheckptr", "noescape", "noinline", "nointerface", "norace", "nosplit",
			"nowritebarrier", "nowritebarrierrec", "registerparams", "systemstack", "yeswritebarrierrec",
			"uintptrkeepalive", "uintptrescapes", "wasmexport", "wasmimport":
			return true
		}
	}
	for _, prefix := range []string{"// +build ", "//line ", "/*line ", "//export ", "//extern "} {
		if strings.HasPrefix(comment, prefix) {
			return true
		}
	}
	return false
}
