package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"slick/internal/compiler"
)

// TestFileTreeNestsDirectories pins both the hierarchy and the ordering:
// directories nest inside their parent and come before files at each level.
func TestFileTreeNestsDirectories(t *testing.T) {
	tree := fileTree([]compiler.Source{
		{Name: "app.slk"},
		{Name: "hello/main.slk"},
		{Name: "hello/models/cat.slk"},
		{Name: "hello/models/dog.slk"},
	})
	expected := `<details open><summary>hello/</summary>` +
		`<details open><summary>models/</summary>` +
		`<a href="#hello/models/cat.slk" data-path="hello/models/cat.slk">cat.slk</a>` +
		`<a href="#hello/models/dog.slk" data-path="hello/models/dog.slk">dog.slk</a>` +
		`</details>` +
		`<a href="#hello/main.slk" data-path="hello/main.slk">main.slk</a>` +
		`</details>` +
		`<a href="#app.slk" data-path="app.slk">app.slk</a>`
	if tree != expected {
		t.Fatalf("tree mismatch\n got: %s\nwant: %s", tree, expected)
	}
}

func newViewer() *viewer { return &viewer{root: "../../examples"} }

// TestSourceServesOnlyDiscoveredFiles pins the trust boundary: the handler
// answers for paths the compiler discovered and nothing else, so a crafted
// path cannot reach outside the project.
func TestSourceServesOnlyDiscoveredFiles(t *testing.T) {
	rejected := []string{
		"",
		"../../go.mod",
		"../cmd/slickview/main.go",
		"/etc/passwd",
		"result/../../../go.mod",
		"result/main.slk/",
	}
	for _, path := range rejected {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			newViewer().source(recorder, httptest.NewRequest(http.MethodGet, "/source?path="+path, nil))
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("path %q returned %d, expected 404", path, recorder.Code)
			}
		})
	}
}

func TestSourceRendersHighlightedFile(t *testing.T) {
	recorder := httptest.NewRecorder()
	newViewer().source(recorder, httptest.NewRequest(http.MethodGet, "/source?path=result/main.slk", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, found %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, fragment := range []string{
		`<h1>result/main.slk</h1>`,
		`<span class="keyword">function</span>`,
		`<span class="constructor">Ok</span>`,
		`<span class="comment">// Result&lt;T, E&gt;</span>`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("rendered source is missing %s", fragment)
		}
	}
}

func TestPageListsEveryDiscoveredFile(t *testing.T) {
	recorder := httptest.NewRecorder()
	newViewer().page(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, found %d", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "{{") {
		t.Fatal("page template has an unreplaced placeholder")
	}
	for _, fragment := range []string{
		`data-path="result/main.slk"`,
		`<details open><summary>hello/</summary><details open><summary>models/</summary>`,
		`<option value="midnight">`,
		`<option value="neon">`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("page is missing %s", fragment)
		}
	}
}
