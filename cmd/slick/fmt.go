package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"slick/internal/compiler"
)

type formattedFile struct {
	path string
	text string
	mode os.FileMode
}

func runFmt(args []string, stdout, stderr io.Writer) int {
	path, check, err := parseFmtArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return reportUsageTo(stderr)
	}

	sources, err := compiler.LoadSources(path)
	if err != nil {
		if errors.Is(err, compiler.ErrNoSources) {
			fmt.Fprintln(stderr, err)
		} else {
			fmt.Fprintf(stderr, "fmt: %v\n", err)
		}
		return 2
	}

	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(stderr, "fmt: %v\n", err)
		return 2
	}
	files := make([]formattedFile, 0, len(sources))
	invalid := false
	for _, source := range sources {
		formatted, diagnostics, formatErr := compiler.Format(source)
		if formatErr != nil {
			fmt.Fprintf(stderr, "fmt: %v\n", formatErr)
			return 2
		}
		if len(diagnostics) > 0 {
			invalid = true
			for _, diagnostic := range diagnostics {
				fmt.Fprintf(stderr, "%s:%d:%d: error[%s]: %s\n", diagnostic.File, diagnostic.Line, diagnostic.Column, diagnostic.Code, diagnostic.Message)
			}
			continue
		}
		file := path
		if info.IsDir() {
			file = filepath.Join(path, filepath.FromSlash(source.Name))
		}
		fileInfo, statErr := os.Stat(file)
		if statErr != nil {
			fmt.Fprintf(stderr, "fmt: %v\n", statErr)
			return 2
		}
		files = append(files, formattedFile{path: file, text: formatted, mode: fileInfo.Mode()})
	}
	if invalid {
		return 1
	}

	different := false
	for index, source := range sources {
		if files[index].text == source.Text {
			continue
		}
		different = true
		if check {
			fmt.Fprintln(stdout, files[index].path)
		}
	}
	if check {
		if different {
			return 1
		}
		return 0
	}

	for index, source := range sources {
		if files[index].text == source.Text {
			continue
		}
		if err := replaceFile(files[index]); err != nil {
			fmt.Fprintf(stderr, "fmt: %v\n", err)
			return 2
		}
	}
	return 0
}

func parseFmtArgs(args []string) (path string, check bool, err error) {
	path = "."
	pathSet := false
	for _, arg := range args {
		switch {
		case arg == "--check":
			if check {
				return "", false, errors.New("fmt --check may only be specified once")
			}
			check = true
		case strings.HasPrefix(arg, "-"):
			return "", false, fmt.Errorf("unknown fmt flag %q", arg)
		default:
			if pathSet {
				return "", false, fmt.Errorf("unexpected fmt argument %q", arg)
			}
			path = arg
			pathSet = true
		}
	}
	return path, check, nil
}

func replaceFile(file formattedFile) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(file.path), "."+filepath.Base(file.path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if _, err = io.WriteString(temporary, file.text); err != nil {
		return err
	}
	if err = temporary.Chmod(file.mode); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, file.path)
}
