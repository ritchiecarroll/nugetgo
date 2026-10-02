# Contributing to nugetgo

nugetgo is one data file, [`v1/mappings.txt`](v1/mappings.txt). A mapping is added, changed or
withdrawn by a pull request that edits that file. This page is the registry's policy. It is stated
here before any conflict exists, so that conflicts are settled by rules written in advance.

By contributing you dedicate your contribution to the public domain under
[CC0-1.0](LICENSE), the license of the whole repository.

## The row format

One mapping per line, six fields separated by single TAB characters:

```
module-path	nuget-id	status	source-repo	registered	contact
```

| Field | Content |
|:--|:--|
| `module-path` | The Go module path exactly as in `go.mod`, including any `/vN` suffix. |
| `nuget-id` | The published NuGet package ID of the go2cs conversion: `nugetgo.` followed by the dotted module path, or its hash-shortened alternate (see [NuGet package IDs](#nuget-package-ids)). |
| `status` | `canonical`, `community` or `withdrawn`. |
| `source-repo` | The `https` URL of the repository that holds the C# conversion. |
| `registered` | The date the row was registered, `YYYY-MM-DD`. |
| `contact` | Who to contact about the row, such as a GitHub handle. |

Rows are sorted by `module-path` in byte order. Lines starting with `#` are comments. The file is
UTF-8 without a byte order mark, with LF line endings.

## NuGet package IDs

The NuGet package ID of a converted Go module is `nugetgo.` followed by the dotted module path.
This is the pattern for every go2cs conversion of a Go module, whoever publishes it, the module's
own author included.

- **The ID** is `nugetgo.` followed by the module path with every `/` replaced by `.`, keeping
  every path segment. `github.com/example/widget/v2` becomes
  `nugetgo.github.com.example.widget.v2`. The package display name keeps Go's letter case.
- **A module conversion does not take the `go.` prefix.** go2cs uses that prefix for the converted
  Go standard library (`go.fmt`, `go.net.http`) and for its own runtime and source-generator
  packages.
- **Status does not change the ID.** A `canonical` row (the conversion comes from the module's
  own org, verified as the [trust policy](#trust-policy) describes) and a `community` row (it
  comes from someone else) follow the same pattern. The prefix says nothing about where a
  conversion comes from; a row's `canonical` or `community` status does.
- When the natural ID fails nuget.org's package ID rule, collides case-insensitively with another
  ID, or collides because two paths dot to the same ID (`a/b.c` and `a.b/c`), a hash-shortened
  alternate ID is used instead. The alternate keeps the `nugetgo.` prefix.

The registry row is the authority on which package a module maps to: a reader of the file takes
the ID from the row and does not derive it from the module path.

## Trust policy

**Canonical is verified, never asserted.** A row is `canonical` when the NuGet package's own
repository metadata (its `RepositoryUrl`, or its verified repository on nuget.org) points to the
same code-hosting org as the module path. For `github.com/ORG/repo[/vN]`, the package's repository
must be under `github.com/ORG/`. The status a contributor writes in the row is a claim; the check
decides it.

**Community rows are allowed.** A conversion of someone else's module is marked `community`. A
community row is never merged automatically; a maintainer reviews it with the check results.

**One row per module major version.** `example.com/mod` and `example.com/mod/v2` are separate
modules and may each have a row. A module path appears at most once.

**Only a canonical row displaces an existing row.** A later community claimant does not replace an
existing community row. When a canonical mapping arrives for a module that has a community row,
the canonical row replaces it.

**Community ties go to the first registered.** Between two community claimants for one module, the
row registered first stands.

**Disputes.** To dispute a row, open an issue. The module owner's stated preference, in the
module's repository or in the issue, is final.

**Withdrawn rows are kept.** A row can be withdrawn by setting its status to `withdrawn`, for
example when its package is found to be wrong or unsafe. The row stays in the file as the record.
Readers of the file, including go2cs once it reads the registry, treat a withdrawn row as unmapped.
A withdrawn row still holds its module path: a new mapping for that module replaces the withdrawn
row in place, through a pull request reviewed like any other that links the withdrawal, and the
file's history keeps the withdrawn row.

## Checks

Every pull request that adds or changes a row is validated in this order, cheapest first:

1. **Schema lint.** Six TAB-separated fields, no empty or padded field, no control or invisible
   characters, a module path that passes a basic syntax check, a valid NuGet package ID that no
   other row uses (compared case-insensitively), a known status, an `https` source repository with
   an ASCII host, a `YYYY-MM-DD` date that is not in the future, one row per module path, and rows
   in sorted order.
2. **Existence.** The module resolves at `proxy.golang.org`, and the package ID resolves at
   nuget.org with at least one non-prerelease version. For a module whose only Go versions are
   prereleases or pseudo-versions, a prerelease package version is accepted when its description
   carries the PROOF text and the Go module version exists at `proxy.golang.org`.
3. **Provenance.** The canonical rule above, evaluated from the package's registration metadata.
   The result confirms or contradicts the row's claimed status.
4. **Surface.** The package's latest version carries a go2cs self-description that names the
   claimed module path and a real module version, was produced by a go2cs release the current
   toolchain recognizes, and matches the exported surface of a fresh go2cs conversion of that
   module version.

**What is automated today.** Only step 1. The `ci` workflow runs `go run ./cmd/sitegen`, which
validates every row and fails with the line number of each problem. You can run the same command
locally before opening a pull request. Steps 2 to 4 are checked by hand by a maintainer until the
registry's validation CI lands.

## Merging

The merge policy is the checks above and nothing else. A pull request is merged automatically only
when every check passes **and** the row is canonical by validation. A contributor's claim of
`canonical` never merges a row on its own. Every other pull request waits for a maintainer, with
the check results on the pull request.

Until the validation CI lands, nothing merges automatically: a maintainer runs steps 2 to 4 and
merges by hand.

## Changing the site generator

The generator lives in [`cmd/sitegen`](cmd/sitegen). Before opening a pull request, run:

```
gofmt -l .
go vet ./...
go test ./...
go run ./cmd/sitegen
```

`gofmt -l .` must print nothing. The site is written to `_site/`, which is not committed.
