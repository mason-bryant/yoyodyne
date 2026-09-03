package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// DigestPrefix marks a workflow digest as one, so an identifier read out of an
// instance record, a listing, or a chat message says what it identifies without
// anybody having to know the shape of a digest.
const DigestPrefix = "wf-"

// Canonical is the exact bytes a definition's digest is taken over: the
// definition as JSON, with its fields in the order the schema declares them and
// its maps in sorted key order, and with nothing of the file it was read from —
// no comments, no key order, no quoting, no whitespace.
//
// It is exported because a digest nobody can reproduce is a number people have
// to trust. Two definitions with the same canonical form are the same workflow
// however differently they were written down, and that is a claim somebody can
// check by looking at these bytes rather than by re-deriving the hash.
func (v Validated) Canonical() []byte {
	return canonical(v.definition)
}

// Digest is the content address of this definition: the same definition digests
// the same way in every build, and any change to what it says changes it.
//
// An instance pins itself to this before its first action and keeps running the
// definition it pinned even after the file changes underneath it, so the digest
// carries the whole hash rather than a readable prefix of one. It is compared and
// stored far more often than it is read aloud, and a truncation that made it
// quotable would be a collision the pin cannot detect.
func (v Validated) Digest() string {
	return v.digest
}

// canonical encodes a definition into the bytes its digest is taken over.
//
// A definition holds strings, an integer, and maps of them, so encoding it
// cannot fail and the error is not worth propagating into every caller of
// Validate; the encoder sorts map keys, which is what makes two definitions that
// declared their states in different orders digest identically.
func canonical(definition Definition) []byte {
	encoded, _ := json.Marshal(definition)
	return encoded
}

// digestOf is the digest of a definition's canonical form.
func digestOf(definition Definition) string {
	sum := sha256.Sum256(canonical(definition))
	return DigestPrefix + hex.EncodeToString(sum[:])
}
