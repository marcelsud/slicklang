package main

import (
	"errors"
	"fmt"
	"os"

	"slick/internal/compiler"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] != "check" || len(args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: slick check [path]")
		return 2
	}

	path := "."
	if len(args) == 2 {
		path = args[1]
	}
	diagnostics, err := compiler.CheckPath(path)
	if err != nil {
		if errors.Is(err, compiler.ErrNoSources) {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Fprintf(os.Stderr, "check: %v\n", err)
		}
		return 2
	}
	for _, diagnostic := range diagnostics {
		fmt.Printf("%s:%d:%d: error[%s]: %s\n", diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Code, diagnostic.Message)
	}
	if len(diagnostics) > 0 {
		return 1
	}
	fmt.Println("ok")
	return 0
}
