package main

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"

	_ "embed"
)

// The go2cs self-description a published conversion carries (go2cs docs/PLAN-nugetgo.md section 5, its
// 2026-10-02 amendment, "Format v1"). The reader below mirrors the header rules of go2cs's own strict
// parser, src/go2cs/internal/sourcemeta/sourcemeta.go (Parse, lines 166-275), and reads no further than
// the registry's checks need: the metadata sections after the header are skipped.

// selfDescriptionPath is sourcemeta.go line 45: const EntryPath = "go2cs/source-metadata.txt"
const selfDescriptionPath = "go2cs/source-metadata.txt"

// selfDescriptionMagic is sourcemeta.go line 48: const Magic = "#go2cs-source-metadata v1"
const selfDescriptionMagic = "#go2cs-source-metadata v1"

// sectionPrefix starts a package's metadata section, stdlib-metadata.txt's "##<dotted package>".
const sectionPrefix = "##"

// goVersionRe is a Go module version as go2cs's pack reads it, NugetgoIdentity.psm1 line 118:
// '^v(?<core>(?<maj>\d+)\.(?<min>\d+)\.(?<pat>\d+))(?:-(?<pre>[0-9A-Za-z.-]+))?(?<inc>\+incompatible)?$'
var goVersionRe = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z.-]+)?(\+incompatible)?$`)

// isGoRelease reports whether a Go module version is a release: no prerelease label, so neither a
// prerelease nor a pseudo-version.
func isGoRelease(v string) bool {
	m := goVersionRe.FindStringSubmatch(v)
	return m != nil && m[4] == ""
}

// SelfDescription is the header of a go2cs self-description.
type SelfDescription struct {
	Module        string
	ModuleVersion string
	Go2csRelease  string
	Packages      []string
}

// parseSelfDescription reads a self-description's header strictly, refusing what go2cs's parser
// refuses there: a byte-order mark, a carriage return, a missing or different magic line, a repeated
// or missing module, module-version or go2cs-release line, a malformed require or package line, an
// unknown key, and a package outside the module.
func parseSelfDescription(data []byte) (SelfDescription, error) {
	var d SelfDescription
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		return d, fmt.Errorf("line 1: a byte-order mark (the file is UTF-8 without one)")
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		if strings.Contains(line, "\r") {
			return d, fmt.Errorf("line %d: a carriage return (the file is written with LF line endings)", i+1)
		}
	}
	if len(lines) == 0 || lines[0] != selfDescriptionMagic {
		return d, fmt.Errorf("line 1: not a go2cs v1 self-description (want %q)", selfDescriptionMagic)
	}
	seen := map[string]bool{}
	for i := 1; i < len(lines); i++ {
		line, number := lines[i], i+1
		if strings.HasPrefix(line, sectionPrefix) {
			break // the metadata sections; the header is complete
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest := line, ""
		if j := strings.IndexByte(line, ' '); j >= 0 {
			key, rest = line[:j], line[j+1:]
		}
		fields := strings.Fields(rest)
		switch key {
		case "module", "module-version", "go2cs-release":
			if seen[key] {
				return d, fmt.Errorf("line %d: a second %s line", number, key)
			}
			if len(fields) != 1 {
				return d, fmt.Errorf("line %d: %s needs exactly one value", number, key)
			}
			seen[key] = true
			switch key {
			case "module":
				d.Module = fields[0]
			case "module-version":
				d.ModuleVersion = fields[0]
			default:
				d.Go2csRelease = fields[0]
			}
		case "require":
			if len(fields) != 3 {
				return d, fmt.Errorf("line %d: require needs <module> <version> <nuget-id>, got %q", number, rest)
			}
			// sourcemeta.go line 361, CheckModuleNuGetID: a required module's ID never takes "go.".
			if strings.HasPrefix(strings.ToLower(fields[2]), stdlibIDPrefix) {
				return d, fmt.Errorf("line %d: require %s: nuget-id %q uses the %q prefix, which is the converted Go standard library", number, fields[0], fields[2], stdlibIDPrefix)
			}
		case "package":
			if len(fields) != 2 {
				return d, fmt.Errorf("line %d: package needs <import-path> <assembly>, got %q", number, rest)
			}
			d.Packages = append(d.Packages, fields[0])
		default:
			return d, fmt.Errorf("line %d: unknown key %q (a v1 self-description has module, module-version, go2cs-release, require and package lines before its sections)", number, key)
		}
	}
	for _, key := range []string{"module", "module-version", "go2cs-release"} {
		if !seen[key] {
			return d, fmt.Errorf("no %s line", key)
		}
	}
	if !goVersionRe.MatchString(d.ModuleVersion) {
		return d, fmt.Errorf("module-version %q is not a Go module version (vX.Y.Z[-prerelease][+incompatible])", d.ModuleVersion)
	}
	if len(d.Packages) == 0 {
		return d, fmt.Errorf("no package line: a self-description describes at least one packed package")
	}
	for _, p := range d.Packages {
		if p != d.Module && !strings.HasPrefix(p, d.Module+"/") {
			return d, fmt.Errorf("package %s is outside module %s", p, d.Module)
		}
	}
	return d, nil
}

// go2csReleases is the checked-in list of go2cs releases the surface check recognizes.
//
//go:embed go2cs-releases.txt
var go2csReleasesFile string

// parseReleases reads the release list: one release per line, '#' comments and blank lines skipped.
func parseReleases(text string) map[string]bool {
	releases := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			releases[line] = true
		}
	}
	return releases
}
