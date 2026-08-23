package notify

import (
	"strings"
	"testing"
)

// A reply's mark is worth reading only while a reader already knows every mark
// they can see, so the set is three words with three symbols and nothing else.
// None of them may be a severity's mark, and none may be a status's: a status is
// about the item and sits on a thread's opener, a receipt is about one reply and
// sits inside the thread, and a reader must never have to work out which of the
// two a symbol is talking about.
func TestTheReceiptVocabularyIsThreeWordsAndThreeDistinctSymbols(t *testing.T) {
	t.Parallel()

	receipts := Receipts()
	if len(receipts) != 3 {
		t.Fatalf("Receipts() = %v, want the three a reply can wear", receipts)
	}
	seen := map[string]Receipt{}
	for _, receipt := range receipts {
		if !receipt.Valid() {
			t.Fatalf("Receipts() offered %q, which nothing recognizes", receipt)
		}
		symbol := receipt.Symbol()
		if symbol == "" {
			t.Fatalf("%s has no symbol, so nothing could mark a reply with it", receipt)
		}
		if other, found := seen[symbol]; found {
			t.Fatalf("%s and %s are both marked %q, so a reader cannot tell them apart", other, receipt, symbol)
		}
		seen[symbol] = receipt
		for _, mark := range []string{criticalMark, warningMark} {
			if strings.Contains(mark, symbol) {
				t.Fatalf("%s is marked %q, which is also how a severity is said", receipt, symbol)
			}
		}
	}
	if Receipt("acknowledged").Valid() || Receipt("acknowledged").Symbol() != "" {
		t.Fatal("a word nobody decided on is a receipt, so the set is not fixed")
	}
}

// The two vocabularies live on different messages, so they may share the check
// mark — and nothing else. A second symbol in common would be a reader guessing
// which of the two a mark meant on a channel where both are in view.
func TestOnlyTheSettledMarkIsSharedWithAStatus(t *testing.T) {
	t.Parallel()

	statuses := map[string]Status{}
	for _, status := range Statuses() {
		statuses[status.Symbol()] = status
	}
	for _, receipt := range Receipts() {
		status, shared := statuses[receipt.Symbol()]
		if !shared {
			continue
		}
		if receipt != ReceiptSettled || status != StatusCompleted {
			t.Fatalf("%s and %s share %q, which is a symbol with two meanings", receipt, status, receipt.Symbol())
		}
	}
}
