package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// RevisionPrefix marks a configuration revision as one, so an identifier read
// out of a run record, a listing, or a chat message says what it identifies
// without anybody having to know the shape of a digest.
const RevisionPrefix = "cfg-"

// RevisionDigits is how much of the digest a revision carries. It is enough
// that two configurations a project actually holds never collide, and short
// enough to be quoted in a sentence and matched by eye.
const RevisionDigits = 12

// Revision identifies the configuration in force, as a digest of every
// effective value in it.
//
// It is derived rather than declared for the reason a repository id is derived
// from a product id: a revision an operator maintained by hand is one that stops
// being bumped, and a record naming a revision that did not move is worse than a
// record naming none. Deriving it also makes it say the thing worth saying — two
// runs under the same revision were configured identically, whichever file said
// so, and a bundle upgrade that moves a default moves the revision with it.
//
// It is taken from the effective configuration, after inheritance and defaults,
// because that is what actually governs a run. Two projects whose files differ
// but whose effective values agree share a revision, which is the honest answer:
// nothing about the runs they make differs either.
func (c Config) Revision() string {
	digest := sha256.New()
	// A configuration holds strings, numbers, booleans, and maps and slices of
	// them, so encoding it cannot fail; the encoder sorts map keys, so the same
	// values always digest the same way whichever order they were loaded in.
	encoded, _ := json.Marshal(c)
	digest.Write(encoded)
	// Persona text is excluded from the serialized form on purpose, so that
	// `config show` stays readable, and it is guidance handed to every prompt: a
	// revision that ignored it would call two configurations the same when the
	// agents they run were told different things.
	for _, name := range sortedNames(c.Agents) {
		digest.Write([]byte(name))
		digest.Write([]byte(c.Agents[name].Persona.Text))
	}
	return RevisionPrefix + hex.EncodeToString(digest.Sum(nil))[:RevisionDigits]
}
