// Package keys embeds the public keys pinned in the pwsgw binary.
//
// Only public keys live here (AGENTS.md rule 5). See README.md in this
// directory for which files exist and when the production key is added.
package keys

import "embed"

// FS holds the *.pub files of this directory.
//
//go:embed *.pub
var FS embed.FS

// CommandSigning is the file name of the production command-signing key.
const CommandSigning = "command-signing.pub"

// CommandSigningTest is the published test key, for tests only.
const CommandSigningTest = "command-signing.test.pub"
