package main

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Row is one mapping line of v1/mappings.txt.
type Row struct {
	Line       int
	ModulePath string
	NuGetID    string
	Status     string
	SourceRepo string
	Registered string
	Contact    string
}

// fieldCount is the number of TAB-separated fields in a schema v1 row.
const fieldCount = 6

var fieldNames = [fieldCount]string{"module-path", "nuget-id", "status", "source-repo", "registered", "contact"}

var statuses = map[string]bool{"canonical": true, "community": true, "withdrawn": true}

// nugetIDRe is NuGet's package ID rule, restricted to ASCII.
var nugetIDRe = regexp.MustCompile(`^[A-Za-z0-9_]+([.-][A-Za-z0-9_]+)*$`)

const maxNuGetIDLength = 100

// majorSuffixRe matches a final module path element that Go rejects as a major version suffix:
// /v0, /v1, or any /vN with a leading zero.
var majorSuffixRe = regexp.MustCompile(`^v(0[0-9]*|1)$`)

// ValidationError lists every problem found in the file, each with its line number.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Problems, "\n")
}

// Parse reads schema v1 mappings and validates every data row. It returns the rows in file order,
// or a *ValidationError naming each offending line.
func Parse(name string, data []byte) ([]Row, error) {
	return parseAt(name, data, time.Now())
}

// parseAt is Parse with the current time supplied, so a registered date can be checked against it.
func parseAt(name string, data []byte, now time.Time) ([]Row, error) {
	var problems []string
	fail := func(line int, format string, args ...interface{}) {
		problems = append(problems, fmt.Sprintf("%s:%d: %s", name, line, fmt.Sprintf(format, args...)))
	}

	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		fail(1, "file starts with a byte order mark; save it as UTF-8 without BOM")
	}

	var rows []Row
	seen := map[string]int{}
	seenID := map[string]int{}
	latest := now.UTC().AddDate(0, 0, 1).Format("2006-01-02")
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, text := range lines {
		line := i + 1
		if !utf8.ValidString(text) {
			fail(line, "line is not valid UTF-8")
			continue
		}
		if strings.Contains(text, "\r") {
			fail(line, "line contains a carriage return; use LF line endings")
			continue
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.TrimSpace(text) == "" {
			fail(line, "line contains only whitespace")
			continue
		}

		fields := strings.Split(text, "\t")
		if len(fields) != fieldCount {
			fail(line, "has %d TAB-separated fields, want %d (%s)", len(fields), fieldCount, strings.Join(fieldNames[:], ", "))
			continue
		}

		bad := false
		for j, f := range fields {
			switch {
			case f == "":
				fail(line, "field %s is empty", fieldNames[j])
				bad = true
			case strings.TrimSpace(f) != f:
				fail(line, "field %s has leading or trailing whitespace", fieldNames[j])
				bad = true
			}
			for _, r := range f {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					fail(line, "field %s contains a control or invisible character %U", fieldNames[j], r)
					bad = true
					break
				}
			}
		}
		if bad {
			continue
		}

		row := Row{
			Line:       line,
			ModulePath: fields[0],
			NuGetID:    fields[1],
			Status:     fields[2],
			SourceRepo: fields[3],
			Registered: fields[4],
			Contact:    fields[5],
		}

		if msg := checkModulePath(row.ModulePath); msg != "" {
			fail(line, "module-path %q: %s", row.ModulePath, msg)
		}
		if len(row.NuGetID) > maxNuGetIDLength {
			fail(line, "nuget-id is %d characters, NuGet allows at most %d", len(row.NuGetID), maxNuGetIDLength)
		} else if !nugetIDRe.MatchString(row.NuGetID) {
			fail(line, "nuget-id %q is not a valid NuGet package ID", row.NuGetID)
		}
		if !statuses[row.Status] {
			fail(line, "status %q is not one of canonical, community, withdrawn", row.Status)
		}
		if u, err := url.Parse(row.SourceRepo); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			fail(line, "source-repo %q is not an https URL", row.SourceRepo)
		} else if !isASCII(u.Host) {
			fail(line, "source-repo %q has a host with non-ASCII characters; use the ASCII (punycode) form", row.SourceRepo)
		}
		if _, err := time.Parse("2006-01-02", row.Registered); err != nil {
			fail(line, "registered %q is not a date in YYYY-MM-DD form", row.Registered)
		} else if row.Registered > latest {
			fail(line, "registered %q is in the future", row.Registered)
		}

		if first, ok := seen[row.ModulePath]; ok {
			fail(line, "module-path %q already has a row at line %d; one row per module major version", row.ModulePath, first)
		} else {
			seen[row.ModulePath] = line
		}
		idKey := strings.ToLower(row.NuGetID)
		if first, ok := seenID[idKey]; ok {
			fail(line, "nuget-id %q already used at line %d (NuGet IDs are case-insensitive)", row.NuGetID, first)
		} else {
			seenID[idKey] = line
		}
		if n := len(rows); n > 0 && row.ModulePath < rows[n-1].ModulePath {
			fail(line, "module-path %q sorts before %q at line %d; keep rows sorted by module-path", row.ModulePath, rows[n-1].ModulePath, rows[n-1].Line)
		}

		rows = append(rows, row)
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return rows, nil
}

// checkModulePath applies basic Go module path syntax: slash-separated non-empty elements drawn
// from the characters Go allows, a first element that looks like a lowercase host name, and no
// /v0 or /v1 major version suffix. It
// returns "" when the path passes. It is a lint, not a full reimplementation of the go command's
// rules; whether the module exists is checked separately.
func checkModulePath(p string) string {
	elems := strings.Split(p, "/")
	for i, e := range elems {
		if e == "" {
			return "has an empty path element"
		}
		if e == "." || e == ".." {
			return "has a . or .. path element"
		}
		if e[0] == '.' || e[len(e)-1] == '.' {
			return "has a path element that starts or ends with a dot"
		}
		for _, r := range e {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)
			if !ok {
				return fmt.Sprintf("contains the character %q", r)
			}
		}
		if i == 0 {
			if !strings.Contains(e, ".") {
				return "first path element has no dot, so it is not a host name"
			}
			if e[0] == '-' {
				return "first path element starts with a hyphen"
			}
			for _, r := range e {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
					return "first path element may contain only lowercase letters, digits, dots and hyphens"
				}
			}
		}
	}
	if len(elems) > 1 && elems[0] != "gopkg.in" && majorSuffixRe.MatchString(elems[len(elems)-1]) {
		return "ends in a /v0 or /v1 style suffix; Go allows a major version suffix only for v2 and later"
	}
	return ""
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}
