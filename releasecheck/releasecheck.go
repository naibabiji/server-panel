// Package releasecheck holds the release trust root and version rules shared by
// the panel updater and the Agent self-updater. It must stay dependency-free
// so the Agent binary does not pull in panel packages (SQLite, Gin, ...).
package releasecheck

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// pubKeyHex verifies the detached signature published alongside each
// release's checksums file. The matching private key is kept locally and is
// never committed to this repository.
const pubKeyHex = "be74aa38e024baa117156f62c9714961f7e7d1aaa36138c527b6bdf1544e9da0"

var (
	// ErrSignatureFormat means the .sig file is not base64 text.
	ErrSignatureFormat = errors.New("release signature file is not valid base64")
	// ErrSignatureMismatch means the signature does not verify against the
	// embedded release key.
	ErrSignatureMismatch = errors.New("release signature verification failed")
)

// VerifySignatureFile verifies a published checksums-<arch>.txt.sig file
// over message using the embedded release public key. The file format is the
// one tools/sign-checksums writes: the base64 (StdEncoding) Ed25519 signature,
// optionally surrounded by whitespace. Panel and Agent updaters must both go
// through this so they can never disagree about the format.
func VerifySignatureFile(message, sigFile []byte) error {
	pub, err := hex.DecodeString(pubKeyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ErrSignatureMismatch
	}
	return verifySignatureFile(ed25519.PublicKey(pub), message, sigFile)
}

func verifySignatureFile(pub ed25519.PublicKey, message, sigFile []byte) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigFile)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureFormat, err)
	}
	if !ed25519.Verify(pub, message, sig) {
		return ErrSignatureMismatch
	}
	return nil
}

// CompareVersions compares two vX.Y.Z[-prerelease] tags. Returns 1 if a > b,
// -1 if a < b, 0 if equal.
func CompareVersions(a, b string) int {
	majA, minA, patA, preA := parseVersion(a)
	majB, minB, patB, preB := parseVersion(b)

	if c := compareInt(majA, majB); c != 0 {
		return c
	}
	if c := compareInt(minA, minB); c != 0 {
		return c
	}
	if c := compareInt(patA, patB); c != 0 {
		return c
	}
	return comparePrerelease(preA, preB)
}

// IsStableVersion reports whether tag is a plain vX.Y.Z with no prerelease
// suffix. Only stable versions are eligible for auto-update.
func IsStableVersion(tag string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if strings.Contains(v, "-") {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// IsPatchBump reports whether latest is a patch-level increment over current
// (same major.minor, higher patch only).
func IsPatchBump(current, latest string) bool {
	curMaj, curMin, curPat, _ := parseVersion(current)
	latMaj, latMin, latPat, _ := parseVersion(latest)
	return curMaj == latMaj && curMin == latMin && latPat > curPat
}

func parseVersion(v string) (major, minor, patch int, prerelease string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.Index(v, "-"); idx >= 0 {
		prerelease = v[idx+1:]
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	major = atoiSafeVersionPart(parts, 0)
	minor = atoiSafeVersionPart(parts, 1)
	patch = atoiSafeVersionPart(parts, 2)
	return
}

func atoiSafeVersionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, _ := strconv.Atoi(parts[i])
	return n
}

func compareInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

// comparePrerelease implements semver 2.0 precedence rules: a version
// without a prerelease suffix outranks the same version with one; otherwise
// prerelease identifiers are compared dot-segment by dot-segment (numeric
// identifiers compared numerically, alphanumeric compared lexically, numeric
// always ranks below alphanumeric).
func comparePrerelease(a, b string) int {
	if a == "" && b == "" {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	for i := 0; i < len(partsA) || i < len(partsB); i++ {
		if i >= len(partsA) {
			return -1
		}
		if i >= len(partsB) {
			return 1
		}
		if c := comparePrereleaseIdentifier(partsA[i], partsB[i]); c != 0 {
			return c
		}
	}
	return 0
}

func comparePrereleaseIdentifier(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return compareInt(na, nb)
	}
	if errA == nil {
		return -1
	}
	if errB == nil {
		return 1
	}
	return strings.Compare(a, b)
}

// ChecksumFor parses `sha256sum`-style output ("<hash>  <filename>"
// per line) and returns the hash for filename. The checksums file covers
// multiple binaries (panel + agent), so matching by filename is required.
func ChecksumFor(checksumsContent, filename string) (string, error) {
	for _, line := range strings.Split(checksumsContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("校验文件中未找到 %s 的哈希", filename)
}
