package main

import (
	"fmt"
	"sort"
	"strings"
)

type pkgEdge struct {
	from, to string
	vias     map[string]bool
}

func normalizePkg(p string) string {
	p = strings.TrimPrefix(p, "github.com/calypr/loom/")
	p = strings.TrimPrefix(p, "internal/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(p, "/")
	return p
}

func layerRank(pkg string) (int, bool) {
	p := normalizePkg(pkg)
	switch {
	case p == "projectid", p == "authscope", p == "foundation", strings.HasPrefix(p, "foundation/"), p == "fhir", strings.HasPrefix(p, "fhir/"):
		return 0, true
	case p == "store", strings.HasPrefix(p, "store/"):
		return 1, true
	case p == "catalog", strings.HasPrefix(p, "catalog/"), p == "dataset", strings.HasPrefix(p, "dataset/"), p == "semantic", strings.HasPrefix(p, "semantic/"):
		return 2, true
	case p == "ingest", strings.HasPrefix(p, "ingest/"):
		return 3, true
	case p == "dataframe", strings.HasPrefix(p, "dataframe/"):
		return 4, true
	case p == "explorer", strings.HasPrefix(p, "explorer/"):
		return 5, true
	case p == "api", strings.HasPrefix(p, "api/"):
		return 6, true
	case p == "server", strings.HasPrefix(p, "server/"), p == "cmd", strings.HasPrefix(p, "cmd/"):
		return 7, true
	case p == "testkit", strings.HasPrefix(p, "testkit/"), p == "ci", strings.HasPrefix(p, "ci/"), p == "acceptance", strings.HasPrefix(p, "acceptance/"), strings.HasPrefix(p, "tests/"):
		return -1, false
	}
	return 0, false
}

func forbiddenEdge(from, to string) string {
	f := normalizePkg(from)
	t := normalizePkg(to)
	if (f == "explorer" || strings.HasPrefix(f, "explorer/")) &&
		(t == "dataframe/compiler/ir" || strings.HasPrefix(t, "dataframe/compiler/ir/") ||
			t == "dataframe/compiler/lower" || strings.HasPrefix(t, "dataframe/compiler/lower/") ||
			t == "dataframe/compiler/capability" || strings.HasPrefix(t, "dataframe/compiler/capability/")) {
		return "explorer may not import compiler ir/lower/capability directly; use dataframe/compiler/facade"
	}
	if (f == "ingest/sourceload" || strings.HasPrefix(f, "ingest/sourceload/")) &&
		(t == "explorer" || strings.HasPrefix(t, "explorer/") || t == "dataframe" || strings.HasPrefix(t, "dataframe/")) {
		return "ingest/sourceload may not import explorer or dataframe"
	}
	return ""
}

func runGraphcheck(root, ledgerPath string) error {
	rows, err := readTSV(absPath(root, ledgerPath))
	if err != nil {
		return err
	}
	if len(rows) < 1 {
		return fmt.Errorf("empty ledger")
	}
	idx := headerIndex(rows[0])
	for _, col := range []string{"package", "referenced_decls", "dest_package"} {
		if _, ok := idx[col]; !ok {
			return fmt.Errorf("ledger missing column %q", col)
		}
	}
	destBySymbol := map[string]string{}
	for _, r := range rows[1:] {
		if len(r) < len(rows[0]) {
			continue
		}
		pkg := r[idx["package"]]
		dest := r[idx["dest_package"]]
		if dest == "" {
			dest = pkg
		}
		for _, name := range strings.Split(r[idx["name"]], ",") {
			if name != "" {
				destBySymbol[pkg+"::"+name] = dest
			}
		}
	}
	edges := map[string]*pkgEdge{}
	for _, r := range rows[1:] {
		if len(r) < len(rows[0]) {
			continue
		}
		pkg := r[idx["package"]]
		dest := r[idx["dest_package"]]
		if dest == "" {
			dest = pkg
		}
		refs := r[idx["referenced_decls"]]
		if refs == "" {
			continue
		}
		for _, ref := range strings.Split(refs, "|") {
			targetPkg := ref
			if i := strings.Index(ref, "::"); i >= 0 {
				targetPkg = ref[:i]
			} else {
				continue
			}
			tdest := targetPkg
			tail := symbolTail(ref)
			if d, ok := destBySymbol[ref]; ok {
				tdest = d
			} else if i := strings.Index(tail, "."); i >= 0 {
				if d, ok := destBySymbol[targetPkg+"::"+tail[:i]]; ok {
					tdest = d
				}
			}
			if normalizePkg(dest) == normalizePkg(tdest) {
				continue
			}
			key := dest + "\x00" + tdest
			if edges[key] == nil {
				edges[key] = &pkgEdge{from: dest, to: tdest, vias: map[string]bool{}}
			}
			edges[key].vias[ref] = true
		}
	}
	var edgeList []*pkgEdge
	for _, e := range edges {
		edgeList = append(edgeList, e)
	}
	sort.Slice(edgeList, func(i, j int) bool {
		if edgeList[i].from != edgeList[j].from {
			return edgeList[i].from < edgeList[j].from
		}
		return edgeList[i].to < edgeList[j].to
	})

	pkgs := map[string]bool{}
	for _, e := range edgeList {
		pkgs[e.from] = true
		pkgs[e.to] = true
	}
	for _, r := range rows[1:] {
		dest := r[idx["dest_package"]]
		if dest == "" {
			dest = r[idx["package"]]
		}
		pkgs[dest] = true
	}
	fmt.Printf("packages=%d edges=%d\n", len(pkgs), len(edgeList))

	var violations []string
	var forbidden []string
	adj := map[string][]string{}
	for _, e := range edgeList {
		adj[e.from] = append(adj[e.from], e.to)
		if msg := forbiddenEdge(e.from, e.to); msg != "" {
			forbidden = append(forbidden, fmt.Sprintf("%s -> %s: %s", e.from, e.to, msg))
		}
		fr, fok := layerRank(e.from)
		tr, tok := layerRank(e.to)
		if fok && tok && tr > fr {
			violations = append(violations, fmt.Sprintf("%s (rank %d) -> %s (rank %d)", e.from, fr, e.to, tr))
		}
	}
	for p := range adj {
		sort.Strings(adj[p])
	}
	cycles := findCycles(pkgs, adj)

	if len(cycles) == 0 {
		fmt.Println("cycles: none")
	} else {
		for _, c := range cycles {
			fmt.Printf("cycle: %s\n", strings.Join(c, " -> "))
		}
	}
	if len(violations) == 0 {
		fmt.Println("layer violations: none")
	} else {
		for _, v := range violations {
			fmt.Printf("layer violation: %s\n", v)
		}
	}
	if len(forbidden) == 0 {
		fmt.Println("forbidden edges: none")
	} else {
		for _, v := range forbidden {
			fmt.Printf("forbidden edge: %s\n", v)
		}
	}
	if len(cycles)+len(violations)+len(forbidden) > 0 {
		return fmt.Errorf("graphcheck failed")
	}
	return nil
}

func headerIndex(header []string) map[string]int {
	m := map[string]int{}
	for i, h := range header {
		m[h] = i
	}
	return m
}

func symbolTail(ref string) string {
	if i := strings.Index(ref, "::"); i >= 0 {
		return ref[i+2:]
	}
	return ref
}

func findCycles(nodes map[string]bool, adj map[string][]string) [][]string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var cycles [][]string
	seen := map[string]bool{}
	var dfs func(string)
	dfs = func(n string) {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range adj[n] {
			switch color[m] {
			case white:
				dfs(m)
			case gray:
				start := 0
				for i, s := range stack {
					if s == m {
						start = i
						break
					}
				}
				cyc := append([]string{}, stack[start:]...)
				cyc = append(cyc, m)
				key := cycleKey(cyc)
				if !seen[key] {
					seen[key] = true
					cycles = append(cycles, cyc)
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
	}
	var names []string
	for n := range nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if color[n] == white {
			dfs(n)
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return strings.Join(cycles[i], " ") < strings.Join(cycles[j], " ") })
	return cycles
}

func cycleKey(cyc []string) string {
	if len(cyc) == 0 {
		return ""
	}
	body := cyc[:len(cyc)-1]
	best := ""
	for i := range body {
		rot := append(append([]string{}, body[i:]...), body[:i]...)
		s := strings.Join(rot, "\x00")
		if best == "" || s < best {
			best = s
		}
	}
	return best
}
