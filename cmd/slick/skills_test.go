package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// The agent skills in skills/ are a published contract about this toolchain, and
// they rot silently: a reader cannot tell that an example stopped compiling or
// that a command was added. These tests hold the skills to the compiler and CLI
// they describe.

const (
	languageSkillPath = "../../skills/slick-language/SKILL.md"
	cliSkillPath      = "../../skills/slick-cli/SKILL.md"
)

func readSkill(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	return string(data)
}

// skillPrograms returns every fenced block marked "slk program", which the skill
// presents as a complete source.
func skillPrograms(t *testing.T, text string) []string {
	t.Helper()
	var programs []string
	lines := strings.Split(text, "\n")
	for index := 0; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != "```slk program" {
			continue
		}
		var block []string
		index++
		for index < len(lines) && strings.TrimSpace(lines[index]) != "```" {
			block = append(block, lines[index])
			index++
		}
		programs = append(programs, strings.Join(block, "\n")+"\n")
	}
	return programs
}

// TestSkillProgramsCompileFormatAndPassTheGate proves every example the language
// skill presents as complete source is valid, canonically formatted, and clean
// under the same gate the skill tells an agent to run.
func TestSkillProgramsCompileFormatAndPassTheGate(t *testing.T) {
	programs := skillPrograms(t, readSkill(t, languageSkillPath))
	if len(programs) < 10 {
		t.Fatalf("language skill marks only %d complete programs, so its examples are mostly unverified", len(programs))
	}
	for index, program := range programs {
		t.Run(fmt.Sprintf("program%d", index+1), func(t *testing.T) {
			source := compiler.Source{Name: "main.slk", Namespace: "root", Text: program}
			report, err := compiler.Quality([]compiler.Source{source})
			if err != nil {
				t.Fatalf("analyze example: %v", err)
			}
			if !report.Passed() {
				t.Fatalf("example fails the gate: unformatted=%v diagnostics=%+v\n%s", report.Unformatted, report.Diagnostics, program)
			}
		})
	}
}

// TestSkillsReferenceRegisteredDiagnostics proves every SLK code a skill names
// still exists, so a renumbered or retired rule cannot linger in the docs.
func TestSkillsReferenceRegisteredDiagnostics(t *testing.T) {
	pattern := regexp.MustCompile(`SLK[0-9]{3}`)
	found := false
	for _, path := range []string{languageSkillPath, cliSkillPath} {
		for _, code := range pattern.FindAllString(readSkill(t, path), -1) {
			if code == "SLKxxx" {
				continue
			}
			found = true
			if _, err := compiler.DescribeDiagnostic(code); err != nil {
				t.Fatalf("%s names unregistered %s: %v", path, code, err)
			}
		}
	}
	if !found {
		t.Fatal("no diagnostic codes cited in the skills")
	}
}

// TestCLISkillCoversEveryCommand compares the skill's command table with the
// CLI's own usage text in both directions. A command the CLI gained but the skill
// omits is invisible to an agent; a command the skill names but the CLI does not
// have is a wrong instruction.
func TestCLISkillCoversEveryCommand(t *testing.T) {
	var usage strings.Builder
	reportUsageTo(&usage)
	commands := usageCommands(usage.String())
	if len(commands) < 7 {
		t.Fatalf("usage text lists only %d commands: %q", len(commands), usage.String())
	}
	text := readSkill(t, cliSkillPath)
	for _, command := range commands {
		if !strings.Contains(text, "`slick "+command) {
			t.Fatalf("CLI skill does not document slick %s", command)
		}
	}
	documented := map[string]struct{}{}
	for _, match := range regexp.MustCompile("`slick ([a-z]+)").FindAllStringSubmatch(text, -1) {
		documented[match[1]] = struct{}{}
	}
	for command := range documented {
		if !slices.Contains(commands, command) {
			t.Fatalf("CLI skill documents slick %s, which the toolchain does not have", command)
		}
	}
}

// usageCommands reads the command word out of every usage line, which is the
// CLI's own inventory.
func usageCommands(usage string) []string {
	var commands []string
	for _, line := range strings.Split(strings.TrimSpace(usage), "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "usage:"))
		if len(fields) < 2 || fields[0] != "slick" {
			continue
		}
		commands = append(commands, fields[1])
	}
	return commands
}

// TestLanguageSkillListsEveryStandardNamespace proves the standard-library
// inventory matches the compiler's own description of std.
func TestLanguageSkillListsEveryStandardNamespace(t *testing.T) {
	description, diagnostics, err := compiler.DescribePath("std", "")
	if err != nil || len(diagnostics) > 0 {
		t.Fatalf("describe std: diagnostics=%+v err=%v", diagnostics, err)
	}
	inventory := skillSentence(t, readSkill(t, languageSkillPath), "The compiler owns ")
	documented := 0
	for _, child := range description.Symbol.Children {
		if child.Kind != "namespace" {
			continue
		}
		documented++
		if !strings.Contains(inventory, "`"+child.CanonicalName+"`") {
			t.Fatalf("standard-library inventory omits %s: %s", child.CanonicalName, inventory)
		}
	}
	if documented == 0 {
		t.Fatal("std describes no namespaces")
	}
}

// skillSentence returns the one sentence a claim is stated in, so a check reads
// the inventory rather than any incidental mention elsewhere in the skill.
func skillSentence(t *testing.T, text, prefix string) string {
	t.Helper()
	start := strings.Index(text, prefix)
	if start < 0 {
		t.Fatalf("skill states nothing beginning %q", prefix)
	}
	rest := text[start:]
	end := strings.Index(rest, ". ")
	if end < 0 {
		t.Fatalf("skill sentence beginning %q does not end", prefix)
	}
	return rest[:end]
}

// TestLanguageSkillDocumentsEveryEffect proves each documented authority effect
// is one the compiler accepts, and that the set is closed.
func TestLanguageSkillDocumentsEveryEffect(t *testing.T) {
	effects := []string{"database", "environment", "filesystem", "io", "network", "process", "random", "state", "time"}
	text := readSkill(t, languageSkillPath)
	for _, effect := range effects {
		if !strings.Contains(text, "`"+effect+"`") {
			t.Fatalf("language skill does not document effect %s", effect)
		}
	}
	accepted := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function F() -> null effects { " + strings.Join(effects, ", ") + " } {\n    null\n}\n",
	}})
	if len(accepted) != 0 {
		t.Fatalf("documented effects are not all accepted: %+v", accepted)
	}
	rejected := compiler.Check([]compiler.Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function F() -> null effects { telemetry } {\n    null\n}\n",
	}})
	if len(rejected) == 0 {
		t.Fatal("an unknown effect was accepted, so the documented set is not closed")
	}
}

// TestSkillsDocumentEveryBackendAndTarget holds published skills to the backend
// registry: every backend, advertised target, and alpha opt-in must be stated.
func TestSkillsDocumentEveryBackendAndTarget(t *testing.T) {
	cli := readSkill(t, cliSkillPath)
	language := readSkill(t, languageSkillPath)
	var usage strings.Builder
	reportUsageTo(&usage)
	unescapedCLI := strings.ReplaceAll(cli, `\|`, "|")

	backends := compiler.Backends()
	names := make([]string, 0, len(backends))
	seenTargets := make(map[string]struct{})
	var targets []string
	needsAlpha := false
	for _, backend := range backends {
		name := string(backend.Name)
		names = append(names, name)
		if !strings.Contains(cli, name) {
			t.Fatalf("CLI skill omits backend %s", name)
		}
		if !strings.Contains(language, name) {
			t.Fatalf("language skill omits backend %s", name)
		}
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
			if !strings.Contains(cli, target.Name) {
				t.Fatalf("CLI skill omits target %s", target.Name)
			}
			if !strings.Contains(language, target.Name) {
				t.Fatalf("language skill omits target %s", target.Name)
			}
		}
	}
	sort.Strings(names)
	sort.Strings(targets)
	backendFlag := "--backend=" + strings.Join(names, "|")
	targetFlag := "--target=" + strings.Join(targets, "|")
	if !strings.Contains(usage.String(), backendFlag) || !strings.Contains(unescapedCLI, backendFlag) {
		t.Fatalf("CLI skill build line missing %s", backendFlag)
	}
	if !strings.Contains(usage.String(), targetFlag) || !strings.Contains(unescapedCLI, targetFlag) {
		t.Fatalf("CLI skill build line missing %s", targetFlag)
	}
	if needsAlpha {
		if !strings.Contains(cli, "--allow-alpha") {
			t.Fatal("CLI skill omits --allow-alpha")
		}
		if !strings.Contains(language, "--allow-alpha") {
			t.Fatal("language skill omits --allow-alpha")
		}
	}
}
