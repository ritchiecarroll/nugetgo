## Mapping

- Module path:
- NuGet package ID:
- Claimed status (canonical, community or withdrawn):
- Source repository:

## Checklist

These are the registry's checks, in the order they are validated. See
[CONTRIBUTING.md](https://github.com/ritchiecarroll/nugetgo/blob/main/CONTRIBUTING.md). Only the schema lint runs automatically today;
a maintainer checks the rest by hand.

**Schema lint**

- [ ] The row has six fields separated by single TAB characters, none empty and none padded with spaces.
- [ ] The module path is exactly as in `go.mod`, including any `/vN` suffix, and has no other row.
- [ ] No other row uses the NuGet package ID, compared case-insensitively.
- [ ] The NuGet package ID is valid. It follows the ID pattern in CONTRIBUTING.md: `nugetgo.` followed by the dotted module path, or the hash-shortened alternate described there, whether the row is `canonical` or `community`. A module conversion does not take the `go.` prefix.
- [ ] The source repository is an `https` URL and the date is `YYYY-MM-DD`.
- [ ] The row is in sorted order by module path.
- [ ] `go run ./cmd/sitegen` passes locally.

**Existence**

- [ ] The module resolves at `proxy.golang.org`.
- [ ] The package ID resolves at nuget.org with at least one non-prerelease version, or the module has only prerelease or pseudo-versions and the prerelease package's description carries the PROOF text.

**Provenance**

- [ ] For `canonical`: the package's repository metadata points to the same code-hosting org as the module path.
- [ ] For `community`: I understand the row is never merged automatically and yields to a canonical row.

**Surface**

- [ ] The package's latest version carries a go2cs self-description naming this module path and a real module version.
- [ ] It was produced by a go2cs release the current toolchain recognizes, and its exported surface matches a fresh go2cs conversion of that module version.
