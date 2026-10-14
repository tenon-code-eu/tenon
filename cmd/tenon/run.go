package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

// run exécute la CLI et retourne le code de sortie.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string, wd string) int {
	fs := flag.NewFlagSet("tenon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "afficher la version et quitter")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: tenon [--version]")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}

	if *showVersion {
		fmt.Fprintf(stdout, "tenon %s\n", version)
		return 0
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tenon: argument inattendu: %s\n", fs.Arg(0))
		return 1
	}

	fs.Usage()
	return 0
}
