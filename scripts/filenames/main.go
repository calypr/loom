package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage: filenames <command> [args]

commands:
  inventory
  groupcheck <dir> <selector>
  graphcheck <ledger>
  apply <ledger> <current-package>
  moveverify <base-ref>
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := os.Args[1]
	switch cmd {
	case "inventory":
		err = runInventory(root)
	case "groupcheck":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		err = runGroupcheck(root, os.Args[2], os.Args[3])
	case "graphcheck":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		err = runGraphcheck(root, os.Args[2])
	case "apply":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		err = runApply(root, os.Args[2], os.Args[3])
	case "moveverify":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		err = runMoveverify(root, os.Args[2])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
