package main

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// The pull-request checks after the schema lint (CONTRIBUTING.md, Checks; go2cs docs/PLAN-nugetgo.md
// section 3). They run on the rows a pull request adds or changes, cheapest first, and stop at a row's
// first failing check.

// Check names, as the annotations title them.
const (
	checkPattern    = "check 1: ID pattern"
	checkExistence  = "check 2: existence"
	checkProvenance = "check 3: provenance"
	checkModule     = "check 4a: surface, module"
	checkRelease    = "check 4b: surface, go2cs release"
	checkSkipped    = "checks 2-4"
)

// Finding levels, named as GitHub Actions annotation commands.
const (
	levelError   = "error"
	levelWarning = "warning"
	levelNotice  = "notice"
)

// Finding is one check's verdict on one row.
type Finding struct {
	Check   string
	Level   string
	Message string
}

// RowReport is every finding for one added or changed row.
type RowReport struct {
	Row      Row
	Findings []Finding
	// Canonical is true when check 3 confirmed the canonical rule for the row's package.
	Canonical bool
}

func (r *RowReport) add(check, level, format string, args ...interface{}) {
	r.Findings = append(r.Findings, Finding{check, level, fmt.Sprintf(format, args...)})
}

// Failed reports whether any check failed the row.
func (r RowReport) Failed() bool {
	for _, f := range r.Findings {
		if f.Level == levelError {
			return true
		}
	}
	return false
}

// changedRows returns the rows of head that base does not have byte for byte: the rows a pull request
// adds or changes. base is read leniently, field by field, so a base row that a newer lint would refuse
// still counts as present.
func changedRows(base []byte, head []Row) []Row {
	before := map[string]string{}
	for _, line := range strings.Split(string(base), "\n") {
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(fields) == fieldCount && !strings.HasPrefix(line, "#") {
			before[fields[0]] = strings.Join(fields, "\t")
		}
	}
	var changed []Row
	for _, row := range head {
		text := strings.Join([]string{row.ModulePath, row.NuGetID, row.Status, row.SourceRepo, row.Registered, row.Contact}, "\t")
		if before[row.ModulePath] != text {
			changed = append(changed, row)
		}
	}
	return changed
}

// proofRe opens the PROOF description of go2cs owner ruling B6 (docs/PLAN-nugetgo.md section 8), in
// its third-party form, "PROOF: unofficial go2cs C# conversion of <module> <ver>, ...", and its
// author's form, "PROOF: go2cs C# conversion of <module> <ver>, published by its author, ...".
var proofRe = regexp.MustCompile(`^PROOF: (?:unofficial )?go2cs C# conversion of (\S+) (\S+), `)

// orgHosts are the hosts whose paths read host/ORG/repo, NugetgoIdentity.psm1 line 151:
// $script:OrgHosts = @('github.com', 'gitlab.com', 'bitbucket.org')
var orgHosts = map[string]bool{"github.com": true, "gitlab.com": true, "bitbucket.org": true}

// hostOrg is NugetgoIdentity.psm1 lines 153-157, Get-NugetgoHostOrg: "host/org" lowercased for a path
// on one of orgHosts, or "" when the path names no org that can be compared.
func hostOrg(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || !orgHosts[strings.ToLower(parts[0])] || parts[1] == "" {
		return ""
	}
	return strings.ToLower(parts[0]) + "/" + strings.ToLower(parts[1])
}

// repositoryOrg is the host/org of a RepositoryUrl, read as NugetgoIdentity.psm1 line 179 reads it:
// an https URL only.
func repositoryOrg(repositoryURL string) string {
	if !strings.HasPrefix(repositoryURL, "https://") {
		return ""
	}
	return hostOrg(strings.TrimPrefix(repositoryURL, "https://"))
}

func describe(err error) string {
	if errors.Is(err, errNotFound) {
		return "not found"
	}
	return err.Error()
}

// CheckRow runs checks 1 (the ID pattern warning), 2, 3, 4a and 4b on one added or changed row.
func CheckRow(remote *Remote, releases map[string]bool, row Row) RowReport {
	report := RowReport{Row: row}
	if row.Status == "withdrawn" {
		report.add(checkSkipped, levelNotice, "the row is withdrawn, which readers treat as unmapped; checks 2 to 4 do not run on it")
		return report
	}

	if msg := patternWarning(row); msg != "" {
		report.add(checkPattern, levelWarning, "%s", msg)
	}

	// Check 2: the module resolves at the proxy, and the package at NuGet with a release version.
	if err := remote.ModuleLatest(row.ModulePath); err != nil {
		report.add(checkExistence, levelError, "module %s does not resolve at the Go module proxy: %s", row.ModulePath, describe(err))
	} else {
		report.add(checkExistence, levelNotice, "module %s resolves at the Go module proxy", row.ModulePath)
	}
	var latest PackageVersion
	if versions, err := remote.PackageVersions(row.NuGetID); err != nil {
		report.add(checkExistence, levelError, "package %s does not resolve at NuGet's v3 API: %s", row.NuGetID, describe(err))
	} else if listed, err := listedVersions(versions); err != nil {
		report.add(checkExistence, levelError, "package %s: %v", row.NuGetID, err)
	} else if v, ok := latestVersion(listed); !ok {
		report.add(checkExistence, levelError, "package %s has no listed version at NuGet", row.NuGetID)
	} else if latest = v; !isPrerelease(latest.Version) {
		report.add(checkExistence, levelNotice, "package %s resolves at NuGet, latest release %s", row.NuGetID, latest.Version)
	} else {
		checkPrereleaseOnly(remote, &report, listed)
	}
	if report.Failed() {
		report.add(checkSkipped, levelNotice, "checks 3 and 4 did not run: check 2 failed")
		return report
	}

	// Check 3: the canonical rule, read from the latest version's RepositoryUrl.
	repositoryURL, err := remote.RepositoryURL(row.NuGetID, latest.Version)
	if err != nil {
		report.add(checkProvenance, levelError, "the nuspec of %s %s could not be read: %s", row.NuGetID, latest.Version, describe(err))
		return report
	}
	moduleOrg := hostOrg(row.ModulePath)
	var why string
	switch {
	case moduleOrg == "":
		why = fmt.Sprintf("the module path %s names no org on github.com, gitlab.com or bitbucket.org, so the canonical rule cannot compare it", row.ModulePath)
	case repositoryURL == "":
		why = fmt.Sprintf("%s %s has no RepositoryUrl", row.NuGetID, latest.Version)
	case repositoryOrg(repositoryURL) != moduleOrg:
		why = fmt.Sprintf("the RepositoryUrl %s of %s %s is not under %s, the module's org", repositoryURL, row.NuGetID, latest.Version, moduleOrg)
	default:
		report.Canonical = true
	}
	switch {
	case report.Canonical && row.Status == "canonical":
		report.add(checkProvenance, levelNotice, "canonical confirmed: the RepositoryUrl %s of %s %s is under %s", repositoryURL, row.NuGetID, latest.Version, moduleOrg)
	case report.Canonical:
		report.add(checkProvenance, levelNotice, "the row claims community, and its package is canonical by the rule: the RepositoryUrl %s of %s %s is under %s", repositoryURL, row.NuGetID, latest.Version, moduleOrg)
	case row.Status == "canonical":
		report.add(checkProvenance, levelError, "the claim canonical is contradicted: %s", why)
		return report
	default:
		report.add(checkProvenance, levelNotice, "community, as claimed: %s; a maintainer reviews the row", why)
	}

	// Check 4a and 4b: the self-description in the latest version's .nupkg.
	data, err := remote.SelfDescription(row.NuGetID, latest.Version)
	if err != nil {
		report.add(checkModule, levelError, "%s", describe(err))
		return report
	}
	desc, err := parseSelfDescription(data)
	if err != nil {
		report.add(checkModule, levelError, "%s %s: %s is malformed: %v", row.NuGetID, latest.Version, selfDescriptionPath, err)
		return report
	}
	if desc.Module != row.ModulePath {
		report.add(checkModule, levelError, "%s %s describes module %s, not %s", row.NuGetID, latest.Version, desc.Module, row.ModulePath)
	} else if err := remote.ModuleVersion(row.ModulePath, desc.ModuleVersion); err != nil {
		report.add(checkModule, levelError, "%s %s names module version %s %s, which does not resolve at the Go module proxy: %s", row.NuGetID, latest.Version, row.ModulePath, desc.ModuleVersion, describe(err))
	} else {
		report.add(checkModule, levelNotice, "%s %s describes %s %s, which resolves at the Go module proxy", row.NuGetID, latest.Version, desc.Module, desc.ModuleVersion)
	}
	if !releases[desc.Go2csRelease] {
		report.add(checkRelease, levelError, "%s %s was built against go2cs release %q, which is not in cmd/sitegen/go2cs-releases.txt", row.NuGetID, latest.Version, desc.Go2csRelease)
	} else {
		report.add(checkRelease, levelNotice, "%s %s was built against go2cs release %s", row.NuGetID, latest.Version, desc.Go2csRelease)
	}
	return report
}

// listedVersions returns the listed versions, refusing a version that is not a NuGet version.
func listedVersions(versions []PackageVersion) ([]PackageVersion, error) {
	var listed []PackageVersion
	for _, v := range versions {
		if !nugetVersionRe.MatchString(v.Version) {
			return nil, fmt.Errorf("the registration lists the version %q, which is not a NuGet version", v.Version)
		}
		if v.listed() {
			listed = append(listed, v)
		}
	}
	return listed, nil
}

// checkPrereleaseOnly is check 2's amendment for a package with no listed release version (go2cs
// docs/PLAN-nugetgo.md section 3, "AMENDED 2026-09-30 (owner ruling B6, §8)"): "A module whose only Go
// versions are prereleases or pseudo-versions has none by construction, so for such a module the
// check accepts a prerelease package version that carries the PROOF text of B6 and the existence of
// the Go module version at proxy.golang.org."
func checkPrereleaseOnly(remote *Remote, report *RowReport, listed []PackageVersion) {
	row := report.Row
	tags, err := remote.ModuleVersions(row.ModulePath)
	if err != nil {
		report.add(checkExistence, levelError, "package %s has no listed release version, and the module's version list could not be read: %s", row.NuGetID, describe(err))
		return
	}
	for _, tag := range tags {
		if isGoRelease(tag) {
			report.add(checkExistence, levelError, "package %s has no listed release version, and module %s has the release %s, so a prerelease package is not enough", row.NuGetID, row.ModulePath, tag)
			return
		}
	}
	reasons := []string{}
	for _, v := range listed {
		m := proofRe.FindStringSubmatch(v.Description)
		switch {
		case m == nil:
			reasons = append(reasons, fmt.Sprintf("%s: the description does not open with the PROOF text", v.Version))
		case m[1] != row.ModulePath:
			reasons = append(reasons, fmt.Sprintf("%s: the PROOF text names module %s", v.Version, m[1]))
		case !goVersionRe.MatchString(m[2]):
			reasons = append(reasons, fmt.Sprintf("%s: the PROOF text names %q, which is not a Go module version", v.Version, m[2]))
		default:
			if err := remote.ModuleVersion(row.ModulePath, m[2]); err != nil {
				reasons = append(reasons, fmt.Sprintf("%s: the PROOF text names %s %s, which does not resolve at the Go module proxy: %s", v.Version, row.ModulePath, m[2], describe(err)))
				continue
			}
			report.add(checkExistence, levelNotice, "module %s has only prerelease or pseudo-versions; package %s %s carries the PROOF text for %s, which resolves at the Go module proxy", row.ModulePath, row.NuGetID, v.Version, m[2])
			return
		}
	}
	report.add(checkExistence, levelError, "package %s has no listed release version and no prerelease version that passes the prerelease-only rule: %s", row.NuGetID, strings.Join(reasons, "; "))
}

// autoMergeEligible is check 5's decision, which only labels: every check passes, every added or
// changed row is canonical by validation, and the pull request changes no file but the mappings file.
// It returns the decision and its reason.
func autoMergeEligible(reports []RowReport, changedFiles []string, mappingsPath string) (bool, string) {
	for _, f := range changedFiles {
		if f != mappingsPath {
			return false, fmt.Sprintf("the pull request changes %s, not only %s", f, mappingsPath)
		}
	}
	if len(changedFiles) == 0 {
		return false, "no changed file is listed"
	}
	if len(reports) == 0 {
		return false, "the pull request adds or changes no row"
	}
	for _, r := range reports {
		if r.Failed() {
			return false, fmt.Sprintf("a check failed for %s", r.Row.ModulePath)
		}
		if r.Row.Status != "canonical" || !r.Canonical {
			return false, fmt.Sprintf("%s is not canonical by validation", r.Row.ModulePath)
		}
	}
	return true, "every check passed and every added or changed row is canonical by validation"
}

// escapeData and escapeProperty are GitHub Actions' workflow-command escaping.
func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

// WriteAnnotations prints every finding as a GitHub Actions annotation on the row's line.
func WriteAnnotations(w io.Writer, file string, reports []RowReport) {
	for _, r := range reports {
		for _, f := range r.Findings {
			fmt.Fprintf(w, "::%s file=%s,line=%d,title=%s::%s: %s\n", f.Level, escapeProperty(file), r.Row.Line, escapeProperty(f.Check), escapeData(r.Row.ModulePath), escapeData(f.Message))
		}
	}
}
