package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Remote reads the two services a row's checks consult: the Go module proxy and NuGet's v3 API.
// Every URL is built from the base fields, so tests point them at an httptest server.
type Remote struct {
	Client *http.Client
	// Proxy is the Go module proxy, without a trailing slash.
	Proxy string
	// Registration is NuGet's v3 RegistrationsBaseUrl (the SemVer 2.0.0 hive), without a trailing slash.
	Registration string
	// Flat is NuGet's v3 PackageBaseAddress (the flat container), without a trailing slash.
	Flat string
}

// DefaultRemote reads proxy.golang.org and nuget.org, giving each request two minutes.
func DefaultRemote() *Remote {
	return &Remote{
		Client:       &http.Client{Timeout: 2 * time.Minute},
		Proxy:        "https://proxy.golang.org",
		Registration: "https://api.nuget.org/v3/registration5-gz-semver2",
		Flat:         "https://api.nuget.org/v3-flatcontainer",
	}
}

// Size caps on what is read from the network.
const (
	maxJSONBytes    = 32 << 20
	maxNupkgBytes   = 256 << 20
	maxNuspecBytes  = 1 << 20
	maxSelfDescSize = 16 << 20
)

// errNotFound is a 404 or 410 answer: the thing asked for does not exist.
var errNotFound = errors.New("not found")

func (r *Remote) get(u string, limit int64) ([]byte, error) {
	resp, err := r.Client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	data, err := ioutil.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %v", u, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: response is over %d bytes", u, limit)
	}
	return data, nil
}

// escapeModulePath is the Go module proxy's case encoding: each uppercase letter becomes '!' and its
// lowercase form (golang.org/x/mod/module.EscapePath, which this package does not import).
func escapeModulePath(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(c))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// ModuleLatest reports whether the module resolves at the proxy, by its @latest query, which answers
// for a module with tagged versions and for one with pseudo-versions only.
func (r *Remote) ModuleLatest(modulePath string) error {
	_, err := r.get(r.Proxy+"/"+escapeModulePath(modulePath)+"/@latest", maxJSONBytes)
	return err
}

// ModuleVersion reports whether one module version resolves at the proxy.
func (r *Remote) ModuleVersion(modulePath, version string) error {
	_, err := r.get(r.Proxy+"/"+escapeModulePath(modulePath)+"/@v/"+escapeModulePath(version)+".info", maxJSONBytes)
	return err
}

// ModuleVersions is the module's @v/list: its tagged versions, without pseudo-versions.
func (r *Remote) ModuleVersions(modulePath string) ([]string, error) {
	data, err := r.get(r.Proxy+"/"+escapeModulePath(modulePath)+"/@v/list", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}

// PackageVersion is one version of a NuGet package, from its registration leaf's catalogEntry.
type PackageVersion struct {
	Version     string `json:"version"`
	Listed      *bool  `json:"listed"`
	Description string `json:"description"`
}

// listed is true unless the registration says the version is unlisted.
func (v PackageVersion) listed() bool { return v.Listed == nil || *v.Listed }

type registrationLeaf struct {
	CatalogEntry PackageVersion `json:"catalogEntry"`
}

type registrationPage struct {
	ID    string             `json:"@id"`
	Items []registrationLeaf `json:"items"`
}

type registrationIndex struct {
	Items []registrationPage `json:"items"`
}

// PackageVersions reads the package's registration and returns every version it lists, listed or not.
// A page whose leaves are not inlined in the index is fetched, but only from the registration base.
func (r *Remote) PackageVersions(id string) ([]PackageVersion, error) {
	data, err := r.get(r.Registration+"/"+strings.ToLower(id)+"/index.json", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	var index registrationIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("registration of %s: %v", id, err)
	}
	var versions []PackageVersion
	for _, page := range index.Items {
		if page.Items == nil {
			if !strings.HasPrefix(page.ID, r.Registration+"/") {
				return nil, fmt.Errorf("registration of %s: page %q is outside %s", id, page.ID, r.Registration)
			}
			data, err := r.get(page.ID, maxJSONBytes)
			if err != nil {
				return nil, fmt.Errorf("registration page %s: %v", page.ID, err)
			}
			if err := json.Unmarshal(data, &page); err != nil {
				return nil, fmt.Errorf("registration page %s: %v", page.ID, err)
			}
		}
		for _, leaf := range page.Items {
			versions = append(versions, leaf.CatalogEntry)
		}
	}
	return versions, nil
}

// nugetVersionRe is the shape of a NuGet package version: one to four numeric parts, an optional
// prerelease label and optional build metadata. A version read from the registration is checked
// against it before it becomes part of a URL.
var nugetVersionRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,3}(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// flatURL is a file of one package version in the flat container: lowercase ID and version, no build metadata.
func (r *Remote) flatURL(id, version, ext string) string {
	id, version = strings.ToLower(id), strings.ToLower(stripBuild(version))
	if ext == "nuspec" {
		return r.Flat + "/" + id + "/" + version + "/" + id + ".nuspec"
	}
	return r.Flat + "/" + id + "/" + version + "/" + id + "." + version + ".nupkg"
}

type nuspec struct {
	Metadata struct {
		Repository struct {
			URL string `xml:"url,attr"`
		} `xml:"repository"`
	} `xml:"metadata"`
}

// RepositoryURL is the package version's RepositoryUrl: the url of the nuspec's repository element, as
// NuGet serves the nuspec. The registration's catalogEntry does not carry it.
func (r *Remote) RepositoryURL(id, version string) (string, error) {
	data, err := r.get(r.flatURL(id, version, "nuspec"), maxNuspecBytes)
	if err != nil {
		return "", err
	}
	var spec nuspec
	if err := xml.Unmarshal(data, &spec); err != nil {
		return "", fmt.Errorf("nuspec of %s %s: %v", id, version, err)
	}
	return strings.TrimSpace(spec.Metadata.Repository.URL), nil
}

// SelfDescription downloads the package version's .nupkg and returns the go2cs self-description entry.
func (r *Remote) SelfDescription(id, version string) ([]byte, error) {
	data, err := r.get(r.flatURL(id, version, "nupkg"), maxNupkgBytes)
	if err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%s %s: the .nupkg is not a zip archive: %v", id, version, err)
	}
	var found *zip.File
	for _, f := range archive.File {
		if f.Name == selfDescriptionPath {
			if found != nil {
				return nil, fmt.Errorf("%s %s: the .nupkg has %s twice", id, version, selfDescriptionPath)
			}
			found = f
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%s %s: the .nupkg has no %s", id, version, selfDescriptionPath)
	}
	rc, err := found.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	body, err := ioutil.ReadAll(io.LimitReader(rc, maxSelfDescSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSelfDescSize {
		return nil, fmt.Errorf("%s %s: %s is over %d bytes", id, version, selfDescriptionPath, maxSelfDescSize)
	}
	return body, nil
}

// stripBuild drops a version's build metadata.
func stripBuild(v string) string {
	if i := strings.IndexByte(v, '+'); i >= 0 {
		return v[:i]
	}
	return v
}

// isPrerelease reports whether a NuGet version has a prerelease label.
func isPrerelease(v string) bool {
	return strings.Contains(stripBuild(v), "-")
}

// compareNuGet orders two NuGet versions by NuGet's rules: up to four numeric parts, missing parts
// zero; a release above any of its prereleases; prerelease labels by SemVer 2.0.0, compared
// dot-separated, numeric identifiers numerically and others case-insensitively.
func compareNuGet(a, b string) int {
	a, b = stripBuild(a), stripBuild(b)
	splitPre := func(v string) (string, string) {
		if i := strings.IndexByte(v, '-'); i >= 0 {
			return v[:i], v[i+1:]
		}
		return v, ""
	}
	coreA, preA := splitPre(a)
	coreB, preB := splitPre(b)
	partsA, partsB := strings.Split(coreA, "."), strings.Split(coreB, ".")
	for i := 0; i < 4; i++ {
		var x, y uint64
		if i < len(partsA) {
			x, _ = strconv.ParseUint(partsA[i], 10, 64)
		}
		if i < len(partsB) {
			y, _ = strconv.ParseUint(partsB[i], 10, 64)
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case preA == "" && preB == "":
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	idsA, idsB := strings.Split(preA, "."), strings.Split(preB, ".")
	for i := 0; i < len(idsA) && i < len(idsB); i++ {
		x, errX := strconv.ParseUint(idsA[i], 10, 64)
		y, errY := strconv.ParseUint(idsB[i], 10, 64)
		var c int
		switch {
		case errX == nil && errY == nil:
			if x < y {
				c = -1
			} else if x > y {
				c = 1
			}
		case errX == nil:
			c = -1
		case errY == nil:
			c = 1
		default:
			c = strings.Compare(strings.ToLower(idsA[i]), strings.ToLower(idsB[i]))
		}
		if c != 0 {
			return c
		}
	}
	switch {
	case len(idsA) < len(idsB):
		return -1
	case len(idsA) > len(idsB):
		return 1
	}
	return 0
}

// latestVersion is NuGet's latest version of a package: the highest listed release, or, when no
// release is listed, the highest listed prerelease. It returns false when nothing is listed.
func latestVersion(versions []PackageVersion) (PackageVersion, bool) {
	var best PackageVersion
	found := false
	for _, v := range versions {
		if !v.listed() {
			continue
		}
		if !found || isPrerelease(best.Version) && !isPrerelease(v.Version) ||
			isPrerelease(best.Version) == isPrerelease(v.Version) && compareNuGet(v.Version, best.Version) > 0 {
			best, found = v, true
		}
	}
	return best, found
}
