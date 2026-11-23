package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// withFastBcrypt swaps the bcrypt entry points to cost-4 implementations
// for the duration of t, so a test does not pay cost-12 per hash/check.
// It also regenerates the unknown-user dummy hash under the fast seam: a
// bcrypt comparison runs at the cost embedded in the hash itself, so the
// cost-12 dummy created in init() would stay slow no matter what the
// entry points are swapped to. Everything is restored by t.Cleanup, which
// makes the swap safe to nest inside subtests (no auth test uses
// t.Parallel, so there is no cross-test contamination). Tests that must
// verify the real cost-12 default (TestHashAndCheckPassword) simply do
// not install the swap.
func withFastBcrypt(t *testing.T) {
	t.Helper()
	const fastCost = 4
	origGen, origCmp := bcryptGen, bcryptCmp
	origDummy := dummyHash
	bcryptGen = func(p []byte) ([]byte, error) {
		return bcrypt.GenerateFromPassword(p, fastCost)
	}
	bcryptCmp = bcrypt.CompareHashAndPassword
	if h, err := bcryptGen([]byte(dummyPassword)); err == nil {
		dummyHash = string(h)
	}
	t.Cleanup(func() {
		bcryptGen = origGen
		bcryptCmp = origCmp
		dummyHash = origDummy
	})
}

// TestFastBcryptSeam pins the seam itself: under the swap HashPassword
// produces a cost-4 hash that still round-trips through CheckPassword, and
// after the subtest's cleanup the cost-12 default is back in place.
func TestFastBcryptSeam(t *testing.T) {
	t.Run("swapped", func(t *testing.T) {
		withFastBcrypt(t)
		hash, err := HashPassword("seam-password")
		if err != nil {
			t.Fatalf("HashPassword: %v", err)
		}
		if !strings.HasPrefix(hash, "$2a$04$") {
			t.Fatalf("expected a cost-4 hash under the seam, got %q", hash)
		}
		if !strings.HasPrefix(dummyHash, "$2a$04$") {
			t.Fatalf("expected a cost-4 dummy hash under the seam, got %q", dummyHash)
		}
		if !CheckPassword(hash, "seam-password") {
			t.Fatal("CheckPassword should accept the right password under the seam")
		}
		if CheckPassword(hash, "wrong-password") {
			t.Fatal("CheckPassword should reject a wrong password under the seam")
		}
	})
	t.Run("restored", func(t *testing.T) {
		// The swap above is cleaned up before this subtest runs, so the
		// default (production) cost-12 path must be active again.
		hash, err := HashPassword("seam-password")
		if err != nil {
			t.Fatalf("HashPassword: %v", err)
		}
		if !strings.HasPrefix(hash, "$2a$12$") {
			t.Fatalf("expected the cost-12 default after cleanup, got %q", hash)
		}
		if !strings.HasPrefix(dummyHash, "$2a$12$") {
			t.Fatalf("expected the cost-12 dummy hash after cleanup, got %q", dummyHash)
		}
		if !CheckPassword(hash, "seam-password") {
			t.Fatal("CheckPassword should accept the right password after cleanup")
		}
	})
}
