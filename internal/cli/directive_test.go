package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The default listing is the one place an operator asks what direction still
// applies, and an operational directive somebody carried out still applies: it
// took effect when it was recorded, it held nothing up, and recording what came
// of it says what it produced rather than withdrawing it.
//
// This is the regression the outcome half could have introduced. Before an
// operational directive could carry an outcome at all it was permanently
// unsettled, so filtering this listing on "nothing has been recorded about it"
// happened to be right; the moment one could be settled, that filter would drop
// a standing instruction out of the listing the operator reads to find it. The
// directive that pauses work is the only one settling takes out of force, and
// this holds both halves apart at the surface.
func TestTheDefaultDirectiveListingKeepsAStandingInstructionAfterItIsCarriedOut(t *testing.T) {
	// Not parallel: the state root the store resolves is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	standing := recordTestDirective(t, parts.directives, parts.config.Product.ID, directive.Directive{
		Kind: directive.KindOperational,
		Text: "stop opening pull requests for documentation-only changes",
	})
	lifted := recordTestDirective(t, parts.directives, parts.config.Product.ID, directive.Directive{
		Kind:       directive.KindAmbiguous,
		Text:       "do publishing differently",
		Unresolved: "which of the two publishing behaviours was meant",
	})

	if _, err := parts.directives.CarryOut(standing.ID, "admitted yoyodyne-ifd.170 to the backlog: make it configurable", time.Now()); err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	if _, err := parts.directives.Resolve(lifted.ID, "the second behaviour", time.Now()); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	var stdout, stderr strings.Builder
	if code := runDirective([]string{"list", "--config", configPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("directive list exited %d: %s", code, stderr.String())
	}
	listed := stdout.String()
	// Still in force, and now saying what it produced as well.
	if !strings.Contains(listed, standing.ID) {
		t.Fatalf("listing = %q, want the carried-out instruction still listed as in force", listed)
	}
	if !strings.Contains(listed, "carried out") || !strings.Contains(listed, "yoyodyne-ifd.170") {
		t.Fatalf("listing = %q, want it to say what the instruction produced", listed)
	}
	// The pause it lifted is over, so it is not what the operator is shown by
	// default.
	if strings.Contains(listed, lifted.ID) {
		t.Fatalf("listing = %q, want a resolved pause out of the default listing", listed)
	}
	// And --all is still the whole record, both of them.
	var everything strings.Builder
	if code := runDirective([]string{"list", "--all", "--config", configPath}, &everything, &stderr); code != 0 {
		t.Fatalf("directive list --all exited %d: %s", code, stderr.String())
	}
	for _, wanted := range []string{standing.ID, lifted.ID} {
		if !strings.Contains(everything.String(), wanted) {
			t.Fatalf("listing = %q, want every recorded directive", everything.String())
		}
	}
}

// recordTestDirective stamps what the harness supplies onto a directive and
// makes it durable, so a test states only the part it is about.
func recordTestDirective(t *testing.T, store directiveRecorder, productID domain.ProductID, said directive.Directive) directive.Directive {
	t.Helper()
	id, err := directive.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	recorded := said
	recorded.SchemaVersion = directive.SchemaVersion
	recorded.ID = id
	recorded.ProductID = productID
	recorded.ReceivedBy = domain.RoleProductManager
	recorded.ReceivedAt = time.Now().UTC()
	if err := store.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return recorded
}

// directiveRecorder is the one method these tests need from the store, named so
// the helper says what it uses rather than taking the whole store.
type directiveRecorder interface {
	Record(recorded directive.Directive) error
}
