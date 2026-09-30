// Command sitegen validates v1/mappings.txt and generates the static registry site.
//
// It writes three files under the output directory: index.html, rendered from the mappings file;
// v1/mappings.txt, a byte-for-byte copy of the input; and .nojekyll. Any invalid data row fails
// the run with its line number, so the same command is the schema lint for pull requests.
//
// Usage:
//
//	go run ./cmd/sitegen [-in v1/mappings.txt] [-out _site]
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	in := flag.String("in", filepath.Join("v1", "mappings.txt"), "mappings file to read")
	out := flag.String("out", "_site", "directory to write the site into")
	flag.Parse()

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
