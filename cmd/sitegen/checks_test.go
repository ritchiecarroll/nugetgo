package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The checks' tests never touch the network: every request goes to an httptest server that serves
// fixtures shaped like the Go module proxy's and NuGet's v3 answers, through a client that refuses any
// host but the loopback address.

const (
	testModule = "github.com/example/hashset"
	testID     = "nugetgo.github.com.example.hashset"
	testRepo   = "https://github.com/example/hashset-cs"
)

// goodSelfDescription is a v1 self-description as go2cs's pack writes it, for testModule v1.0.0.
const goodSelfDescription = selfDescriptionMagic + "\n# Written by go2cs (internal/gensourcemeta) when a converted Go module is packed; do not edit.\n" +
	"module " + testModule + "\nmodule-version v1.0.0\ngo2cs-release 1.24.13.4\n" +
	"package " + testModule + " hashset\n\n##github.com.example.hashset\n"

type fakeModule struct {
	tags     []string // the @v/list answer
	versions []string // every version .info answers for, tags and pseudo-versions
}

type fakeVersion struct {
	version     string
	unlisted    bool
	description string
	repository  string
	selfDesc    string // "" packs no self-description
}

// fake is the proxy and NuGet. Packages are keyed by lowercase ID.
type fake struct {
	modules  map[string]fakeModule
	packages map[string][]fakeVersion
	paged    bool // serve registration leaves on a separate page, as NuGet does for large packages
}

func newFake() *fake {
	return &fake{
		modules: map[string]fakeModule{testModule: {tags: []string{"v1.0.0"}, versions: []string{"v1.0.0"}}},
		packages: map[string][]fakeVersion{testID: {{
			version:     "1.0.0",
			description: "PROOF: go2cs C# conversion of " + testModule + " v1.0.0, published by its author, ...",
			repository:  testRepo,
			selfDesc:    goodSelfDescription,
		}}},
	}
}

func (f *fake) module(escaped string) (fakeModule, bool) {
	for path, m := range f.modules {
		if escapeModulePath(path) == escaped {
			return m, true
		}
	}
	return fakeModule{}, false
}

func (f *fake) version(id, version string) (fakeVersion, bool) {
	for _, v := range f.packages[id] {
		if strings.ToLower(v.version) == version {
			return v, true
		}
	}
	return fakeVersion{}, false
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/proxy/"):
		rest := strings.TrimPrefix(p, "/proxy/")
		at := strings.Index(rest, "/@")
		if at < 0 {
			break
		}
		m, ok := f.module(rest[:at])
		if !ok {
			break
		}
		query := rest[at+1:]
		switch {
		case query == "@latest" && len(m.versions) > 0:
			fmt.Fprintf(w, `{"Version":%q}`, m.versions[len(m.versions)-1])
			return
		case query == "@v/list":
			for _, t := range m.tags {
				fmt.Fprintln(w, t)
			}
			return
		case strings.HasPrefix(query, "@v/") && strings.HasSuffix(query, ".info"):
			want := strings.TrimSuffix(strings.TrimPrefix(query, "@v/"), ".info")
			for _, v := range m.versions {
				if escapeModulePath(v) == want {
					fmt.Fprintf(w, `{"Version":%q}`, v)
					return
				}
			}
		}
	case strings.HasPrefix(p, "/reg/"):
		parts := strings.Split(strings.TrimPrefix(p, "/reg/"), "/")
		versions, ok := f.packages[parts[0]]
		if !ok {
			break
		}
		var leaves []interface{}
		for _, v := range versions {
			leaves = append(leaves, map[string]interface{}{"catalogEntry": map[string]interface{}{
				"id": parts[0], "version": v.version, "listed": !v.unlisted, "description": v.description,
			}})
		}
		pageID := "http://" + r.Host + "/reg/" + parts[0] + "/page/1.json"
		var body interface{}
		switch {
		case len(parts) == 2 && parts[1] == "index.json" && f.paged:
			body = map[string]interface{}{"items": []interface{}{map[string]interface{}{"@id": pageID}}}
		case len(parts) == 2 && parts[1] == "index.json":
			body = map[string]interface{}{"items": []interface{}{map[string]interface{}{"@id": pageID, "items": leaves}}}
		case strings.Join(parts[1:], "/") == "page/1.json" && f.paged:
			body = map[string]interface{}{"@id": pageID, "items": leaves}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(body)
		return
	case strings.HasPrefix(p, "/flat/"):
		parts := strings.Split(strings.TrimPrefix(p, "/flat/"), "/")
		if len(parts) != 3 {
			break
		}
		v, ok := f.version(parts[0], parts[1])
		if !ok {
			break
		}
		nuspec := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd"><metadata><id>%s</id><version>%s</version>`, parts[0], v.version)
		if v.repository != "" {
			nuspec += fmt.Sprintf(`<repository type="git" url="%s" />`, v.repository)
		}
		nuspec += `</metadata></package>`
		switch parts[2] {
		case parts[0] + ".nuspec":
			fmt.Fprint(w, nuspec)
			return
		case parts[0] + "." + parts[1] + ".nupkg":
			var buf bytes.Buffer
			z := zip.NewWriter(&buf)
			entries := [][2]string{{parts[0] + ".nuspec", nuspec}, {"lib/net10.0/hashset.dll", "MZ"}}
			if v.selfDesc != "" {
				entries = append(entries, [2]string{selfDescriptionPath, v.selfDesc})
			}
			for _, e := range entries {
				fw, err := z.Create(e[0])
				if err == nil {
					_, err = fw.Write([]byte(e[1]))
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			if err := z.Close(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Write(buf.Bytes())
			return
		}
	}
	http.NotFound(w, r)
}

// loopbackOnly refuses every request to a host other than a loopback address.
type loopbackOnly struct{}

func (loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if ip := net.ParseIP(r.URL.Hostname()); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("test tried to reach %s; the checks' tests never touch the network", r.URL.Host)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func serve(t *testing.T, f *fake) *Remote {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Remote{
		Client:       &http.Client{Transport: loopbackOnly{}},
		Proxy:        srv.URL + "/proxy",
		Registration: srv.URL + "/reg",
		Flat:         srv.URL + "/flat",
	}
}

var testReleases = parseReleases("# test list\n1.24.13.3\n1.24.13.4\n")

func testRow(status string) Row {
	return Row{Line: 30, ModulePath: testModule, NuGetID: testID, Status: status, SourceRepo: testRepo, Registered: "2026-10-09", Contact: "example"}
}

// findings returns the row's findings for one check, as "level: message" lines.
func findings(r RowReport, check string) []string {
	var out []string
	for _, f := range r.Findings {
		if f.Check == check {
			out = append(out, f.Level+": "+f.Message)
		}
	}
	return out
}

// wantFinding fails the test unless the report has a finding for check at level whose message
// contains every one of want.
func wantFinding(t *testing.T, r RowReport, check, level string, want ...string) {
	t.Helper()
	for _, f := range r.Findings {
		if f.Check != check || f.Level != level {
			continue
		}
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(f.Message, w)
		}
		if ok {
			return
		}
	}
	t.Errorf("no %s %s finding containing %q; findings: %+v", check, level, want, r.Findings)
}

func TestNetworkGuardRefusesRealHosts(t *testing.T) {
	remote := DefaultRemote()
	remote.Client = &http.Client{Transport: loopbackOnly{}}
	if err := remote.ModuleLatest(testModule); err == nil || !strings.Contains(err.Error(), "never touch the network") {
		t.Fatalf("the guard let a request to the real proxy through: %v", err)
	}
}

func TestGoodRowPassesEveryCheck(t *testing.T) {
	for _, paged := range []bool{false, true} {
		f := newFake()
		f.paged = paged
		r := CheckRow(serve(t, f), testReleases, testRow("canonical"))
		if r.Failed() || !r.Canonical {
			t.Fatalf("paged=%t: good row failed or not canonical: %+v", paged, r.Findings)
		}
		for check, want := range map[string]int{checkExistence: 2, checkProvenance: 1, checkModule: 1, checkRelease: 1} {
			got := findings(r, check)
			if len(got) != want {
				t.Errorf("paged=%t: %s: got %q, want %d notice(s)", paged, check, got, want)
			}
			for _, g := range got {
				if !strings.HasPrefix(g, levelNotice) {
					t.Errorf("paged=%t: %s: got %q, want notices", paged, check, g)
				}
			}
		}
		if got := findings(r, checkPattern); len(got) != 0 {
			t.Errorf("paged=%t: natural ID drew a pattern warning: %q", paged, got)
		}
	}
}

// Check 1's ID pattern: a warning, never a failure.

func TestPatternWarningRed(t *testing.T) {
	f := newFake()
	f.packages["example.hashset"] = f.packages[testID]
	row := testRow("canonical")
	row.NuGetID = "Example.HashSet"
	r := CheckRow(serve(t, f), testReleases, row)
	wantFinding(t, r, checkPattern, levelWarning, `nuget-id "Example.HashSet" does not follow the ID pattern`, `"`+testID+`"`, `"`+testID+`.b2920a17"`)
	if r.Failed() {
		t.Errorf("a pattern warning failed the row: %+v", r.Findings)
	}
	var out bytes.Buffer
	WriteAnnotations(&out, "v1/mappings.txt", []RowReport{r})
	if !strings.Contains(out.String(), "::warning file=v1/mappings.txt,line=30,title=check 1%3A ID pattern::"+testModule+": nuget-id") {
		t.Errorf("no ::warning annotation on the row's line:\n%s", out.String())
	}
}

// TestPatternWarningGreen pins the Go port of go2cs's ID minting against Get-NugetgoPackageId's own
// answers (NugetgoIdentity.psm1, run with the natural ID passed in -ExistingIds to force the alternate).
func TestPatternWarningGreen(t *testing.T) {
	long := "github.com/example-organisation-with-a-long-name/a-module-whose-repository-name-is-also-quite-long/v2"
	for _, c := range []struct{ module, id string }{
		{testModule, testID},
		{testModule, "NuGetGo.GitHub.com.Example.HashSet"},
		{testModule, "nugetgo.github.com.example.hashset.b2920a17"},
		{"github.com/example/my~mod", "nugetgo.github.com.example.my-mod.c793e41b"},
		{long, "nugetgo.github.com.example-organisation-with-a-long-name.a-module-whose-repository-name-is.d5c45612"},
		{"gopkg.in/yaml.v3", "nugetgo.gopkg.in.yaml.v3.9852593e"},
		{"gopkg.in/yaml.v3", "nugetgo.gopkg.in.yaml.v3"},
	} {
		if msg := patternWarning(Row{ModulePath: c.module, NuGetID: c.id}); msg != "" {
			t.Errorf("%s -> %s: %s", c.module, c.id, msg)
		}
	}
}

// Check 2: existence.

func TestExistenceRed(t *testing.T) {
	pseudo := "v0.0.0-20251001235044-fca9a0999f15"
	prereleaseOnly := func(description string, tags []string) *fake {
		f := newFake()
		f.modules[testModule] = fakeModule{tags: tags, versions: append(append([]string{}, tags...), pseudo)}
		f.packages[testID] = []fakeVersion{{version: "0.0.0-20251001235044-fca9a0999f15", description: description, repository: testRepo, selfDesc: goodSelfDescription}}
		return f
	}
	proof := "PROOF: unofficial go2cs C# conversion of " + testModule + " " + pseudo + ", built on the Go 1.24.13 standard library, ..."
	cases := []struct {
		name string
		f    func() *fake
		want []string
	}{
		{"module missing at the proxy", func() *fake { f := newFake(); delete(f.modules, testModule); return f }, []string{"does not resolve at the Go module proxy"}},
		{"package missing at NuGet", func() *fake { f := newFake(); delete(f.packages, testID); return f }, []string{"package " + testID + " does not resolve at NuGet's v3 API: not found"}},
		{"registration lists a malformed version", func() *fake { f := newFake(); f.packages[testID][0].version = "1.0.0/../x"; return f }, []string{`lists the version "1.0.0/../x", which is not a NuGet version`}},
		{"only unlisted versions", func() *fake { f := newFake(); f.packages[testID][0].unlisted = true; return f }, []string{"has no listed version"}},
		{"prerelease only, module has a release", func() *fake { return prereleaseOnly(proof, []string{"v1.0.0"}) }, []string{"has the release v1.0.0"}},
		{"prerelease only, no PROOF text", func() *fake { return prereleaseOnly("A HashSet for C#.", nil) }, []string{"does not open with the PROOF text"}},
		{"prerelease only, PROOF names another module", func() *fake {
			return prereleaseOnly(strings.Replace(proof, testModule, "github.com/other/hashset", 1), nil)
		}, []string{"the PROOF text names module github.com/other/hashset"}},
		{"prerelease only, PROOF version not at the proxy", func() *fake {
			return prereleaseOnly(strings.Replace(proof, pseudo, "v0.0.0-20990101000000-000000000000", 1), nil)
		}, []string{"does not resolve at the Go module proxy"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := CheckRow(serve(t, c.f()), testReleases, testRow("canonical"))
			if !r.Failed() {
				t.Fatalf("check 2 passed: %+v", r.Findings)
			}
			wantFinding(t, r, checkExistence, levelError, c.want...)
			if got := findings(r, checkProvenance); len(got) != 0 {
				t.Errorf("check 3 ran after check 2 failed: %q", got)
			}
		})
	}
}

func TestExistenceGreen(t *testing.T) {
	pseudo := "v0.0.0-20251001235044-fca9a0999f15"
	for name, description := range map[string]string{
		"third-party PROOF": "PROOF: unofficial go2cs C# conversion of " + testModule + " " + pseudo + ", built on the Go 1.24.13 standard library, ...",
		"author's PROOF":    "PROOF: go2cs C# conversion of " + testModule + " " + pseudo + ", published by its author, ...",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			f.modules[testModule] = fakeModule{versions: []string{pseudo}}
			f.packages[testID] = []fakeVersion{{version: "0.0.0-20251001235044-fca9a0999f15", description: description, repository: testRepo,
				selfDesc: strings.Replace(goodSelfDescription, "v1.0.0", pseudo, 1)}}
			r := CheckRow(serve(t, f), testReleases, testRow("canonical"))
			if r.Failed() {
				t.Fatalf("prerelease-only row failed: %+v", r.Findings)
			}
			wantFinding(t, r, checkExistence, levelNotice, "only prerelease or pseudo-versions", "carries the PROOF text for "+pseudo)
		})
	}
	t.Run("a release beside prereleases", func(t *testing.T) {
		f := newFake()
		f.packages[testID] = append(f.packages[testID], fakeVersion{version: "1.1.0-rc.1", repository: "https://github.com/other/x"})
		r := CheckRow(serve(t, f), testReleases, testRow("canonical"))
		if r.Failed() {
			t.Fatalf("failed: %+v", r.Findings)
		}
		wantFinding(t, r, checkExistence, levelNotice, "package "+testID+" resolves at NuGet, latest release 1.0.0")
	})
}

// Check 3: provenance.

func TestProvenanceRed(t *testing.T) {
	for name, c := range map[string]struct {
		module, repository string
		want               string
	}{
		"repository under another org": {testModule, "https://github.com/someone-else/hashset-cs", "is not under github.com/example, the module's org"},
		"no RepositoryUrl":             {testModule, "", "has no RepositoryUrl"},
		"http RepositoryUrl":           {testModule, "http://github.com/example/hashset-cs", "is not under github.com/example"},
		"another host, same org name":  {testModule, "https://gitlab.com/example/hashset-cs", "is not under github.com/example"},
		"vanity module path":           {"example.com/hashset", "https://github.com/example/hashset-cs", "names no org on github.com, gitlab.com or bitbucket.org"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			f.modules[c.module] = f.modules[testModule]
			f.packages[testID][0].repository = c.repository
			row := testRow("canonical")
			row.ModulePath = c.module
			r := CheckRow(serve(t, f), testReleases, row)
			if !r.Failed() || r.Canonical {
				t.Fatalf("contradicted canonical claim passed: %+v", r.Findings)
			}
			wantFinding(t, r, checkProvenance, levelError, "the claim canonical is contradicted", c.want)
			if got := findings(r, checkModule); len(got) != 0 {
				t.Errorf("check 4 ran after check 3 failed: %q", got)
			}
		})
	}
}

func TestProvenanceGreen(t *testing.T) {
	t.Run("canonical confirmed, org compared case-insensitively", func(t *testing.T) {
		f := newFake()
		f.packages[testID][0].repository = "https://GitHub.com/Example/hashset-cs.git"
		r := CheckRow(serve(t, f), testReleases, testRow("canonical"))
		if r.Failed() || !r.Canonical {
			t.Fatalf("canonical row not confirmed: %+v", r.Findings)
		}
		wantFinding(t, r, checkProvenance, levelNotice, "canonical confirmed", "is under github.com/example")
	})
	t.Run("community contradicted is reported, not failed", func(t *testing.T) {
		f := newFake()
		f.packages[testID][0].repository = "https://github.com/someone-else/hashset-cs"
		r := CheckRow(serve(t, f), testReleases, testRow("community"))
		if r.Failed() || r.Canonical {
			t.Fatalf("community row failed or confirmed: %+v", r.Findings)
		}
		wantFinding(t, r, checkProvenance, levelNotice, "community, as claimed", "someone-else")
		wantFinding(t, r, checkRelease, levelNotice, "1.24.13.4")
	})
	t.Run("community row whose package is canonical", func(t *testing.T) {
		r := CheckRow(serve(t, newFake()), testReleases, testRow("community"))
		if r.Failed() || !r.Canonical {
			t.Fatalf("got %+v", r.Findings)
		}
		wantFinding(t, r, checkProvenance, levelNotice, "the row claims community, and its package is canonical by the rule")
	})
}

// Check 4a: the self-description names the module and a real module version.

func TestSurfaceModuleRed(t *testing.T) {
	for name, c := range map[string]struct{ selfDesc, want string }{
		"no self-description":   {"", "has no " + selfDescriptionPath},
		"another module":        {strings.Replace(goodSelfDescription, testModule, "github.com/example/other", -1), "describes module github.com/example/other, not " + testModule},
		"version not at proxy":  {strings.Replace(goodSelfDescription, "v1.0.0", "v1.0.1", 1), "names module version " + testModule + " v1.0.1, which does not resolve"},
		"not a Go version":      {strings.Replace(goodSelfDescription, "v1.0.0", "1.0.0", 1), "is not a Go module version"},
		"no magic line":         {strings.TrimPrefix(goodSelfDescription, selfDescriptionMagic+"\n"), "not a go2cs v1 self-description"},
		"carriage returns":      {strings.Replace(goodSelfDescription, "\n", "\r\n", -1), "a carriage return"},
		"unknown key":           {strings.Replace(goodSelfDescription, "go2cs-release", "go2cs-version", 1), `unknown key "go2cs-version"`},
		"package outside":       {strings.Replace(goodSelfDescription, "package "+testModule, "package github.com/example/other", 1), "is outside module"},
		"require with go. id":   {strings.Replace(goodSelfDescription, "package ", "require github.com/a/b v1.0.0 go.github.com.a.b\npackage ", 1), `uses the "go." prefix`},
		"second module-version": {strings.Replace(goodSelfDescription, "go2cs-release", "module-version v1.0.0\ngo2cs-release", 1), "a second module-version line"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			f.packages[testID][0].selfDesc = c.selfDesc
			r := CheckRow(serve(t, f), testReleases, testRow("canonical"))
			if !r.Failed() {
				t.Fatalf("check 4a passed: %+v", r.Findings)
			}
			wantFinding(t, r, checkModule, levelError, c.want)
		})
	}
}

func TestSurfaceModuleGreen(t *testing.T) {
	f := newFake()
	f.modules[testModule] = fakeModule{tags: []string{"v1.0.0", "v1.1.0"}, versions: []string{"v1.0.0", "v1.1.0"}}
	f.packages[testID] = append(f.packages[testID], fakeVersion{version: "1.1.0", repository: testRepo,
		selfDesc: strings.Replace(goodSelfDescription, "v1.0.0", "v1.1.0", 1)})
	r := CheckRow(serve(t, f), testReleases, testRow("canonical"))
	if r.Failed() {
		t.Fatalf("failed: %+v", r.Findings)
	}
	wantFinding(t, r, checkModule, levelNotice, testID+" 1.1.0 describes "+testModule+" v1.1.0, which resolves")
}

// Check 4b: the self-description names a go2cs release the check recognizes.

func TestSurfaceReleaseRed(t *testing.T) {
	f := newFake()
	f.packages[testID][0].selfDesc = strings.Replace(goodSelfDescription, "1.24.13.4", "1.24.13.99", 1)
	r := CheckRow(serve(t, f), testReleases, testRow("canonical"))
	if !r.Failed() {
		t.Fatalf("check 4b passed: %+v", r.Findings)
	}
	wantFinding(t, r, checkRelease, levelError, `go2cs release "1.24.13.99", which is not in cmd/sitegen/go2cs-releases.txt`)
}

func TestSurfaceReleaseGreen(t *testing.T) {
	for _, release := range []string{"1.24.13.3", "1.24.13.4"} {
		f := newFake()
		f.packages[testID][0].selfDesc = strings.Replace(goodSelfDescription, "1.24.13.4", release, 1)
		r := CheckRow(serve(t, f), parseReleases(go2csReleasesFile), testRow("canonical"))
		if r.Failed() {
			t.Fatalf("%s: failed: %+v", release, r.Findings)
		}
		wantFinding(t, r, checkRelease, levelNotice, "built against go2cs release "+release)
	}
	if releases := parseReleases(go2csReleasesFile); len(releases) == 0 || releases["#"] {
		t.Errorf("the checked-in release list reads as %v", releases)
	}
}

// Check 5: the auto-merge-eligible label decision.

func TestAutoMergeEligibleRed(t *testing.T) {
	const mappings = "v1/mappings.txt"
	confirmed := func() RowReport { return RowReport{Row: testRow("canonical"), Canonical: true} }
	failed := confirmed()
	failed.add(checkModule, levelError, "planted failure")
	community := RowReport{Row: testRow("community"), Canonical: true}
	unconfirmed := RowReport{Row: testRow("canonical")}
	withdrawn := RowReport{Row: testRow("withdrawn")}
	for name, c := range map[string]struct {
		reports []RowReport
		files   []string
		want    string
	}{
		"a check failed":         {[]RowReport{failed}, []string{mappings}, "a check failed"},
		"community row":          {[]RowReport{community}, []string{mappings}, "not canonical by validation"},
		"canonical not verified": {[]RowReport{unconfirmed}, []string{mappings}, "not canonical by validation"},
		"withdrawn row":          {[]RowReport{withdrawn}, []string{mappings}, "not canonical by validation"},
		"one of two rows":        {[]RowReport{confirmed(), community}, []string{mappings}, "not canonical by validation"},
		"code changed too":       {[]RowReport{confirmed()}, []string{"cmd/sitegen/checks.go", mappings}, "changes cmd/sitegen/checks.go"},
		"no row changed":         {nil, []string{"README.md"}, "changes README.md"},
		"mappings unchanged":     {nil, []string{mappings}, "adds or changes no row"},
		"no changed-files list":  {[]RowReport{confirmed()}, nil, "no changed file is listed"},
	} {
		if ok, reason := autoMergeEligible(c.reports, c.files, mappings); ok || !strings.Contains(reason, c.want) {
			t.Errorf("%s: got (%t, %q), want ineligible because %q", name, ok, reason, c.want)
		}
	}
}

func TestAutoMergeEligibleGreen(t *testing.T) {
	f := newFake()
	row := testRow("canonical")
	r := CheckRow(serve(t, f), testReleases, row)
	if ok, reason := autoMergeEligible([]RowReport{r}, []string{"v1/mappings.txt"}, "v1/mappings.txt"); !ok {
		t.Fatalf("a canonical-confirmed all-green row is ineligible: %s", reason)
	}
	// A pattern warning does not block the label.
	f.packages["example.hashset"] = f.packages[testID]
	row.NuGetID = "Example.HashSet"
	r = CheckRow(serve(t, f), testReleases, row)
	if ok, reason := autoMergeEligible([]RowReport{r}, []string{"v1/mappings.txt"}, "v1/mappings.txt"); !ok {
		t.Fatalf("a pattern warning blocked the label: %s", reason)
	}
}

func TestChangedRows(t *testing.T) {
	// rowFor is validRow for another module under example, with its own natural ID.
	rowFor := func(name string) string {
		row := strings.Replace(validRow, "github.com/example/hashset\t", "github.com/example/"+name+"\t", 1)
		return strings.Replace(row, "nugetgo.github.com.example.hashset", "nugetgo.github.com.example."+name, 1)
	}
	base := header + validRow + rowFor("kept")
	head, err := Parse("m.txt", []byte(header+strings.Replace(validRow, "\texample\n", "\tsomeone\n", 1)+rowFor("kept")+rowFor("new")))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range changedRows([]byte(base), head) {
		got = append(got, r.ModulePath)
	}
	if want := []string{"github.com/example/hashset", "github.com/example/new"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("changed rows %q, want %q", got, want)
	}
}

func TestWithdrawnRowSkipsNetworkChecks(t *testing.T) {
	remote := DefaultRemote()
	remote.Client = &http.Client{Transport: loopbackOnly{}}
	r := CheckRow(remote, testReleases, testRow("withdrawn"))
	if r.Failed() || len(r.Findings) != 1 || r.Findings[0].Level != levelNotice {
		t.Errorf("withdrawn row: %+v", r.Findings)
	}
}

func TestLatestVersion(t *testing.T) {
	no := false
	for _, c := range []struct {
		versions []PackageVersion
		want     string
	}{
		{[]PackageVersion{{Version: "1.0.0"}, {Version: "1.10.0"}, {Version: "1.9.0"}}, "1.10.0"},
		{[]PackageVersion{{Version: "1.0.0"}, {Version: "2.0.0-rc.1"}}, "1.0.0"},
		{[]PackageVersion{{Version: "1.0.0-rc.2"}, {Version: "1.0.0-rc.10"}, {Version: "1.0.0-beta"}}, "1.0.0-rc.10"},
		{[]PackageVersion{{Version: "1.0.0.2"}, {Version: "1.0.0"}, {Version: "1.0.0.10"}}, "1.0.0.10"},
		{[]PackageVersion{{Version: "0.0.0-20251001235044-fca9a0999f15"}, {Version: "0.0.0-20251001235044-fca9a0999f15.0.1"}}, "0.0.0-20251001235044-fca9a0999f15.0.1"},
		{[]PackageVersion{{Version: "1.0.0"}, {Version: "2.0.0", Listed: &no}}, "1.0.0"},
	} {
		if got, ok := latestVersion(c.versions); !ok || got.Version != c.want {
			t.Errorf("latestVersion(%v) = %q, want %q", c.versions, got.Version, c.want)
		}
	}
}

func TestEscapeModulePath(t *testing.T) {
	if got := escapeModulePath("github.com/BurntSushi/toml"); got != "github.com/!burnt!sushi/toml" {
		t.Errorf("got %q", got)
	}
}
