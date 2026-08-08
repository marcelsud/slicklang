package main

import "testing"

func TestParseBuildArgsAcceptsOutputAfterProject(t *testing.T) {
	path, output, err := parseBuildArgs([]string{"examples/hello", "-o", "bin/hello"})
	if err != nil {
		t.Fatalf("parse build arguments: %v", err)
	}
	if path != "examples/hello" || output != "bin/hello" {
		t.Fatalf("unexpected build arguments: path=%q output=%q", path, output)
	}
}

func TestParseBuildArgsRequiresOutput(t *testing.T) {
	if _, _, err := parseBuildArgs([]string{"examples/hello"}); err == nil {
		t.Fatal("build arguments accepted a missing output")
	}
}
