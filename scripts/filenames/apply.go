package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type applyRow struct {
	file       string
	destPkg    string
	destFile   string
	ranges     []applyRange
	curPackage string
}

type applyRange struct {
	file  string
	start int
	end   int
}

func parseRanges(s string) []applyRange {
	var out []applyRange
	for _, item := range strings.Split(s, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		colon := strings.LastIndex(item, ":")
		if colon < 0 {
			continue
		}
		file := item[:colon]
		bounds := item[colon+1:]
		var start, end int
		if _, err := fmt.Sscanf(bounds, "%d-%d", &start, &end); err != nil {
			continue
		}
		out = append(out, applyRange{file: file, start: start, end: end})
	}
	return out
}

func runApply(root, ledgerPath, currentPkg string) error {
	rows, err := readTSV(absPath(root, ledgerPath))
	if err != nil {
		return err
	}
	if len(rows) < 1 {
		return fmt.Errorf("empty ledger")
	}
	idx := headerIndex(rows[0])
	for _, col := range []string{"file", "package", "ranges", "dest_package", "dest_file"} {
		if _, ok := idx[col]; !ok {
			return fmt.Errorf("ledger missing column %q", col)
		}
	}
	want := normalizePkg(currentPkg)
	var selected []applyRow
	for _, r := range rows[1:] {
		if len(r) < len(rows[0]) {
			continue
		}
		if normalizePkg(r[idx["package"]]) != want {
			continue
		}
		destPkg := r[idx["dest_package"]]
		if destPkg == "" {
			destPkg = r[idx["package"]]
		}
		destFile := r[idx["dest_file"]]
		if normalizePkg(destPkg) == normalizePkg(r[idx["package"]]) && destFile == "" {
			continue
		}
		selected = append(selected, applyRow{
			file:       r[idx["file"]],
			destPkg:    destPkg,
			destFile:   destFile,
			ranges:     parseRanges(r[idx["ranges"]]),
			curPackage: r[idx["package"]],
		})
	}
	if len(selected) == 0 {
		fmt.Printf("apply: no approved rows for %s\n", currentPkg)
		return nil
	}
	byFile := map[string][]applyRow{}
	for _, r := range selected {
		seenFile := map[string]bool{}
		for _, rg := range r.ranges {
			if rg.file == "" || seenFile[rg.file] {
				continue
			}
			seenFile[rg.file] = true
			byFile[rg.file] = append(byFile[rg.file], r)
		}
		if len(seenFile) == 0 {
			byFile[r.file] = append(byFile[r.file], r)
		}
	}
	var files []string
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	touched := map[string]bool{}
	for _, file := range files {
		rowsForFile := byFile[file]
		sort.Slice(rowsForFile, func(i, j int) bool {
			return firstStart(rowsForFile[i], file) < firstStart(rowsForFile[j], file)
		})
		srcAbs := absPath(root, file)
		srcBytes, err := os.ReadFile(srcAbs)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, srcAbs, srcBytes, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", file, err)
		}
		oldPkg := af.Name.Name
		destPkg := rowsForFile[0].destPkg
		sameDest := true
		for _, r := range rowsForFile[1:] {
			if normalizePkg(r.destPkg) != normalizePkg(destPkg) {
				sameDest = false
			}
		}
		destFileName := rowsForFile[0].destFile
		for _, r := range rowsForFile[1:] {
			if r.destFile != destFileName {
				sameDest = false
			}
		}
		allDeclsCovered := declsCovered(fset, af, rowsForFile)
		primaryOnly := true
		for _, r := range rowsForFile {
			if r.file != file {
				primaryOnly = false
				break
			}
		}
		if sameDest && allDeclsCovered && primaryOnly {
			target := destPath(root, destPkg, destFileName, file)
			if target == srcAbs {
				continue
			}
			if _, statErr := os.Stat(target); statErr == nil {
				// A merge target already exists: append this unit below instead.
			} else {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				if err := gitMove(root, srcAbs, target); err != nil {
					return err
				}
				newName := packageName(destPkg)
				if newName != oldPkg {
					if err := rewritePackage(target, oldPkg, newName); err != nil {
						return err
					}
				}
				touched[target] = true
				continue
			}
		}
		pieces := map[string][][]byte{}
		var cut []applyRange
		var keepRows int
		for _, r := range rowsForFile {
			if normalizePkg(r.destPkg) == normalizePkg(r.curPackage) && (r.destFile == "" || filepath.Base(r.destFile) == filepath.Base(file)) {
				keepRows++
				continue
			}
			var rel []applyRange
			for _, rg := range r.ranges {
				if rg.file != "" && rg.file != file {
					continue
				}
				if rg.start < 0 || rg.end > len(srcBytes) || rg.start > rg.end {
					continue
				}
				rel = append(rel, rg)
				cut = append(cut, rg)
			}
			if len(rel) == 0 {
				continue
			}
			key := r.destPkg + "\x00" + r.destFile
			pieces[key] = append(pieces[key], extractRanges(srcBytes, rel))
		}
		_ = keepRows
		var keys []string
		for k := range pieces {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts := strings.SplitN(k, "\x00", 2)
			destPkg, destFile := parts[0], parts[1]
			target := destPath(root, destPkg, destFile, file)
			if target == srcAbs {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			newFile := false
			if _, err := os.Stat(target); os.IsNotExist(err) {
				newFile = true
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return err
			}
			if newFile {
				if _, err := fmt.Fprintf(f, "package %s\n\n", packageName(destPkg)); err != nil {
					f.Close()
					return err
				}
			} else {
				if _, err := f.WriteString("\n"); err != nil {
					f.Close()
					return err
				}
			}
			for _, piece := range pieces[k] {
				if _, err := f.Write(piece); err != nil {
					f.Close()
					return err
				}
				if len(piece) == 0 || piece[len(piece)-1] != '\n' {
					if _, err := f.WriteString("\n"); err != nil {
						f.Close()
						return err
					}
				}
				if _, err := f.WriteString("\n"); err != nil {
					f.Close()
					return err
				}
			}
			f.Close()
			touched[target] = true
		}
		if len(cut) == 0 {
			continue
		}
		remaining := removeRanges(srcBytes, cut)
		remaining = removeUnusedImports(remaining, srcAbs)
		if !hasTopLevelDecl(remaining) {
			if err := os.Remove(srcAbs); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(srcAbs, remaining, 0o644); err != nil {
			return err
		}
		touched[srcAbs] = true
	}
	if err := runGofmt(touched); err != nil {
		return err
	}
	fmt.Printf("apply: processed %d rows in %d files for %s\n", len(selected), len(files), currentPkg)
	return nil
}

func packageName(pkg string) string {
	p := strings.TrimSuffix(filepath.ToSlash(pkg), "/")
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

func destPath(root, destPkg, destFile, sourceFile string) string {
	destPkg = strings.TrimPrefix(destPkg, "github.com/calypr/loom/")
	var dir string
	if filepath.IsAbs(destPkg) {
		dir = destPkg
	} else if strings.HasPrefix(destPkg, "internal/") || destPkg == "internal" {
		dir = filepath.Join(root, filepath.FromSlash(destPkg))
	} else {
		dir = filepath.Join(root, filepath.FromSlash(destPkg))
	}
	name := destFile
	if name == "" {
		name = filepath.Base(sourceFile)
	}
	if strings.ContainsAny(name, "/\\") {
		if strings.HasPrefix(name, "internal/") {
			return filepath.Join(root, filepath.FromSlash(name))
		}
		return filepath.Join(dir, filepath.FromSlash(name))
	}
	return filepath.Join(dir, name)
}

func gitMove(root, src, dst string) error {
	cmd := exec.Command("git", "mv", src, dst)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git mv %s %s: %v: %s", src, dst, err, out)
	}
	return nil
}

func rewritePackage(path, oldPkg, newPkg string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	old := "package " + oldPkg
	i := bytes.Index(b, []byte(old))
	if i < 0 {
		return fmt.Errorf("%s: package %s not found", path, oldPkg)
	}
	b = append(append(append([]byte{}, b[:i]...), []byte("package "+newPkg)...), b[i+len(old):]...)
	return os.WriteFile(path, b, 0o644)
}

func declsCovered(fset *token.FileSet, af *ast.File, rows []applyRow) bool {
	var ranges []applyRange
	for _, r := range rows {
		ranges = append(ranges, r.ranges...)
	}
	tf := fset.File(af.Pos())
	for _, d := range af.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		off := tf.Offset(d.Pos())
		covered := false
		for _, rg := range ranges {
			if off >= rg.start && off < rg.end {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func extractRanges(src []byte, ranges []applyRange) []byte {
	var out []byte
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	for _, rg := range ranges {
		if rg.start < 0 || rg.end > len(src) || rg.start > rg.end {
			continue
		}
		out = append(out, src[rg.start:rg.end]...)
	}
	return out
}

func removeRanges(src []byte, ranges []applyRange) []byte {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start > ranges[j].start })
	out := append([]byte{}, src...)
	for _, rg := range ranges {
		if rg.start < 0 || rg.end > len(out) {
			continue
		}
		out = append(out[:rg.start], out[rg.end:]...)
	}
	return out
}

func hasTopLevelDecl(src []byte) bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		return len(bytes.TrimSpace(src)) > 0
	}
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		return true
	}
	return false
}

func removeUnusedImports(src []byte, filename string) []byte {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return src
	}
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if is, ok := n.(*ast.ImportSpec); ok {
			_ = is
			return false
		}
		if id, ok := n.(*ast.Ident); ok {
			used[id.Name] = true
		}
		return true
	})
	var removes []applyRange
	tf := fset.File(f.Pos())
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		var specRemovals []applyRange
		for _, spec := range gd.Specs {
			is := spec.(*ast.ImportSpec)
			local := ""
			if is.Name != nil {
				local = is.Name.Name
			} else {
				path := strings.Trim(is.Path.Value, "\"")
				local = path[strings.LastIndex(path, "/")+1:]
			}
			if local == "_" || local == "." || used[local] {
				continue
			}
			specRemovals = append(specRemovals, applyRange{start: tf.Offset(is.Pos()), end: tf.Offset(is.End())})
		}
		if len(specRemovals) == len(gd.Specs) {
			removes = append(removes, applyRange{start: tf.Offset(gd.Pos()), end: tf.Offset(gd.End())})
		} else {
			removes = append(removes, specRemovals...)
		}
	}
	if len(removes) == 0 {
		return src
	}
	return removeRanges(src, removes)
}

func runGofmt(files map[string]bool) error {
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		return fmt.Errorf("gofmt not found: %w", err)
	}
	var list []string
	for f := range files {
		list = append(list, f)
	}
	sort.Strings(list)
	for _, f := range list {
		cmd := exec.Command(gofmt, "-w", f)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("gofmt %s: %v: %s", f, err, out)
		}
	}
	return nil
}

func firstStart(r applyRow, file string) int {
	best := int(^uint(0) >> 1)
	for _, rg := range r.ranges {
		if rg.file == "" || rg.file == file {
			if rg.start < best {
				best = rg.start
			}
		}
	}
	return best
}
