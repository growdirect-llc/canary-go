// Package auth implements the JWT mint + refresh + login surfaces of
// the identity service. T-1 / GRO-848.
//
// The package owns the AtlasView identity contract surfaces 1 (mint)
// and (forthcoming) 3 (WhoAmI). It is the only package that reads
// public.persons / public.person_credentials / public.refresh_tokens.
// All other services consume identity over HTTP — see
// Brain/wiki/cards/platform-identity-database-boundary.md.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidPassword is returned when a presented password does not
// verify against the stored hash. Callers MUST NOT distinguish this
// from "person not found" in their HTTP responses — both should map to
// the same generic 401, otherwise email enumeration is trivial.
var ErrInvalidPassword = errors.New("auth: invalid password")

// ErrMalformedHash is returned when a stored password hash does not
// parse as a recognised argon2id encoding. Treat as a fatal data
// integrity bug — page on it.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// argon2idParams are the hashing parameters T-1 baselines on. The
// values match OWASP's 2024 minimum recommendation for argon2id and
// are tuned to ~50ms on a Cloud Run instance with 1 vCPU. If the
// recommendation moves, bump the constants here and let new logins
// re-hash on next-success.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024 // 19 MiB
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// HashPassword produces an argon2id-encoded hash suitable for storage
// in public.person_credentials.password_hash. The encoding includes
// the parameters and salt so VerifyPassword can reconstruct them
// without a separate parameter table.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt rand: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// PHC-style encoding: $argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword constant-time-compares password against an encoded
// hash. Returns nil on match, ErrInvalidPassword on mismatch,
// ErrMalformedHash on malformed input.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	// Expected: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrMalformedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return ErrMalformedHash
	}
	if version != argon2.Version {
		return ErrMalformedHash
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return ErrMalformedHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrMalformedHash
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrInvalidPassword
	}
	return nil
}
