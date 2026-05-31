package vault

import "testing"

// TestFingerprintSeededByUniqueIDIsUnique locks in the per-agent uniqueness fix
// (Group G): fingerprints are seeded by a fresh NewID() rather than the display
// name. Two agents/citizens created with the SAME display name therefore no
// longer collide, because each gets a distinct unique-id seed.
func TestFingerprintSeededByUniqueIDIsUnique(t *testing.T) {
	a, err := GenerateFingerprint(NewID())
	if err != nil {
		t.Fatalf("GenerateFingerprint: %v", err)
	}
	b, err := GenerateFingerprint(NewID())
	if err != nil {
		t.Fatalf("GenerateFingerprint: %v", err)
	}
	if a == b {
		t.Fatalf("two unique-id-seeded fingerprints are identical (no per-agent uniqueness):\n%s", a)
	}
	if NewID() == NewID() {
		t.Fatal("NewID() returned duplicate identifiers")
	}
}
