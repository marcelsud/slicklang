// Command slickview serves a Slick project as a browsable, syntax-highlighted
// website. Highlighting comes from the compiler's own scanner, so it can never
// disagree with the language about what a token is.
//
//	go run ./cmd/slickview .
//	go run ./cmd/slickview -addr 127.0.0.1:9000 examples
//
// Flags come before the path: Go's flag package stops parsing at the first
// positional argument.
package main

import (
	"flag"
	"fmt"
	"html"
	"net/http"
	"os"
	"strings"

	"slick/internal/compiler"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "listen address")
	flag.Parse()
	root := "."
	// Go stops flag parsing at the first positional, so a trailing -addr would
	// otherwise be silently ignored.
	if flag.NArg() > 1 {
		fmt.Fprintf(os.Stderr, "slickview: unexpected argument %q; flags must come before the path\n", flag.Arg(1))
		os.Exit(2)
	}
	if flag.NArg() == 1 {
		root = flag.Arg(0)
	}
	// Fail before binding rather than serving an empty tree.
	if _, err := compiler.LoadSources(root); err != nil {
		fmt.Fprintf(os.Stderr, "slickview: %v\n", err)
		os.Exit(2)
	}

	server := &viewer{root: root}
	http.HandleFunc("/", server.page)
	http.HandleFunc("/source", server.source)
	fmt.Printf("slickview: http://%s (serving %s)\n", *address, root)
	if err := http.ListenAndServe(*address, nil); err != nil {
		fmt.Fprintf(os.Stderr, "slickview: %v\n", err)
		os.Exit(1)
	}
}

type viewer struct {
	root string
}

// Sources are re-read per request so an edit shows up on reload, and so the
// only files ever served are the ones the compiler itself discovers. A request
// path that is not in that set is rejected, which leaves no room for traversal.
func (v *viewer) load(w http.ResponseWriter) ([]compiler.Source, bool) {
	sources, err := compiler.LoadSources(v.root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	return sources, true
}

func (v *viewer) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	sources, ok := v.load(w)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A replacer, not Printf: the CSS below is full of percent signs.
	page := strings.NewReplacer(
		"{{root}}", html.EscapeString(v.root),
		"{{tree}}", fileTree(sources),
		"{{themes}}", themeOptions(),
	).Replace(pageTemplate)
	fmt.Fprint(w, page)
}

func (v *viewer) source(w http.ResponseWriter, r *http.Request) {
	sources, ok := v.load(w)
	if !ok {
		return
	}
	name := r.URL.Query().Get("path")
	for _, source := range sources {
		if source.Name != name {
			continue
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<h1>%s</h1>", html.EscapeString(source.Name))
		fmt.Fprintf(w, "<div class=\"code\"><pre class=\"gutter\">%s</pre><pre class=\"source\">%s</pre></div>",
			gutter(source.Text), highlight(source.Text))
		return
	}
	http.NotFound(w, r)
}

// highlight renders the compiler's classified tokens as spans. Every token's
// text is emitted verbatim, so the rendered source matches the file exactly.
func highlight(source string) string {
	var out strings.Builder
	for _, token := range compiler.Highlight(source) {
		escaped := html.EscapeString(token.Text)
		if token.Class == compiler.ClassPlain {
			out.WriteString(escaped)
			continue
		}
		fmt.Fprintf(&out, "<span class=%q>%s</span>", token.Class, escaped)
	}
	return out.String()
}

func gutter(source string) string {
	var out strings.Builder
	for line := 1; line <= strings.Count(source, "\n")+1; line++ {
		fmt.Fprintf(&out, "%d\n", line)
	}
	return out.String()
}

// treeNode is one entry in the sidebar hierarchy. A node with a non-empty file
// is a leaf; everything else is a directory.
type treeNode struct {
	name     string
	file     string
	children []*treeNode
}

// directory finds or appends the child directory called name, so repeated path
// prefixes collapse into a single branch.
func (n *treeNode) directory(name string) *treeNode {
	for _, child := range n.children {
		if child.file == "" && child.name == name {
			return child
		}
	}
	child := &treeNode{name: name}
	n.children = append(n.children, child)
	return child
}

// fileTree renders sources as a nested directory tree. <details> gives
// collapsing for free, so the page needs no tree-widget JavaScript, and nesting
// the elements is what produces the indentation.
func fileTree(sources []compiler.Source) string {
	root := &treeNode{}
	for _, source := range sources {
		node := root
		segments := strings.Split(source.Name, "/")
		for _, segment := range segments[:len(segments)-1] {
			node = node.directory(segment)
		}
		node.children = append(node.children, &treeNode{name: segments[len(segments)-1], file: source.Name})
	}
	var out strings.Builder
	renderTree(root, &out)
	return out.String()
}

// renderTree emits directories before files at each level. Sources arrive in
// name order, so each group is already alphabetical.
func renderTree(node *treeNode, out *strings.Builder) {
	for _, child := range node.children {
		if child.file != "" {
			continue
		}
		fmt.Fprintf(out, "<details open><summary>%s/</summary>", html.EscapeString(child.name))
		renderTree(child, out)
		out.WriteString("</details>")
	}
	for _, child := range node.children {
		if child.file == "" {
			continue
		}
		fmt.Fprintf(out, "<a href=\"#%s\" data-path=\"%s\">%s</a>",
			html.EscapeString(child.file), html.EscapeString(child.file), html.EscapeString(child.name))
	}
}

// themes are ordered; the first is the default. Each is only a set of CSS
// custom properties, so adding one means adding a row here and a matching
// [data-theme] block in the stylesheet.
var themes = []struct{ id, label string }{
	{"midnight", "Midnight"},
	{"paper", "Paper"},
	{"solar", "Solar"},
	{"neon", "Neon"},
}

func themeOptions() string {
	var out strings.Builder
	for _, theme := range themes {
		fmt.Fprintf(&out, "<option value=%q>%s</option>", theme.id, html.EscapeString(theme.label))
	}
	return out.String()
}

const pageTemplate = `<!doctype html>
<html lang="en" data-theme="midnight"><head><meta charset="utf-8">
<title>{{root}} &middot; slickview</title>
<script>
// Applied before first paint so switching themes never flashes the old one.
try { document.documentElement.dataset.theme = localStorage.getItem('slickview-theme') || 'midnight'; } catch (e) {}
</script>
<style>
[data-theme="midnight"] {
  color-scheme: dark;
  --bg: #12141a; --panel: #171a21; --line: #262b36; --text: #d7dae0; --dim: #6f7787; --hover: #1f2430; --active: #2b3346;
  --keyword: #c98fe0; --type: #6cc4d8; --ctor: #7fd88f; --const: #e5975c;
  --string: #d2c07a; --comment: #5d6675; --punct: #98a0b0;
}
[data-theme="paper"] {
  color-scheme: light;
  --bg: #ffffff; --panel: #f6f7f9; --line: #dfe3e8; --text: #1f2328; --dim: #6a737d; --hover: #eceff2; --active: #dce3ea;
  --keyword: #8250df; --type: #0550ae; --ctor: #1a7f37; --const: #953800;
  --string: #0a3069; --comment: #6a737d; --punct: #57606a;
}
[data-theme="solar"] {
  color-scheme: light;
  --bg: #fdf6e3; --panel: #f5eed8; --line: #e6dcc0; --text: #4a4335; --dim: #93a1a1; --hover: #eee7d0; --active: #e3dbbe;
  --keyword: #6c71c4; --type: #268bd2; --ctor: #859900; --const: #cb4b16;
  --string: #2aa198; --comment: #93a1a1; --punct: #657b83;
}
[data-theme="neon"] {
  color-scheme: dark;
  --bg: #000000; --panel: #0a0a0a; --line: #333333; --text: #f0f0f0; --dim: #888888; --hover: #1a1a1a; --active: #2a2a2a;
  --keyword: #ff6ac1; --type: #5af0ff; --ctor: #5aff7f; --const: #ffb86c;
  --string: #f1fa8c; --comment: #7a7a7a; --punct: #cccccc;
}
* { box-sizing: border-box; }
body { margin: 0; height: 100vh; display: flex; background: var(--bg); color: var(--text);
  font: 13px/1.6 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
nav { width: 260px; flex: none; overflow: auto; padding: 12px; background: var(--panel);
  border-right: 1px solid var(--line); }
nav h2 { margin: 0 0 10px; font-size: 11px; letter-spacing: .12em; text-transform: uppercase;
  color: var(--dim); overflow-wrap: anywhere; }
#theme { width: 100%; margin-bottom: 14px; padding: 4px 6px; font: inherit; border-radius: 4px;
  background: var(--bg); color: var(--text); border: 1px solid var(--line); }
summary { cursor: pointer; color: var(--dim); padding: 2px 6px; border-radius: 4px;
  overflow-wrap: anywhere; }
summary:hover { background: var(--hover); }
nav a { display: block; padding: 3px 6px; color: var(--text);
  text-decoration: none; border-radius: 4px; overflow-wrap: anywhere; }
nav a:hover { background: var(--hover); }
nav a.active { background: var(--active); font-weight: 600; }
/* Nesting the elements is the indentation; the rule is the hierarchy guide. */
details > details, details > a { margin-left: 7px; padding-left: 7px; border-left: 1px solid var(--line); }
main { flex: 1; overflow: auto; padding: 20px 24px; }
h1 { font-size: 14px; font-weight: 600; margin: 0 0 16px; }
.empty { color: var(--dim); }
.code { display: flex; background: var(--panel); border: 1px solid var(--line); border-radius: 6px; }
pre { margin: 0; padding: 14px 0; }
.gutter { padding-left: 14px; padding-right: 14px; color: var(--dim); text-align: right;
  user-select: none; border-right: 1px solid var(--line); }
.source { padding-left: 16px; padding-right: 16px; overflow-x: auto; flex: 1; }
.keyword { color: var(--keyword); }
.type { color: var(--type); }
.constructor { color: var(--ctor); }
.constant, .number { color: var(--const); }
.string, .template { color: var(--string); }
.comment { color: var(--comment); font-style: italic; }
.punct { color: var(--punct); }
</style></head>
<body>
<nav>
  <h2>{{root}}</h2>
  <select id="theme" aria-label="Theme">{{themes}}</select>
  {{tree}}
</nav>
<main id="view"><p class="empty">Select a file.</p></main>
<script>
const theme = document.getElementById('theme');
theme.value = document.documentElement.dataset.theme;
theme.addEventListener('change', () => {
  document.documentElement.dataset.theme = theme.value;
  try { localStorage.setItem('slickview-theme', theme.value); } catch (e) {}
});

const links = document.querySelectorAll('a[data-path]');
let wanted = null;
async function show(path) {
  const link = [...links].find(a => a.dataset.path === path);
  if (!link) return;
  links.forEach(a => a.classList.toggle('active', a === link));
  wanted = path;
  const response = await fetch('/source?path=' + encodeURIComponent(path));
  // A newer selection may have landed while this request was in flight.
  if (wanted !== path) return;
  document.getElementById('view').innerHTML = response.ok
    ? await response.text()
    : '<p class="empty">Could not load ' + path + '</p>';
}
links.forEach(a => a.addEventListener('click', event => {
  event.preventDefault();
  location.hash = a.dataset.path;
  show(a.dataset.path);
}));
addEventListener('hashchange', () => show(decodeURIComponent(location.hash.slice(1))));
show(decodeURIComponent(location.hash.slice(1)) || links[0]?.dataset.path);
</script>
</body></html>
`
