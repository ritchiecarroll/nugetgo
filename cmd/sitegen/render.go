package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"io"
)

// page is the site's only template. html/template escapes every value by context, so text taken
// from mappings.txt cannot inject markup, script or unsafe URLs into the page.
var page = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src '{{.ScriptHash}}'; style-src '{{.StyleHash}}'; base-uri 'none'; form-action 'none'">
<title>nugetgo: Go module to NuGet package registry</title>
<style>{{.Style}}</style>
</head>
<body>
<main>
<h1>nugetgo</h1>
<p>nugetgo maps Go module paths to the NuGet packages that hold their <a href="https://github.com/ritchiecarroll/go2cs">go2cs</a> C# conversions, so a converted program can reference a dependency as a package instead of converting it again. go2cs does not read the registry yet; that support is planned. This page is generated from the registry's data file, <a href="v1/mappings.txt">v1/mappings.txt</a>, which is the single source of truth.</p>
<p>The NuGet package ID of a converted module is <code>nugetgo.</code> followed by its dotted module path, whoever publishes it, the module's own author included: <code>github.com/example/widget/v2</code> maps to <code>nugetgo.github.com.example.widget.v2</code>, or to a hash-shortened alternate when that ID cannot be used. A module conversion does not take the <code>go.</code> prefix, which go2cs uses for the converted Go standard library. A mapping's status, not its ID, says whether the conversion comes from the module's own org.</p>
{{if .Rows}}
<div class="controls" id="controls" hidden>
<input id="filter" type="search" placeholder="Filter mappings" aria-label="Filter mappings" autocomplete="off" spellcheck="false">
</div>
<p class="count">{{if eq (len .Rows) 1}}1 mapping{{else}}{{len .Rows}} mappings{{end}}</p>
<div class="scroll">
<table id="mappings">
<thead>
<tr>
<th scope="col"><button type="button" data-col="0">Module path</button></th>
<th scope="col"><button type="button" data-col="1">NuGet package</button></th>
<th scope="col"><button type="button" data-col="2">Status</button></th>
<th scope="col"><button type="button" data-col="3">Source repository</button></th>
<th scope="col"><button type="button" data-col="4">Registered</button></th>
<th scope="col"><button type="button" data-col="5">Contact</button></th>
</tr>
</thead>
<tbody>
{{range .Rows}}<tr class="row-{{.Status}}">
<td class="mono">{{.ModulePath}}</td>
<td class="mono"><a href="https://www.nuget.org/packages/{{.NuGetID}}">{{.NuGetID}}</a></td>
<td><span class="status">{{.Status}}</span></td>
<td class="mono"><a href="{{.SourceRepo}}">{{.SourceRepo}}</a></td>
<td>{{.Registered}}</td>
<td>{{.Contact}}</td>
</tr>
{{end}}</tbody>
</table>
</div>
<p class="empty" id="nomatch" hidden>No mappings match the filter.</p>
{{else}}
<p class="empty">No mappings are registered yet.</p>
{{end}}
<p class="foot">Data: CC0-1.0. To add or change a mapping, see <a href="https://github.com/ritchiecarroll/nugetgo/blob/main/CONTRIBUTING.md">CONTRIBUTING.md</a>.</p>
</main>
{{if .Rows}}<script>{{.Script}}</script>{{end}}
</body>
</html>
`))

const style = `
:root {
  color-scheme: light dark;
  --bg: #ffffff;
  --fg: #1f2328;
  --muted: #59636e;
  --border: #d1d9e0;
  --head: #f6f8fa;
  --link: #0969da;
  --canonical: #1a7f37;
  --community: #9a6700;
  --withdrawn: #82071e;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0d1117;
    --fg: #e6edf3;
    --muted: #9198a1;
    --border: #3d444d;
    --head: #151b23;
    --link: #4493f8;
    --canonical: #3fb950;
    --community: #d29922;
    --withdrawn: #f85149;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font: 16px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
}
main { max-width: 72rem; margin: 0 auto; padding: 2rem 1rem; }
h1 { margin: 0 0 1rem; font-size: 2rem; }
a { color: var(--link); }
.controls { margin: 1.5rem 0 0.5rem; }
#filter {
  width: 100%;
  max-width: 28rem;
  padding: 0.5rem 0.75rem;
  font: inherit;
  color: inherit;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
}
.count, .foot { color: var(--muted); font-size: 0.875rem; }
.scroll { overflow-x: auto; border: 1px solid var(--border); border-radius: 6px; }
table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
th, td { padding: 0.5rem 0.75rem; text-align: left; vertical-align: top; border-bottom: 1px solid var(--border); }
tbody tr:last-child td { border-bottom: 0; }
th { background: var(--head); white-space: nowrap; }
th button {
  font: inherit;
  font-weight: 600;
  color: inherit;
  background: none;
  border: 0;
  padding: 0;
  cursor: pointer;
}
th[aria-sort="ascending"] button::after { content: " \25B2"; }
th[aria-sort="descending"] button::after { content: " \25BC"; }
.mono { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; word-break: break-all; }
.status { font-weight: 600; }
.row-canonical .status { color: var(--canonical); }
.row-community .status { color: var(--community); }
.row-withdrawn .status { color: var(--withdrawn); }
.row-withdrawn td { color: var(--muted); }
.empty { padding: 1rem; border: 1px dashed var(--border); border-radius: 6px; color: var(--muted); }
`

const script = `
(function () {
  "use strict";
  var table = document.getElementById("mappings");
  if (!table) { return; }
  var tbody = table.tBodies[0];
  var rows = Array.prototype.slice.call(tbody.rows);
  var filter = document.getElementById("filter");
  var nomatch = document.getElementById("nomatch");
  document.getElementById("controls").hidden = false;

  filter.addEventListener("input", function () {
    var q = filter.value.trim().toLowerCase();
    var shown = 0;
    rows.forEach(function (r) {
      var match = q === "" || r.textContent.toLowerCase().indexOf(q) !== -1;
      r.hidden = !match;
      if (match) { shown++; }
    });
    nomatch.hidden = shown !== 0;
  });

  var headers = table.tHead.rows[0].cells;
  Array.prototype.forEach.call(table.tHead.querySelectorAll("button[data-col]"), function (button) {
    button.addEventListener("click", function () {
      var col = Number(button.getAttribute("data-col"));
      var th = button.parentNode;
      var ascending = th.getAttribute("aria-sort") !== "ascending";
      Array.prototype.forEach.call(headers, function (h) { h.removeAttribute("aria-sort"); });
      th.setAttribute("aria-sort", ascending ? "ascending" : "descending");
      rows.sort(function (x, y) {
        var c = x.cells[col].textContent.localeCompare(y.cells[col].textContent, undefined, { numeric: true, sensitivity: "base" });
        return ascending ? c : -c;
      });
      rows.forEach(function (r) { tbody.appendChild(r); });
    });
  });
})();
`

func cspHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// Render writes the site's index page for rows. An empty rows slice renders the empty state.
func Render(w io.Writer, rows []Row) error {
	var buf bytes.Buffer
	err := page.Execute(&buf, struct {
		Rows       []Row
		Style      template.CSS
		Script     template.JS
		StyleHash  string
		ScriptHash string
	}{
		Rows:       rows,
		Style:      template.CSS(style),
		Script:     template.JS(script),
		StyleHash:  cspHash(style),
		ScriptHash: cspHash(script),
	})
	if err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}
