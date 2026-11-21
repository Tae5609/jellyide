package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is the work factor used for all password hashes.
const bcryptCost = 12

// HashPassword hashes a plaintext password with bcrypt (cost 12).
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword reports whether password matches the bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
