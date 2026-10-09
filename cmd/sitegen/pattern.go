package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// The NuGet package ID pattern of a converted Go module, mirrored from go2cs's pack, which is the one
// place a module's ID is minted (go2cs src/tools/nugetgo/NugetgoIdentity.psm1, Get-NugetgoPackageId).
// The schema lint does not require it: the nuget-id column is free-form apart from the go. refusal, so
// an ID that follows neither form below is a warning, never a failure.

// idPrefix is NugetgoIdentity.psm1 line 25: $script:IdPrefix = 'nugetgo.'
const idPrefix = "nugetgo."

// hashDigits is NugetgoIdentity.psm1 line 26: $script:HashDigits = 8
const hashDigits = 8

var (
	// NugetgoIdentity.psm1 line 84: [regex]::Replace($natural, '[^A-Za-z0-9_.-]', '-')
	stemCharRe = regexp.MustCompile(`[^A-Za-z0-9_.-]`)
	// NugetgoIdentity.psm1 line 85: [regex]::Replace($stem, '([_.-])[_.-]+', '$1')
	stemRunRe = regexp.MustCompile(`([_.-])[_.-]+`)
)

// naturalID is NugetgoIdentity.psm1 line 60: 'nugetgo.' + the module path with every '/' replaced by '.'.
func naturalID(modulePath string) string {
	return idPrefix + strings.ReplaceAll(modulePath, "/", ".")
}

// alternateID is the hash-shortened alternate, NugetgoIdentity.psm1 lines 76-93: the natural ID's stem,
// cut at a separator boundary so that stem + '.' + the first 8 hex digits of SHA-256 over the exact
// module path fits in 100 characters.
func alternateID(modulePath string) string {
	sum := sha256.Sum256([]byte(modulePath))
	hash := hex.EncodeToString(sum[:hashDigits/2])
	stem := stemCharRe.ReplaceAllString(naturalID(modulePath), "-")
	stem = strings.Trim(stemRunRe.ReplaceAllString(stem, "$1"), ".-_")
	room := maxNuGetIDLength - (hashDigits + 1)
	if len(stem) > room {
		cut := stem[:room+1] // one past the room, so a separator AT the boundary counts
		if at := strings.LastIndexAny(cut, "._-"); at > 0 {
			stem = cut[:at]
		} else {
			stem = stem[:room]
		}
		stem = strings.TrimRight(stem, ".-_")
	}
	return stem + "." + hash
}

// patternWarning returns "" when the row's NuGet ID is the module's natural ID or its hash-shortened
// alternate, compared case-insensitively as NuGet compares IDs, and a warning otherwise.
func patternWarning(row Row) string {
	id := strings.ToLower(row.NuGetID)
	natural, alternate := naturalID(row.ModulePath), alternateID(row.ModulePath)
	if id == strings.ToLower(natural) || id == strings.ToLower(alternate) {
		return ""
	}
	return fmt.Sprintf("nuget-id %q does not follow the ID pattern for %s: %q, or its hash-shortened alternate %q (see CONTRIBUTING.md, NuGet package IDs); the registry accepts it, the row is the authority", row.NuGetID, row.ModulePath, natural, alternate)
}
