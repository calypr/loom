package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ledgerHeader = "unit_id\tfile\tpackage\tkind\tname\treceiver\tlines\texported\traw_tokens\tnormalized_tokens\treferenced_decls\treferencing_decls\taffinity\tin_refs\tout_refs\tself_refs\tranges\tevidence\tdest_package\tdest_file\texports_added\treceiver_bound\ttests_affected"

type listedPackage struct {
	ImportPath string
	Dir        string
	Name       string
	GoFiles    []string
	Export     string
	Module     *struct{ Path string }
	Error      *struct{ Err string }
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found")
		}
		dir = parent
	}
}

func modulePath(root string) string {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func shortPath(path, module string) string {
	if module != "" && path == module {
		return "."
	}
	if module != "" && strings.HasPrefix(path, module+"/") {
		return strings.TrimPrefix(path, module+"/")
	}
	return path
}

func goList(root string, args ...string) ([]listedPackage, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(out)
	var pkgs []listedPackage
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("decode go list: %v: %s", err, stderr.String())
		}
		pkgs = append(pkgs, p)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, stderr.String())
	}
	return pkgs, nil
}

func exportMap(pkgs []listedPackage) map[string]string {
	exports := map[string]string{}
	for _, p := range pkgs {
		if p.Export != "" {
			exports[p.ImportPath] = p.Export
		}
	}
	return exports
}

// Essential: a nil gc lookup resolves only GOROOT/GOPATH packages, so module
// export data must be supplied for cross-package reference edges.
func newExportImporter(fset *token.FileSet, exports map[string]string) types.Importer {
	lookup := func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok || f == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	}
	return importer.ForCompiler(fset, "gc", lookup)
}

func relPath(root, path string) string {
	if filepath.IsAbs(path) {
		if r, err := filepath.Rel(root, path); err == nil {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(path)
}

func absPath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

func escapeField(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func unescapeField(s string) string {
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\r", "\r")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

// Essential: encoding/csv quotes tabs and newlines, so free-text evidence
// survives TSV round trips.
func writeTSV(path string, header []string, rows [][]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Comma = '\t'
	if err := w.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func readTSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	return r.ReadAll()
}

func lowerFirst(id string) string {
	if id == "" {
		return id
	}
	r, n := utf8.DecodeRuneInString(id)
	return string(unicode.ToLower(r)) + id[n:]
}

func receiverBase(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiverBase(e.X)
	case *ast.IndexExpr:
		return receiverBase(e.X)
	case *ast.IndexListExpr:
		return receiverBase(e.X)
	case *ast.ParenExpr:
		return receiverBase(e.X)
	}
	return ""
}

func namedRecvName(t types.Type) string {
	switch t := t.(type) {
	case *types.Named:
		return t.Obj().Name()
	case *types.Pointer:
		return namedRecvName(t.Elem())
	}
	return ""
}

func symbolKey(obj types.Object, module string) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	p := shortPath(obj.Pkg().Path(), module)
	name := obj.Name()
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			recv := namedRecvName(sig.Recv().Type())
			if recv != "" {
				name = recv + "." + name
			}
		}
	}
	return p + "::" + name
}

func escapeSymbols(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
