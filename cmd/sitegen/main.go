// Command sitegen validates v1/mappings.txt and generates the static registry site.
//
// It writes three files under the output directory: index.html, rendered from the mappings file;
// v1/mappings.txt, a byte-for-byte copy of the input; and .nojekyll. Any invalid data row fails
// the run with its line number, so the same command is the schema lint for pull requests.
//
// With -check, it writes no site. It lints the file, then runs the registry's network checks on the
// rows the file adds or changes against the base file (CONTRIBUTING.md, Checks), printing each
// finding as a GitHub Actions annotation, and fails when any check fails. -changed-files names a file
// listing the pull request's changed paths, one per line, for the auto-merge-eligible decision, which
// is written to $GITHUB_OUTPUT as eligible=true or eligible=false when that variable is set.
//
// Usage:
//
//	go run ./cmd/sitegen [-in v1/mappings.txt] [-out _site]
//	go run ./cmd/sitegen -check base-mappings.txt [-in v1/mappings.txt] [-changed-files changed.txt]
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	in := flag.String("in", filepath.Join("v1", "mappings.txt"), "mappings file to read")
	out := flag.String("out", "_site", "directory to write the site into")
	check := flag.String("check", "", "base mappings file; run the network checks on the rows -in adds or changes")
	changed := flag.String("changed-files", "", "file listing the pull request's changed paths, for -check")
	flag.Parse()

	if *check != "" {
		os.Exit(runChecks(*in, *check, *changed))
	}

	n, err := Build(*in, *out)
	if err != nil {
		var verr *ValidationError
		if errors.As(err, &verr) {
			fmt.Fprintln(os.Stderr, verr.Error())
			fmt.Fprintf(os.Stderr, "sitegen: %d problem(s) in %s\n", len(verr.Problems), *in)
		} else {
			fmt.Fprintln(os.Stderr, "sitegen:", err)
		}
		os.Exit(1)
	}
	fmt.Printf("sitegen: %d mapping(s) validated; site written to %s\n", n, *out)
}

// runChecks is the -check mode. It returns the process exit code.
func runChecks(in, base, changedList string) int {
	data, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		return 1
	}
	name := filepath.ToSlash(in)
	rows, err := Parse(name, data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	baseData, err := os.ReadFile(base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		return 1
	}
	var changedFiles []string
	if changedList != "" {
		list, err := os.ReadFile(changedList)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sitegen:", err)
			return 1
		}
		changedFiles = strings.Fields(string(list))
	}

	remote, releases := DefaultRemote(), parseReleases(go2csReleasesFile)
	var reports []RowReport
	failed := 0
	for _, row := range changedRows(baseData, rows) {
		report := CheckRow(remote, releases, row)
		if report.Failed() {
			failed++
		}
		reports = append(reports, report)
	}
	WriteAnnotations(os.Stdout, name, reports)

	eligible, reason := autoMergeEligible(reports, changedFiles, name)
	fmt.Printf("::notice title=check 5%%3A auto-merge-eligible::%t: %s\n", eligible, escapeData(reason))
	fmt.Printf("sitegen: %d row(s) added or changed, %d failed\n", len(reports), failed)
	if path := os.Getenv("GITHUB_OUTPUT"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, err = fmt.Fprintf(f, "eligible=%t\n", eligible)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "sitegen:", err)
			return 1
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// Build validates the mappings file at in and writes the site under out. It returns the number
// of data rows. Nothing is written when validation fails.
func Build(in, out string) (int, error) {
	data, err := os.ReadFile(in)
	if err != nil {
		return 0, err
	}
	rows, err := Parse(filepath.ToSlash(in), data)
	if err != nil {
		return 0, err
	}

	var index bytes.Buffer
	if err := Render(&index, rows); err != nil {
		return 0, err
	}

	if err := os.MkdirAll(filepath.Join(out, "v1"), 0o755); err != nil {
		return 0, err
	}
	files := []struct {
		path string
		data []byte
	}{
		{filepath.Join(out, "index.html"), index.Bytes()},
		{filepath.Join(out, "v1", "mappings.txt"), data},
		{filepath.Join(out, ".nojekyll"), nil},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}
