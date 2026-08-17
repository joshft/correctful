package harvest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joshft/correctful/schema"
)

// realAlloyFixture is shaped verbatim after a real Alloy lifecycle model
// (captured 2026-08-16) — the assert-block form, the multi-line check command
// with its scope list, the run witness, and both Alloy comment styles are
// preserved exactly; identifiers are anonymized per AGENTS.md. The orphan
// assert and commented decoys are synthetic additions, because the real model
// had no unchecked assert or commented-out declaration to pin those behaviors
// against.
const realAlloyFixture = `assert GuardNeverWidensAccess {
  all s: Snapshot - trace/last |
    let n = trace/next[s] |
      n.event = LockEvent implies
        all w: Task | access[n, w] in access[s, w]
}

check GuardNeverWidensAccess for 12 but exactly 12 Snapshot,
  3 Task, 2 Identity, 5 PolicyVersion, 4 Resource, 6 Epoch,
  5 Generation

run ExerciseProtocol for exactly 16 Snapshot, exactly 2 Task,
  2 Identity, 6 PolicyVersion, 4 Resource, 8 Epoch, 6 Generation

-- synthetic additions --
assert OrphanNeverChecked {
  all s: Snapshot | some s
}
// assert CommentedDecoy {
-- check CommentedDecoy for 4
/* assert BlockCommentDecoy {
check BlockCommentDecoy for 4 */
`

// TestAlloyHarvesterVerbatimFixture: the checked assert binds an alloy-check
// probe; the run command becomes a witness claim; the orphan assert has NO
// probe (asserted but never checked — remainder-bound); commented declarations
// in either comment style mint nothing.
func TestAlloyHarvesterVerbatimFixture(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.als"), []byte(realAlloyFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := AlloyHarvester{}.Harvest(dir, []string{"model.als"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]schema.Claim{}
	for _, c := range res.Claims {
		byID[c.ID] = c
	}
	if len(res.Claims) != 3 {
		t.Fatalf("claims = %d (%v), want 3 (checked assert, orphan assert, run witness)", len(res.Claims), res.Claims)
	}

	checked := byID["GuardNeverWidensAccess"]
	if checked.Shape != schema.ShapeSafetyAssert {
		t.Errorf("checked assert shape = %q", checked.Shape)
	}
	if len(checked.ProbeIDs) != 1 || checked.ProbeIDs[0] != schema.AlloyCheckProbeID("model.als", "GuardNeverWidensAccess") {
		t.Errorf("checked assert probes = %v", checked.ProbeIDs)
	}

	orphan := byID["OrphanNeverChecked"]
	if orphan.ID == "" {
		t.Fatal("orphan assert not harvested")
	}
	if len(orphan.ProbeIDs) != 0 {
		t.Errorf("orphan assert has probes %v — an unchecked assert must be remainder-bound", orphan.ProbeIDs)
	}

	witness := byID["ExerciseProtocol"]
	if witness.Shape != schema.ShapeWitness {
		t.Errorf("run witness shape = %q, want witness", witness.Shape)
	}
	if len(witness.ProbeIDs) != 1 {
		t.Errorf("run witness probes = %v", witness.ProbeIDs)
	}

	for _, decoy := range []string{"CommentedDecoy", "BlockCommentDecoy"} {
		if _, minted := byID[decoy]; minted {
			t.Errorf("commented-out declaration %q minted a claim", decoy)
		}
	}
}
