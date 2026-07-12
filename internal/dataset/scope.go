package dataset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	authScopeFingerprintVersion = "loom-auth-scope-v1"
	fingerprintAlgorithmSHA256  = "sha256"
)

// AuthScopeMode distinguishes an unscoped caller from a caller with a scoped
// authorization result. In particular, a restricted caller with zero allowed
// paths must not share a cache key with an unrestricted caller.
type AuthScopeMode string

const (
	AuthScopeUnrestricted AuthScopeMode = "unrestricted"
	AuthScopeRestricted   AuthScopeMode = "restricted"
)

// AuthScopeFingerprint is a stable, serializable representation of an
// effective authorization scope. It intentionally retains no raw paths, token,
// subject, or claims. Callers must construct it from the already-authorized,
// canonical effective scope returned by the authorization layer.
type AuthScopeFingerprint struct {
	mode      AuthScopeMode
	algorithm string
	digest    string
}

// UnrestrictedAuthScopeFingerprint returns the canonical fingerprint for an
// authorization layer that is explicitly unrestricted.
func UnrestrictedAuthScopeFingerprint() AuthScopeFingerprint {
	return AuthScopeFingerprint{
		mode:      AuthScopeUnrestricted,
		algorithm: fingerprintAlgorithmSHA256,
		digest:    fingerprintDigest(AuthScopeUnrestricted, nil),
	}
}

// RestrictedAuthScopeFingerprint returns a fingerprint for one effective,
// restricted scope. Input paths must already be canonical authorization-path
// identifiers. Ordering does not matter; duplicate or noncanonical values are
// rejected so two integrations cannot silently hash different representations
// of the same scope.
func RestrictedAuthScopeFingerprint(paths []string) (AuthScopeFingerprint, error) {
	canonicalPaths, err := canonicalScopePaths(paths)
	if err != nil {
		return AuthScopeFingerprint{}, err
	}
	return AuthScopeFingerprint{
		mode:      AuthScopeRestricted,
		algorithm: fingerprintAlgorithmSHA256,
		digest:    fingerprintDigest(AuthScopeRestricted, canonicalPaths),
	}, nil
}

// Mode returns whether the fingerprint represents an unrestricted or
// restricted authorization result.
func (f AuthScopeFingerprint) Mode() AuthScopeMode { return f.mode }

// Algorithm returns the digest algorithm used for this fingerprint.
func (f AuthScopeFingerprint) Algorithm() string { return f.algorithm }

// Digest returns the lower-case hexadecimal digest. It never exposes raw
// authorization paths.
func (f AuthScopeFingerprint) Digest() string { return f.digest }

// Equal reports exact cache-key equality.
func (f AuthScopeFingerprint) Equal(other AuthScopeFingerprint) bool {
	return f.mode == other.mode && f.algorithm == other.algorithm && f.digest == other.digest
}

// Validate checks a persisted fingerprint. It cannot recompute a digest
// without the intentionally omitted raw scope paths.
func (f AuthScopeFingerprint) Validate() error {
	if f.mode != AuthScopeUnrestricted && f.mode != AuthScopeRestricted {
		return fmt.Errorf("%w: mode must be unrestricted or restricted", ErrInvalidAuthScopeFingerprint)
	}
	if f.algorithm != fingerprintAlgorithmSHA256 {
		return fmt.Errorf("%w: algorithm must be %q", ErrInvalidAuthScopeFingerprint, fingerprintAlgorithmSHA256)
	}
	if len(f.digest) != sha256.Size*2 || strings.ToLower(f.digest) != f.digest {
		return fmt.Errorf("%w: digest must be a lower-case SHA-256 hexadecimal value", ErrInvalidAuthScopeFingerprint)
	}
	if _, err := hex.DecodeString(f.digest); err != nil {
		return fmt.Errorf("%w: digest is not hexadecimal: %v", ErrInvalidAuthScopeFingerprint, err)
	}
	return nil
}

func canonicalScopePaths(paths []string) ([]string, error) {
	canonical := append([]string(nil), paths...)
	if len(canonical) > maxScopePaths {
		return nil, fmt.Errorf("%w: scope has more than %d paths", ErrInvalidAuthScopeFingerprint, maxScopePaths)
	}
	for index, path := range canonical {
		if err := validateOpaqueIdentifier(fmt.Sprintf("scope path at index %d", index), path, false); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidAuthScopeFingerprint, err)
		}
	}
	sort.Strings(canonical)
	for index := 1; index < len(canonical); index++ {
		if canonical[index] == canonical[index-1] {
			return nil, fmt.Errorf("%w: duplicate scope path", ErrInvalidAuthScopeFingerprint)
		}
	}
	return canonical, nil
}

func fingerprintDigest(mode AuthScopeMode, canonicalPaths []string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(authScopeFingerprintVersion))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(mode))
	for _, path := range canonicalPaths {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(path))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (f AuthScopeFingerprint) MarshalJSON() ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(authScopeFingerprintWire{
		Mode:      f.mode,
		Algorithm: f.algorithm,
		Digest:    f.digest,
	})
}

func (f *AuthScopeFingerprint) UnmarshalJSON(data []byte) error {
	if f == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil AuthScopeFingerprint", ErrInvalidAuthScopeFingerprint)
	}
	var decoded authScopeFingerprintWire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidAuthScopeFingerprint, err)
	}
	value := AuthScopeFingerprint{
		mode:      decoded.Mode,
		algorithm: decoded.Algorithm,
		digest:    decoded.Digest,
	}
	if err := value.Validate(); err != nil {
		return err
	}
	*f = value
	return nil
}

type authScopeFingerprintWire struct {
	Mode      AuthScopeMode `json:"mode"`
	Algorithm string        `json:"algorithm"`
	Digest    string        `json:"digest"`
}
