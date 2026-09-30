# nugetgo

nugetgo is a registry that maps Go module paths to the NuGet package IDs of their
[go2cs](https://github.com/ritchiecarroll/go2cs) C# conversions. go2cs transpiles Go to C#; with a
mapping, a converted program can reference a dependency as a published NuGet package instead of
converting it again. go2cs does not read the registry yet; that support is planned.

The registry is one plain text file, served at:

```
https://nugetgo.net/v1/mappings.txt
```

The site at [nugetgo.net](https://nugetgo.net/) is generated from that file and lists its mappings.
The file is the single source of truth.

## Status

No mappings are registered yet.

## Schema v1

One mapping per line, six TAB-separated fields, `#` for comments:

```
module-path	nuget-id	status	source-repo	registered	contact
```

- `module-path`: the Go module path as in `go.mod`, including any `/vN` suffix; one row per major version.
- `nuget-id`: the NuGet package ID of the conversion.
- `status`: `canonical` (the package's repository metadata points to the module's own org),
  `community` (a third-party conversion), or `withdrawn` (kept for the record, treated as unmapped).
- `source-repo`, `registered`, `contact`: provenance for people.

The file maps names only. Package versions are not listed; each package describes the Go module
version it was converted from.

## Contributing

Mappings are added by pull request. [CONTRIBUTING.md](CONTRIBUTING.md) has the trust policy, the
NuGet ID convention and the checks every row goes through.

## Non-goals

nugetgo is not a package host (nuget.org hosts the packages) and not a build service. It maps
names and checks provenance; it makes no claim about package content.

## License

The data and the site generator are dedicated to the public domain under [CC0-1.0](LICENSE).
