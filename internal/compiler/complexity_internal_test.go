package compiler

import (
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"path/filepath"
	"strings"
	"testing"
)

// unclassifiedStatement is an AST form the metric walker has never seen, which
// stands in for a statement node a future language feature adds.
type unclassifiedStatement struct {
	pos position
}

func (n *unclassifiedStatement) statementPos() position { return n.pos }

// TestComplexityWalkerClassifiesEveryASTNode is the completeness gate: a new
// statement or expression node must be given an explicit metric decision in the
// walker before it can be measured. Without this, new control-flow syntax would
// silently score zero and the gate would go stale.
func TestComplexityWalkerClassifiesEveryASTNode(t *testing.T) {
	nodes := astNodeTypes(t)
	if len(nodes) < 20 {
		t.Fatalf("found only %d AST node types, so the scan is wrong: %v", len(nodes), sortedKeys(nodes))
	}
	classified := complexityWalkerCases(t)
	for _, node := range sortedKeys(nodes) {
		if _, decided := classified[node]; !decided {
			t.Fatalf("complexity walker has no case for %s; classify its metric behavior in complexity.go", node)
		}
	}
	for _, node := range sortedKeys(classified) {
		if _, exists := nodes[node]; !exists {
			t.Fatalf("complexity walker classifies %s, which is no longer an AST node", node)
		}
	}
}

// TestUnclassifiedNodeFailsAnalysis proves the walker's fallback is a hard
// analyzer failure rather than a silent zero.
func TestUnclassifiedNodeFailsAnalysis(t *testing.T) {
	prog, diagnostics := compile([]Source{{
		Name:      "main.slk",
		Namespace: "root",
		Text:      "function main() -> int {\n    1\n}\n",
	}})
	requireNoDiagnostics(t, diagnostics)
	main := prog.functions["root.main"]
	main.ast.statements = append(main.ast.statements, &unclassifiedStatement{pos: main.pos})

	if _, _, err := prog.measureCallables(nil); err == nil || !strings.Contains(err.Error(), "unclassifiedStatement") {
		t.Fatalf("unclassified node produced err=%v", err)
	}
}

// astNodeTypes is every type in this package that implements statementNode or
// expressionNode.
func astNodeTypes(t *testing.T) map[string]struct{} {
	t.Helper()
	nodes := make(map[string]struct{})
	for _, file := range packageFiles(t) {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			if function.Name.Name != "statementPos" && function.Name.Name != "expressionPos" {
				continue
			}
			if name, ok := pointerTypeName(function.Recv.List[0].Type); ok {
				nodes[name] = struct{}{}
			}
		}
	}
	return nodes
}

// complexityWalkerCases is every node type the metric walker matches on.
func complexityWalkerCases(t *testing.T) map[string]struct{} {
	t.Helper()
	fileSet := gotoken.NewFileSet()
	parsed, err := goparser.ParseFile(fileSet, "complexity.go", nil, 0)
	if err != nil {
		t.Fatalf("parse complexity.go: %v", err)
	}
	cases := make(map[string]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		clause, isClause := node.(*ast.CaseClause)
		if !isClause {
			return true
		}
		for _, expression := range clause.List {
			if name, ok := pointerTypeName(expression); ok {
				cases[name] = struct{}{}
			}
		}
		return true
	})
	return cases
}

func packageFiles(t *testing.T) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list package sources: %v", err)
	}
	fileSet := gotoken.NewFileSet()
	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := goparser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		files = append(files, parsed)
	}
	return files
}

func pointerTypeName(expression ast.Expr) (string, bool) {
	pointer, isPointer := expression.(*ast.StarExpr)
	if !isPointer {
		return "", false
	}
	name, isName := pointer.X.(*ast.Ident)
	if !isName {
		return "", false
	}
	return name.Name, true
}
