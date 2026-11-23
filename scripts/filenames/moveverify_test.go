package main

import "testing"

func TestExtractDeclsNormalizesQualifierAndExportCase(t *testing.T) {
	base := []mvFile{{path: "x/a.go", src: []byte(`package a

import old "example.com/old/pkg"

func foo(v old.T) old.T { return v }
`)}}
	head := []mvFile{{path: "x/a.go", src: []byte(`package b

import newpkg "example.com/new/pkg"

func Foo(v newpkg.T) newpkg.T { return v }
`)}}
	bd, err := extractDecls(base)
	if err != nil {
		t.Fatal(err)
	}
	hd, err := extractDecls(head)
	if err != nil {
		t.Fatal(err)
	}
	if len(bd) != 1 || len(hd) != 1 {
		t.Fatalf("got %d base, %d head declarations", len(bd), len(hd))
	}
	if bd[0].Hash != hd[0].Hash {
		t.Fatalf("hash changed after qualifier/export normalization:\nbase=%s\nhead=%s", bd[0].Hash, hd[0].Hash)
	}
	missing, added, changed := compareDecls(bd, hd)
	if len(missing)+len(added)+len(changed) != 0 {
		t.Fatalf("unexpected differences: %v %v %v", missing, added, changed)
	}
}

func TestTypeMethodsAreOrderIndependent(t *testing.T) {
	base := []mvFile{{path: "x/a.go", src: []byte(`package a

type T struct{}

func (T) One() {}
func (T) Two() {}
`)}}
	head := []mvFile{{path: "y/b.go", src: []byte(`package b

type T struct{}

func (T) Two() {}
func (T) One() {}
`)}}
	bd, err := extractDecls(base)
	if err != nil {
		t.Fatal(err)
	}
	hd, err := extractDecls(head)
	if err != nil {
		t.Fatal(err)
	}
	missing, added, changed := compareDecls(bd, hd)
	if len(missing)+len(added)+len(changed) != 0 {
		t.Fatalf("method order changed hash: %v %v %v", missing, added, changed)
	}
}

func TestCompareDeclsReportsChanges(t *testing.T) {
	base := []mvDecl{{Kind: "func", Name: "F", File: "a.go", Hash: "one"}}
	head := []mvDecl{{Kind: "func", Name: "F", File: "a.go", Hash: "two"}}
	missing, added, changed := compareDecls(base, head)
	if len(missing) != 0 || len(added) != 0 || len(changed) != 1 {
		t.Fatalf("got missing=%d added=%d changed=%d", len(missing), len(added), len(changed))
	}
}
