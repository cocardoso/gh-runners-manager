package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

// Params are argon2id cost parameters.
type Params struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

var (
	// DefaultParams follow the RFC 9106 second recommendation (64 MiB, 3 passes).
	DefaultParams = Params{MemoryKiB: 64 * 1024, Time: 3, Threads: 2}
	// TestParams are cheap, for tests only.
	TestParams = Params{MemoryKiB: 64, Time: 1, Threads: 1}
)

const (
	saltLen = 16
	keyLen  = 32
)

var b64 = base64.RawStdEncoding

// maxConcurrentHashes bounds the argon2id computations running at once: each takes
// MemoryKiB of memory, so a burst of sign-ins could otherwise exhaust it.
const maxConcurrentHashes = 2

var (
	hashSlots             = make(chan struct{}, maxConcurrentHashes)
	hashesNow, hashesPeak atomic.Int32
)

func idKey(password string, salt []byte, p Params, n uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	if now := hashesNow.Add(1); now > hashesPeak.Load() {
		hashesPeak.Store(now)
	}
	defer hashesNow.Add(-1)
	return argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, n)
}

// hashPeak reports the most argon2id computations seen at once (tests).
func hashPeak() int { return int(hashesPeak.Load()) }

// HashPassword returns the PHC string of an argon2id hash with a random salt.
func HashPassword(password string, p Params) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := idKey(password, salt, p, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC string written by HashPassword.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("auth: not an argon2id hash")
	}
	var version int
	var p Params
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("auth: unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Time, &p.Threads); err != nil {
		return false, fmt.Errorf("auth: argon2 parameters: %w", err)
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := idKey(password, salt, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
