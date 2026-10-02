package main

import (
	"bytes"
	"errors"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const header = "# module-path\tnuget-id\tstatus\tsource-repo\tregistered\tcontact\n"

const validRow = "github.com/example/hashset\tnugetgo.github.com.example.hashset\tcommunity\thttps://github.com/example/hashset-cs\t2026-09-30\texample\n"

func build(t *testing.T, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "mappings.txt")
	if err := os.WriteFile(in, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "_site")
	if _, err := Build(in, out); err != nil {
		t.Fatalf("Build: %v", err)
	}
	index, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(index), out
}

func TestRepositoryFileValidates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "v1", "mappings.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("v1/mappings.txt", data); err != nil {
		t.Fatalf("v1/mappings.txt does not validate:\n%v", err)
	}
}

func TestEmptyFileRendersEmptyState(t *testing.T) {
	for _, content := range []string{"", header} {
		index, _ := build(t, content)
		if !strings.Contains(index, "No mappings are registered yet.") {
			t.Errorf("content %q: empty state missing", content)
		}
		if strings.Contains(index, "<table") || strings.Contains(index, "<script") {
			t.Errorf("content %q: empty page has a table or a script", content)
		}
		if !strings.Contains(index, `href="v1/mappings.txt"`) {
			t.Errorf("content %q: relative link to the raw file missing", content)
		}
	}
}

func TestValidRowRenders(t *testing.T) {
	index, _ := build(t, header+validRow)
	for _, want := range []string{
		"<table id=\"mappings\">",
		"<td class=\"mono\">github.com/example/hashset</td>",
		`<a href="https://www.nuget.org/packages/nugetgo.github.com.example.hashset">nugetgo.github.com.example.hashset</a>`,
		`<a href="https://github.com/example/hashset-cs">https://github.com/example/hashset-cs</a>`,
		`<tr class="row-community">`,
		"<td>2026-09-30</td>",
		"<td>example</td>",
		"1 mapping<",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("rendered page lacks %q", want)
		}
	}
	if strings.Contains(index, "No mappings are registered yet.") {
		t.Error("empty state shown with a row present")
	}
}

// TestHostileRowIsEscaped renders hostile text in every field, bypassing validation, so the
// template's own escaping is what is under test.
func TestHostileRowIsEscaped(t *testing.T) {
	const (
		script = `<script>alert("x")</script>`
		quotes = `"onmouseover="alert(1)`
		jsURL  = `javascript:alert(document.cookie)`
		tag    = `<img src=x onerror=alert(1)>`
	)
	rows := []Row{{
		Line:       1,
		ModulePath: script,
		NuGetID:    `"><script>alert(2)</script>`,
		Status:     `canonical" onclick="alert(3)`,
		SourceRepo: jsURL,
		Registered: tag,
		Contact:    quotes + `'` + script,
	}}
	var buf bytes.Buffer
	if err := Render(&buf, rows); err != nil {
		t.Fatal(err)
	}
	page := buf.String()

	for _, raw := range []string{
		script,
		quotes,
		tag,
		`<script>alert(2)</script>`,
		`onclick="alert(3)`,
		`href="javascript:`,
	} {
		if strings.Contains(page, raw) {
			t.Errorf("hostile payload %q appears unescaped", raw)
		}
	}
	if n := strings.Count(page, "<script"); n != 1 {
		t.Errorf("page has %d <script elements, want exactly the one inline script", n)
	}
	if !strings.Contains(page, `href="#ZgotmplZ"`) {
		t.Error("javascript: source-repo was not neutralised in the href")
	}
	if !strings.Contains(page, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;") {
		t.Error("escaped script text missing from the module-path cell")
	}

	// The same hostile text through the whole pipeline: the contact field is free text, so it
	// passes validation and must still render escaped.
	row := "github.com/example/hashset\tnugetgo.github.com.example.hashset\tcommunity\thttps://github.com/example/hashset-cs\t2026-09-30\t" + quotes + script + tag + "\n"
	index, _ := build(t, header+row)
	for _, raw := range []string{script, quotes, tag} {
		if strings.Contains(index, raw) {
			t.Errorf("pipeline: hostile payload %q appears unescaped", raw)
		}
	}
}

func TestMalformedRowsFailWithLineNumbers(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"too few fields", header + "github.com/a/b\tnugetgo.github.com.a.b\tcommunity\n", []string{"m.txt:2:", "3 TAB-separated fields"}},
		{"too many fields", header + strings.TrimSuffix(validRow, "\n") + "\textra\n", []string{"m.txt:2:", "7 TAB-separated fields"}},
		{"spaces not tabs", header + strings.Replace(validRow, "\t", "    ", 1), []string{"m.txt:2:", "5 TAB-separated fields"}},
		{"bad status", header + strings.Replace(validRow, "community", "official", 1), []string{"m.txt:2:", `status "official"`}},
		{"empty field", header + "\n" + strings.Replace(validRow, "\texample\n", "\t\n", 1), []string{"m.txt:3:", "field contact is empty"}},
		{"padded field", header + strings.Replace(validRow, "\texample", "\texample ", 1), []string{"m.txt:2:", "field contact has leading or trailing whitespace"}},
		{"not https", header + strings.Replace(validRow, "https://", "javascript://", 1), []string{"m.txt:2:", "not an https URL"}},
		{"bad date", header + strings.Replace(validRow, "2026-09-30", "30/09/2026", 1), []string{"m.txt:2:", "YYYY-MM-DD"}},
		{"bad nuget id", header + strings.Replace(validRow, "nugetgo.github.com", "nugetgo..github.com", 1), []string{"m.txt:2:", "not a valid NuGet package ID"}},
		{"bad module path", header + strings.Replace(validRow, "github.com/example/hashset\t", "example/hashset\t", 1), []string{"m.txt:2:", "not a host name"}},
		{"duplicate module", header + validRow + validRow, []string{"m.txt:3:", "already has a row at line 2"}},
		{"unsorted", header + validRow + strings.Replace(validRow, "github.com/example/hashset\t", "github.com/aaa/hashset\t", 1), []string{"m.txt:3:", "keep rows sorted"}},
		{"crlf", header + strings.Replace(validRow, "\n", "\r\n", 1), []string{"m.txt:2:", "carriage return"}},
		{"whitespace line", header + " \t \n", []string{"m.txt:2:", "only whitespace"}},
		{"bom", "\xef\xbb\xbf" + header, []string{"m.txt:1:", "byte order mark"}},
		{"invalid utf-8", header + "\xff\n", []string{"m.txt:2:", "not valid UTF-8"}},
		{"duplicate nuget id", header + validRow + strings.Replace(strings.Replace(validRow, "github.com/example/hashset\t", "github.com/example/other\t", 1), "nugetgo.github.com", "NuGetGo.GitHub.com", 1), []string{"m.txt:3:", "already used at line 2", "case-insensitive"}},
		{"bidi override", header + strings.Replace(validRow, "\texample\n", "\texa‮mple\n", 1), []string{"m.txt:2:", "field contact contains a control or invisible character U+202E"}},
		{"escape sequence", header + strings.Replace(validRow, "\texample\n", "\t\x1b[31mexample\a\n", 1), []string{"m.txt:2:", "control or invisible character U+001B"}},
		{"zero-width space", header + strings.Replace(validRow, "\texample\n", "\texa​mple\n", 1), []string{"m.txt:2:", "U+200B"}},
		{"homograph host", header + strings.Replace(validRow, "https://github.com/", "https://еxample.com/", 1), []string{"m.txt:2:", "non-ASCII"}},
		{"v1 suffix", header + strings.Replace(validRow, "github.com/example/hashset\t", "github.com/example/hashset/v1\t", 1), []string{"m.txt:2:", "/v0 or /v1"}},
		{"v0 suffix", header + strings.Replace(validRow, "github.com/example/hashset\t", "github.com/example/hashset/v0\t", 1), []string{"m.txt:2:", "/v0 or /v1"}},
		{"future date", header + strings.Replace(validRow, "2026-09-30", "2099-12-31", 1), []string{"m.txt:2:", "in the future"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse("m.txt", []byte(c.content))
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("got %v, want a *ValidationError", err)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestAcceptedEdgeCases(t *testing.T) {
	for name, row := range map[string]string{
		"v2 suffix":        strings.Replace(validRow, "github.com/example/hashset\t", "github.com/example/hashset/v2\t", 1),
		"gopkg.in v1":      strings.Replace(validRow, "github.com/example/hashset\t", "gopkg.in/yaml/v1\t", 1),
		"unicode contact":  strings.Replace(validRow, "\texample\n", "\tJosé Müller 中\n", 1),
		"punycode host":    strings.Replace(validRow, "https://github.com/", "https://xn--xample-2of.com/", 1),
		"tomorrow is fine": strings.Replace(validRow, "2026-09-30", time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"), 1),
	} {
		if _, err := Parse("m.txt", []byte(header+row)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// rowWith is validRow with its NuGet ID and its status replaced.
func rowWith(id, status string) string {
	row := strings.Replace(validRow, "\tnugetgo.github.com.example.hashset\t", "\t"+id+"\t", 1)
	return strings.Replace(row, "\tcommunity\t", "\t"+status+"\t", 1)
}

// TestNuGetIDGoPrefixRefused pins the one prefix rule the lint enforces: a NuGet ID that starts
// with go., the prefix of the converted Go standard library, is refused by name in any letter case
// and whatever the row's status. Every other valid NuGet ID is accepted, with or without the
// nugetgo. prefix. A row yields one problem: an ID that is malformed or too long is refused for
// that alone, even when it starts with go.
func TestNuGetIDGoPrefixRefused(t *testing.T) {
	goPrefix := func(id string) string {
		return `nuget-id "` + id + `" uses the "go." prefix, which is the converted Go standard library; the ID of a converted module starts with "nugetgo."`
	}
	invalid := func(id string) string {
		return `nuget-id "` + id + `" is not a valid NuGet package ID`
	}
	tooLong := func(string) string {
		return "nuget-id is 101 characters, NuGet allows at most 100"
	}
	longest := "go." + strings.Repeat("a", maxNuGetIDLength-len("go."))
	cases := []struct {
		name string
		id   string
		want func(id string) string // the row's one problem; nil when the ID is accepted
	}{
		{"module path under nugetgo", "nugetgo.github.com.example.hashset", nil},
		{"nugetgo in another letter case", "NuGetGo.github.com.example.hashset", nil},
		{"hash-shortened alternate", "nugetgo.github.com.example.hashset.22485230", nil},
		{"another prefix", "Example.HashSet", nil},
		{"nugetgo without its dot", "nugetgo-x.y", nil},
		{"starts with go but not with go.", "gopher.hashset", nil},
		{"golang prefix", "golang.x", nil},
		{"go and a hyphen", "go-x.y", nil},
		{"the bare id go", "go", nil},
		{"go. after the start", "example.go.hashset", nil},
		{"module path under go", "go.github.com.example.hashset", goPrefix},
		{"standard library id", "go.net.http", goPrefix},
		{"go capitalised", "Go.X", goPrefix},
		{"go in upper case", "GO.x", goPrefix},
		{"go in mixed case", "gO.GitHub.com.example.hashset", goPrefix},
		{"go. at the longest length", longest, goPrefix},
		{"go. past the longest length", longest + "a", tooLong},
		{"go. and nothing else", "go.", invalid},
		{"go. and an empty segment", "go..x", invalid},
		{"go. capitalised and a hyphen", "Go.-x", invalid},
	}
	for _, status := range []string{"canonical", "community", "withdrawn"} {
		for _, c := range cases {
			t.Run(status+"/"+c.name, func(t *testing.T) {
				rows, err := Parse("m.txt", []byte(header+rowWith(c.id, status)))
				if c.want == nil {
					if err != nil {
						t.Fatalf("nuget-id %q refused: %v", c.id, err)
					}
					if len(rows) != 1 || rows[0].NuGetID != c.id || rows[0].Status != status {
						t.Fatalf("got rows %+v, want one %s row with nuget-id %q", rows, status, c.id)
					}
					return
				}
				var verr *ValidationError
				if !errors.As(err, &verr) {
					t.Fatalf("nuget-id %q accepted: got %v, want a *ValidationError", c.id, err)
				}
				if len(verr.Problems) != 1 {
					t.Fatalf("got %d problems, want exactly one: %v", len(verr.Problems), err)
				}
				if want := "m.txt:2: " + c.want(c.id); verr.Problems[0] != want {
					t.Errorf("got  %q\nwant %q", verr.Problems[0], want)
				}
			})
		}
	}
}

func TestInvalidFileWritesNothing(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "mappings.txt")
	if err := os.WriteFile(in, []byte(header+"only\tthree\tfields\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "_site")
	if _, err := Build(in, out); err == nil {
		t.Fatal("Build succeeded on an invalid file")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output directory exists after a failed build: %v", err)
	}
}

func TestRawCopyIsByteIdentical(t *testing.T) {
	content := header + "# a comment with unicode: é中\n\n" + validRow
	_, out := build(t, content)
	got, err := os.ReadFile(filepath.Join(out, "v1", "mappings.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(content)) {
		t.Errorf("raw copy differs from the input:\ngot  %q\nwant %q", got, content)
	}
	if info, err := os.Stat(filepath.Join(out, ".nojekyll")); err != nil || info.Size() != 0 {
		t.Errorf(".nojekyll missing or not empty: %v", err)
	}
}

// TestContentSecurityPolicyMatches checks that the page's CSP hashes admit exactly the inline
// script and style it carries, so the policy neither blocks the page nor admits anything else.
func TestContentSecurityPolicyMatches(t *testing.T) {
	index, _ := build(t, header+validRow)
	csp := regexp.MustCompile(`http-equiv="Content-Security-Policy" content="([^"]*)"`).FindStringSubmatch(index)
	if csp == nil {
		t.Fatal("no Content-Security-Policy meta element")
	}
	policy := html.UnescapeString(csp[1])
	inline := func(tag string) string {
		m := regexp.MustCompile(`(?s)<` + tag + `>(.*?)</` + tag + `>`).FindStringSubmatch(index)
		if m == nil {
			t.Fatalf("no inline <%s>", tag)
		}
		return m[1]
	}
	for _, want := range []string{
		"default-src 'none'",
		"script-src '" + cspHash(inline("script")) + "'",
		"style-src '" + cspHash(inline("style")) + "'",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy %q lacks %q", policy, want)
		}
	}
	if strings.Contains(index, "http://") || regexp.MustCompile(`(src|href)="https?://[^"]*\.(js|css)`).MatchString(index) {
		t.Error("page references an external resource")
	}
	if strings.Contains(script, "innerHTML") || strings.Contains(script, "outerHTML") || strings.Contains(script, "insertAdjacentHTML") {
		t.Error("inline script writes HTML")
	}
}
