// Package auth verifies API credentials and issues the JSON Web Token the API
// accepts.
//
// Passwords are stored as Argon2id hashes in PHC string form, and tokens are
// HS256 JWTs whose subject is the local user id. Nothing in this package trusts
// the claims beyond identity: the caller re-reads the user and the permissions
// from the database on every request.
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

// Argon2id parameters. They follow the RFC 9106 second recommended option:
// 64 MiB, one pass, four lanes.
const (
	argonMemory  = 64 * 1024
	argonTime    = 1
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash means the stored hash is not a PHC Argon2id string.
var ErrInvalidHash = errors.New("auth: the stored password hash is not a valid Argon2id string")

// hashParams are the cost parameters recorded in each hash, so a future change
// can still verify hashes written by an older release.
type hashParams struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

var defaultParams = hashParams{memory: argonMemory, time: argonTime, threads: argonThreads, keyLen: argonKeyLen}

// HashPassword derives an Argon2id hash and returns its PHC encoding. The
// password is never stored or logged.
func HashPassword(password string) (string, error) {
	return hashWithParams(password, defaultParams)
}

func hashWithParams(password string, p hashParams) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC Argon2id hash.
//
// An unusable hash is an error, never a match; callers turn that into a generic
// authentication failure.
func VerifyPassword(hash, password string) (bool, error) {
	p, salt, want, err := parseHash(hash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func parseHash(hash string) (hashParams, []byte, []byte, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return hashParams{}, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return hashParams{}, nil, nil, ErrInvalidHash
	}
	var p hashParams
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &threads); err != nil {
		return hashParams{}, nil, nil, ErrInvalidHash
	}
	if threads < 1 || threads > 255 || p.memory < 8*1024 || p.memory > 1024*1024 || p.time < 1 || p.time > 10 {
		return hashParams{}, nil, nil, ErrInvalidHash
	}
	p.threads = uint8(threads)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return hashParams{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return hashParams{}, nil, nil, ErrInvalidHash
	}
	p.keyLen = uint32(len(key))
	return p, salt, key, nil
}

// dummyHash is a valid hash of a random, unknown password. Verifying against
// it costs the same as verifying a real one, so a request for a username that
// does not exist takes as long as one for a username that does.
var dummyHash = func() string {
	h, err := HashPassword(randomString())
	if err != nil {
		return ""
	}
	return h
}()

// BurnPasswordCheck spends the same work a real verification would, for the
// "no such user" path. Its result is discarded.
func BurnPasswordCheck(password string) {
	if dummyHash == "" {
		return
	}
	_, _ = VerifyPassword(dummyHash, password)
}

func randomString() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return base64.RawStdEncoding.EncodeToString(buf)
}

// HashParamsForTest reports the default cost, so a test can assert that the
// stored string carries it.
func HashParamsForTest() string {
	return fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonTime, argonThreads)
}
