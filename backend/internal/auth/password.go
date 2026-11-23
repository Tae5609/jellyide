package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is the work factor used for all password hashes.
const bcryptCost = 12

// bcryptGen/bcryptCmp are indirections so tests can run at a low cost.
// The defaults are exactly the current cost-12 behaviour.
var (
	bcryptGen = func(p []byte) ([]byte, error) {
		return bcrypt.GenerateFromPassword(p, bcryptCost)
	}
	bcryptCmp = bcrypt.CompareHashAndPassword
)

// HashPassword hashes a plaintext password with bcrypt (cost 12).
func HashPassword(password string) (string, error) {
	b, err := bcryptGen([]byte(password))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword reports whether password matches the bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcryptCmp([]byte(hash), []byte(password)) == nil
}
