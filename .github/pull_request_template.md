## Mapping

- Module path:
- NuGet package ID:
- Claimed status (canonical, community or withdrawn):
- Source repository:

## Checklist

These are the registry's checks, in the order they are validated. See
[CONTRIBUTING.md](https://github.com/ritchiecarroll/nugetgo/blob/main/CONTRIBUTING.md). The `ci`
workflow runs checks 1 to 4(b) and the label; a maintainer runs check 4(c). Checks 2 to 4 run on the
rows this pull request adds or changes, and not on a `withdrawn` row.

**1. Schema lint**

- [ ] The row has six fields separated by single TAB characters, none empty and none padded with spaces.
- [ ] The module path is exactly as in `go.mod`, including any `/vN` suffix, and has no other row.
- [ ] No other row uses the NuGet package ID, compared case-insensitively.
- [ ] The NuGet package ID is valid and does not start with `go.`, the prefix of the converted Go standard library; the lint refuses that prefix in any letter case.
- [ ] The NuGet package ID follows the ID pattern in CONTRIBUTING.md: `nugetgo.` followed by the dotted module path, or the hash-shortened alternate described there, whether the row is `canonical` or `community`. Another ID draws a warning, not a failure.
- [ ] The source repository is an `https` URL and the date is `YYYY-MM-DD`.
- [ ] The row is in sorted order by module path.
- [ ] `go run ./cmd/sitegen` passes locally.

**2. Existence**

- [ ] The module resolves at `proxy.golang.org`.
- [ ] The package ID resolves at nuget.org with at least one listed non-prerelease version, or the module has only prerelease or pseudo-versions and a prerelease package's description opens with the PROOF text naming this module and a Go version the proxy resolves.

**3. Provenance**

- [ ] For `canonical`: the `RepositoryUrl` of the package's latest version is an `https` URL under the same org on the same host as the module path (`github.com`, `gitlab.com` or `bitbucket.org`). The check fails a canonical claim it does not confirm.
- [ ] For `community`: I understand the row is reported, never merged without review, and yields to a canonical row.

**4. Surface**

- [ ] (a) The package's latest version carries `go2cs/source-metadata.txt` naming this module path and a module version that resolves at `proxy.golang.org`.
- [ ] (b) Its `go2cs-release` is listed in `cmd/sitegen/go2cs-releases.txt`.
- [ ] (c) Its exported surface matches a fresh go2cs conversion of that module version. A maintainer runs this check.

**5. Label**

- [ ] I understand `auto-merge-eligible` is set only when every automated check passes, every added or changed row is canonical by validation and the pull request changes only `v1/mappings.txt`, and that a maintainer merges every pull request.
- [ ] `go run ./cmd/sitegen -check base-mappings.txt`, with the base file taken from `main`, passes locally.
