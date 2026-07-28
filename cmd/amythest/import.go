package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/base698/amythest/internal/bases"
)

// runImport implements `amythest import csv <file>... [-table name] [-data dir]`.
func runImport(args []string) {
	if len(args) == 0 || args[0] != "csv" {
		fmt.Fprintln(os.Stderr, "usage: amythest import csv <file.csv>... [-table name] [-data dir]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("import csv", flag.ExitOnError)
	table := fs.String("table", "", "table name (default: file basename); only valid with a single file")
	dataDir := fs.String("data", "data", "data directory holding catalog.db")
	// Allow flags after the file list.
	var files []string
	rest := args[1:]
	for len(rest) > 0 && rest[0] != "" && rest[0][0] != '-' {
		files = append(files, rest[0])
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		os.Exit(2)
	}
	files = append(files, fs.Args()...)
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no CSV files given")
		os.Exit(2)
	}
	if *table != "" && len(files) > 1 {
		fmt.Fprintln(os.Stderr, "-table only makes sense with one file")
		os.Exit(2)
	}

	cat, err := bases.OpenCatalog(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open catalog:", err)
		os.Exit(1)
	}
	defer cat.Close()

	for _, f := range files {
		n, err := cat.ImportCSV(f, *table)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import %s: %v\n", f, err)
			os.Exit(1)
		}
		fmt.Printf("imported %s: %d rows\n", f, n)
	}
}
