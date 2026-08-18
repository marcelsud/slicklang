package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// TestCLIRegistryMatchesBackends is the CLI release gate for issue #109: usage
// text, accepted --backend/--target values, and the stability/eligibility
// slick describe reports must match compiler.Backends() exactly.
func TestCLIRegistryMatchesBackends(t *testing.T) {
	backends := compiler.Backends()
	if len(backends) == 0 {
		t.Fatal("backend registry is empty")
	}

	names := make([]string, 0, len(backends))
	seenTargets := make(map[string]struct{})
	var targets []string
	needsAlpha := false
	for _, backend := range backends {
		names = append(names, string(backend.Name))
		if backend.Stability == compiler.StabilityAlpha {
			needsAlpha = true
		}
		for _, target := range backend.Targets {
			if target.Stability == compiler.StabilityAlpha {
				needsAlpha = true
			}
			if _, exists := seenTargets[target.Name]; exists {
				continue
			}
			seenTargets[target.Name] = struct{}{}
			targets = append(targets, target.Name)
		}
	}
	sort.Strings(names)
	sort.Strings(targets)

	var usage strings.Builder
	reportUsageTo(&usage)
	text := usage.String()
	wantBackend := "--backend=" + strings.Join(names, "|")
	wantTarget := "--target=" + strings.Join(targets, "|")
	if !strings.Contains(text, wantBackend) {
		t.Fatalf("usage does not list registry backends %q:\n%s", wantBackend, text)
	}
	if !strings.Contains(text, wantTarget) {
		t.Fatalf("usage does not list registry targets %q:\n%s", wantTarget, text)
	}
	if needsAlpha && !strings.Contains(text, "--allow-alpha") {
		t.Fatalf("usage omits --allow-alpha for alpha registry entries:\n%s", text)
	}

	for _, backend := range backends {
		_, _, options, err := parseBuildOptions([]string{"-o", "out", "--backend=" + string(backend.Name)})
		if err != nil {
			t.Fatalf("registry backend %s rejected: %v", backend.Name, err)
		}
		if options.Backend != backend.Name {
			t.Fatalf("registry backend %s parsed as %q", backend.Name, options.Backend)
		}
		for _, target := range backend.Targets {
			_, _, options, err := parseBuildOptions([]string{
				"-o", "out",
				"--backend=" + string(backend.Name),
				"--target=" + target.Name,
			})
			if err != nil {
				t.Fatalf("registry target %s for %s rejected: %v", target.Name, backend.Name, err)
			}
			if options.Target != target.Name {
				t.Fatalf("registry target %s parsed as %q", target.Name, options.Target)
			}
		}
	}
	if _, _, _, err := parseBuildOptions([]string{"-o", "out", "--backend=native"}); err == nil {
		t.Fatal("unknown backend native was accepted")
	}
	if _, _, _, err := parseBuildOptions([]string{"-o", "out", "--target=native"}); err == nil {
		t.Fatal("unknown target native was accepted")
	}

	var stdout, stderr bytes.Buffer
	if status := runDescribe([]string{"--json", "std.env.Get"}, &stdout, &stderr); status != 0 || stderr.Len() != 0 {
		t.Fatalf("describe status=%d stderr=%q", status, stderr.String())
	}
	var document struct {
		Symbol struct {
			Stability compiler.Stability `json:"stability"`
			Eligible  *bool              `json:"eligible"`
		} `json:"symbol"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("describe json: %v", err)
	}
	if document.Symbol.Eligible == nil {
		t.Fatal("slick describe omitted eligibility")
	}
	if document.Symbol.Stability != compiler.StabilityStable && document.Symbol.Stability != compiler.StabilityAlpha {
		t.Fatalf("slick describe stability %q is outside the registry vocabulary", document.Symbol.Stability)
	}
	for _, backend := range backends {
		if backend.Stability != compiler.StabilityStable && backend.Stability != compiler.StabilityAlpha {
			t.Fatalf("backend %s stability %q is not reportable by slick describe", backend.Name, backend.Stability)
		}
		for _, target := range backend.Targets {
			if target.Stability != compiler.StabilityStable && target.Stability != compiler.StabilityAlpha {
				t.Fatalf("target %s stability %q is not reportable by slick describe", target.Name, target.Stability)
			}
		}
	}
}
