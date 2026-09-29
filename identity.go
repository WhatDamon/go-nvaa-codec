package nvaa

// Format identity and registered identifiers.
//
// These live beside the codec so a port has a single place to copy them from.
// The conformance manifest in testdata/ must agree with this file, and
// TestIdentityMatchesManifest fails if they drift apart.

const (
	// FormatName is the human-readable name of the format.
	FormatName = "NeoViolet ASCII-style Animation"

	// Alias is the short four-letter form used in markers and prose.
	Alias = "NVAA"

	// FileExtension is the conventional suffix.
	FileExtension = ".nvaa"

	// MIMEType is the media type.
	MIMEType = "application/vnd.neoviolet.nvaa"

	// UTI is the Apple uniform type identifier.
	UTI = "com.neoviolet.nvaa"
)
