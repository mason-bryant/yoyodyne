// Package conformance is the one suite every provider adapter passes on refusal
// classification.
//
// Refusal vocabulary is where adapters diverge invisibly. Each of them reads its
// own provider's words, and nothing above them can see that two have read the
// same condition differently: a provider that says "capacity" in a sentence
// rather than on an event of its own, or reports a logged-out account in prose
// rather than as a status, produces a run that waits where the other fails, or
// one that relaunches forever into an answer no relaunch can change. Every
// failover and retry decision downstream reads a classification, so two adapters
// meaning different things by the same word is a defect nothing else here
// catches — each adapter's own tests assert what its own provider says, and
// agreeing with the adapter beside it is precisely what neither of them states.
//
// So the conditions are named once, in terms no provider owns, and each adapter
// supplies its own provider's words for each of them. What the suite asserts is
// that the harness is left holding the same answer whichever adapter met the
// condition. It asserts it against the adapter rather than against the dialect,
// because a classification only reaches a caller through the whole path an
// invocation takes: the stream is parsed, the dialect answers, and the contract
// records the answer onto the result. An adapter that reads a condition
// correctly and drops it on the way to the result is the same failure to
// everything downstream.
//
// A new adapter cannot be declared complete without passing it. Uncovered names
// every adapter this build ships that a set of cases says nothing about, so an
// adapter added to the registry with no cases beside it arrives as a failing
// test naming what it owes rather than as a provider nobody ever asked how it
// reads a refusal.
//
// The suite is test code and this file is the whole of the package's compiled
// content, which is deliberate: the provider a sample is put through is a
// fabricated one, and the repository's sweep for places that ask a provider for
// work reads a request built outside a test as a call site the harness makes.
// This one is a double exercising the adapters rather than a sixth invocation,
// so it lives where the sweep already accounts for doubles.
package conformance
