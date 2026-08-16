package compiler

import (
	"fmt"
	"sort"
	"strings"
	"text/scanner"
)

// CallableQuality is what the analyzer measured about one authored callable.
// Symbol is its canonical name; a lambda is labelled by its owner and its own
// expression position.
type CallableQuality struct {
	Symbol               string
	File                 string
	Line                 int
	CodeLines            int
	CyclomaticComplexity int
	CognitiveComplexity  int
}

// QualityReport is one source-level decision about a project: canonical
// formatting, compiler validity, semantic lint, and per-callable complexity,
// measured from one load and one compilation.
//
// Compiled is false when the sources produced compiler errors. Formatting, lint,
// and complexity are then not analyzed at all, because claims read off an
// invalid AST would cascade from one mistake.
type QualityReport struct {
	Files       int
	CodeLines   int
	Compiled    bool
	Unformatted []string
	Diagnostics []Diagnostic
	Callables   []CallableQuality
}

// QualityStatus is the verdict of one report section.
type QualityStatus string

const (
	QualityStatusPass QualityStatus = "PASS"
	QualityStatusFail QualityStatus = "FAIL"
	// QualityStatusSkip marks a section that was not analyzed because
	// compilation failed first.
	QualityStatusSkip QualityStatus = "SKIP"
)

// QualitySection is one named part of the gate, in the order a report prints it.
type QualitySection struct {
	Name   string
	Status QualityStatus
}

// Quality analyzes sources once and reports every fact the gate decides on. An
// error means the analysis itself failed; it is never a passing report.
func Quality(sources []Source) (QualityReport, error) {
	report := QualityReport{Files: len(sources)}
	codeLines := make(map[string]map[int]struct{}, len(sources))
	for _, source := range sources {
		lines := sourceCodeLines(source)
		codeLines[source.Name] = lines
		report.CodeLines += len(lines)
	}

	prog, diagnostics := compile(sources)
	if len(diagnostics) > 0 {
		report.Diagnostics = diagnostics
		return report, nil
	}
	report.Compiled = true

	for _, source := range sources {
		formatted, formatDiagnostics, err := Format(source)
		if err != nil {
			return QualityReport{}, fmt.Errorf("format %s: %w", source.Name, err)
		}
		if len(formatDiagnostics) > 0 {
			return QualityReport{}, fmt.Errorf("%s compiles but does not parse for formatting", source.Name)
		}
		if formatted != source.Text {
			report.Unformatted = append(report.Unformatted, source.Name)
		}
	}
	sort.Strings(report.Unformatted)

	report.Diagnostics = prog.lint()
	callables, complexity, err := prog.measureCallables(codeLines)
	if err != nil {
		return QualityReport{}, err
	}
	report.Callables = callables
	report.Diagnostics = append(report.Diagnostics, complexity...)
	sortDiagnostics(report.Diagnostics)
	return report, nil
}

// QualityPath analyzes the .slk file at path, or every .slk file under it.
func QualityPath(path string) (QualityReport, error) {
	sources, err := loadSources(path)
	if err != nil {
		return QualityReport{}, err
	}
	return Quality(sources)
}

// Passed is the gate: canonical formatting, no compiler error, no lint warning,
// and every callable inside both complexity limits. Code lines are navigation
// evidence and never decide it.
func (report QualityReport) Passed() bool {
	return report.Compiled && len(report.Unformatted) == 0 && len(report.Diagnostics) == 0
}

// Sections reports each part of the gate in fixed order.
func (report QualityReport) Sections() []QualitySection {
	if !report.Compiled {
		return []QualitySection{
			{Name: "FORMAT", Status: QualityStatusSkip},
			{Name: "CHECK", Status: QualityStatusFail},
			{Name: "LINT", Status: QualityStatusSkip},
			{Name: "COMPLEXITY", Status: QualityStatusSkip},
		}
	}
	return []QualitySection{
		{Name: "FORMAT", Status: statusOf(len(report.Unformatted) == 0)},
		{Name: "CHECK", Status: QualityStatusPass},
		{Name: "LINT", Status: statusOf(report.count(isLintCode) == 0)},
		{Name: "COMPLEXITY", Status: statusOf(report.ComplexityViolations() == 0)},
	}
}

func statusOf(passed bool) QualityStatus {
	if passed {
		return QualityStatusPass
	}
	return QualityStatusFail
}

// Errors counts the compiler errors the report holds.
func (report QualityReport) Errors() int {
	return report.count(func(diagnostic Diagnostic) bool {
		return diagnostic.Severity == DiagnosticSeverityError
	})
}

// Warnings counts every finding about a valid program, lint and complexity
// alike.
func (report QualityReport) Warnings() int {
	return report.count(func(diagnostic Diagnostic) bool {
		return diagnostic.Severity == DiagnosticSeverityWarning
	})
}

// ComplexityViolations counts the callable metrics that exceed their limit. One
// callable over both limits reports both.
func (report QualityReport) ComplexityViolations() int {
	return report.count(isComplexityCode)
}

func (report QualityReport) count(matches func(Diagnostic) bool) int {
	total := 0
	for _, diagnostic := range report.Diagnostics {
		if matches(diagnostic) {
			total++
		}
	}
	return total
}

func isLintCode(diagnostic Diagnostic) bool {
	return diagnosticPhaseOf(diagnostic) == DiagnosticPhaseLint
}

func isComplexityCode(diagnostic Diagnostic) bool {
	return diagnosticPhaseOf(diagnostic) == DiagnosticPhaseQuality
}

func diagnosticPhaseOf(diagnostic Diagnostic) DiagnosticPhase {
	return diagnosticRegistry[diagnosticCode(diagnostic.Code)].Phase
}

// MaxCyclomatic, MaxCognitive, and LargestCallable name the callable that owns
// each maximum. Ties are broken by canonical symbol and then source position, so
// one project always reports the same callable.
func (report QualityReport) MaxCyclomatic() (CallableQuality, bool) {
	return report.maximum(func(callable CallableQuality) int { return callable.CyclomaticComplexity })
}

func (report QualityReport) MaxCognitive() (CallableQuality, bool) {
	return report.maximum(func(callable CallableQuality) int { return callable.CognitiveComplexity })
}

func (report QualityReport) LargestCallable() (CallableQuality, bool) {
	return report.maximum(func(callable CallableQuality) int { return callable.CodeLines })
}

func (report QualityReport) maximum(metric func(CallableQuality) int) (CallableQuality, bool) {
	var best CallableQuality
	found := false
	for _, callable := range report.Callables {
		if !found || metric(callable) > metric(best) || (metric(callable) == metric(best) && precedes(callable, best)) {
			best = callable
			found = true
		}
	}
	return best, found
}

func precedes(callable, other CallableQuality) bool {
	if callable.Symbol != other.Symbol {
		return callable.Symbol < other.Symbol
	}
	if callable.File != other.File {
		return callable.File < other.File
	}
	return callable.Line < other.Line
}

// measureCallables scores every authored callable, including each lambda as its
// own callable. An AST form the metric walker does not classify fails the
// analysis: a silently ignored control-flow node would make the gate stale.
func (p *program) measureCallables(codeLines map[string]map[int]struct{}) ([]CallableQuality, []Diagnostic, error) {
	var measured []CallableQuality
	var diagnostics []Diagnostic
	unclassified := make(map[string]struct{})
	queue := p.callableSubjects()
	for index := 0; index < len(queue); index++ {
		subject := queue[index]
		walker := measureCallable(subject.body)
		for _, form := range walker.unclassified {
			unclassified[form] = struct{}{}
		}
		callable := CallableQuality{
			Symbol:               subject.symbol,
			File:                 subject.pos.file,
			Line:                 subject.pos.line,
			CodeLines:            countCodeLines(codeLines[subject.pos.file], subject.pos.line, subject.end.line),
			CyclomaticComplexity: walker.score.cyclomatic,
			CognitiveComplexity:  walker.score.cognitive,
		}
		measured = append(measured, callable)
		if callable.CyclomaticComplexity > QualityCyclomaticLimit {
			diagnostics = append(diagnostics, newDiagnostic(subject.pos, diagnosticCodeCyclomaticComplexity,
				"cyclomatic complexity %d exceeds limit %d in %s", callable.CyclomaticComplexity, QualityCyclomaticLimit, callable.Symbol))
		}
		if callable.CognitiveComplexity > QualityCognitiveLimit {
			diagnostics = append(diagnostics, newDiagnostic(subject.pos, diagnosticCodeCognitiveComplexity,
				"cognitive complexity %d exceeds limit %d in %s", callable.CognitiveComplexity, QualityCognitiveLimit, callable.Symbol))
		}
		for _, lambda := range walker.lambdas {
			queue = append(queue, lambdaSubject(subject, lambda))
		}
	}
	if len(unclassified) > 0 {
		return nil, nil, fmt.Errorf("quality analysis does not classify %s", strings.Join(sortedKeys(unclassified), ", "))
	}
	sort.SliceStable(measured, func(first, second int) bool {
		left, right := measured[first], measured[second]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		return left.Symbol < right.Symbol
	})
	return measured, diagnostics, nil
}

// sourceCodeLines is the set of physical lines that carry code: a line is code
// when a non-comment token covers it, which excludes blank lines, comment lines,
// and the interior of a block comment while keeping a multi-line string.
func sourceCodeLines(source Source) map[int]struct{} {
	tokens, _ := scanTokens(source, true)
	lines := make(map[int]struct{}, len(tokens))
	for _, tok := range tokens {
		if tok.kind == scanner.Comment || tok.kind == scanner.EOF {
			continue
		}
		last := tok.pos.line + strings.Count(tok.text, "\n")
		for line := tok.pos.line; line <= last; line++ {
			lines[line] = struct{}{}
		}
	}
	return lines
}

// countCodeLines measures one callable the same way: the code lines from its
// declaration through the closing brace of its body.
func countCodeLines(lines map[int]struct{}, start, end int) int {
	if len(lines) == 0 {
		return 0
	}
	if end < start {
		end = start
	}
	total := 0
	for line := start; line <= end; line++ {
		if _, code := lines[line]; code {
			total++
		}
	}
	return total
}
