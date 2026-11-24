package token

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func keyFile(t *testing.T, name string, pemBytes []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, pemBytes, mode); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// generatedKey writes a freshly generated key of alg to a file only its owner
// can read and returns its path.
func generatedKey(t *testing.T, alg string) string {
	t.Helper()
	pemBytes, err := GenerateKey(alg)
	if err != nil {
		t.Fatalf("GenerateKey(%s): %v", alg, err)
	}
	return keyFile(t, alg+".pem", pemBytes, 0o600)
}

// TestALoadedSigningKeyBecomesTheSigningKey checks that a loaded signing key
// becomes the signing key.
func TestALoadedSigningKeyBecomesTheSigningKey(t *testing.T) {
	path := generatedKey(t, algES256)
	set, diags, err := loadKeySet(resolved{algorithm: algES256, signingFile: path})
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("a key file only its owner can read produced %d cautions", len(diags))
	}
	if len(set.entries) != 1 || set.signing == nil {
		t.Fatalf("the set holds %d entries and signing %v", len(set.entries), set.signing != nil)
	}
	if set.signing.alg != algES256 {
		t.Errorf("the signing key is %s, want %s", set.signing.alg, algES256)
	}
	if set.signing.kid == "" {
		t.Error("the signing key has no kid")
	}
	if len(set.algorithms) != 1 || set.algorithms[0] != algES256 {
		t.Errorf("the allow-list is %v, want [%s]", set.algorithms, algES256)
	}
}

// TestAVerificationKeyFileDoesNotBecomeASigningKey checks that a verification
// key file does not become a signing key.
func TestAVerificationKeyFileDoesNotBecomeASigningKey(t *testing.T) {
	path := generatedKey(t, algES256)
	set, _, err := loadKeySet(resolved{algorithm: algES256, verifyFiles: []string{path}})
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if set.signing != nil {
		t.Error("a verification key file made a signing key, and the module would issue with it")
	}
	if len(set.entries) != 1 {
		t.Fatalf("the set holds %d entries, want 1", len(set.entries))
	}
}

// TestNoKeyAtAllIsRefused checks that no key at all is refused.
func TestNoKeyAtAllIsRefused(t *testing.T) {
	_, _, err := loadKeySet(resolved{algorithm: algES256})
	if !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("loading with no key gave %v, want ErrInvalidKey", err)
	}
}

// TestADuplicateKidIsRefusedAndNamesTheFile checks that a duplicate kid is
// refused and names the file.
func TestADuplicateKidIsRefusedAndNamesTheFile(t *testing.T) {
	pemBytes, err := GenerateKey(algES256)
	if err != nil {
		t.Fatal(err)
	}
	first := keyFile(t, "first.pem", pemBytes, 0o600)
	second := keyFile(t, "second.pem", pemBytes, 0o600)
	_, _, err = loadKeySet(resolved{algorithm: algES256, signingFile: first, verifyFiles: []string{second}})
	if !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("loading two copies of one key gave %v, want ErrInvalidKey", err)
	}
	if !strings.Contains(err.Error(), "second.pem") {
		t.Errorf("the refusal %q does not name the second file", err)
	}
}

// TestAnRSAKeyBelowTheFloorIsRefused checks that an rsa key below the floor
// is refused.
func TestAnRSAKeyBelowTheFloorIsRefused(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	path := keyFile(t, "small.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	_, _, err = loadKeySet(resolved{algorithm: algRS256, signingFile: path})
	if !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("loading a 1024-bit RSA key gave %v, want ErrInvalidKey", err)
	}
	if !strings.Contains(err.Error(), "1024") {
		t.Errorf("the refusal %q does not say the size", err)
	}
}

// TestAKeyOfTheWrongAlgorithmIsRefused checks that a key of the wrong
// algorithm is refused.
func TestAKeyOfTheWrongAlgorithmIsRefused(t *testing.T) {
	path := generatedKey(t, algES256)
	_, _, err := loadKeySet(resolved{algorithm: algRS256, signingFile: path})
	if !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("an ES256 key under RS256 gave %v, want ErrInvalidKey", err)
	}
	if !strings.Contains(err.Error(), algES256) {
		t.Errorf("the refusal %q does not name the key's algorithm", err)
	}
}

// TestAPublicKeyFileWhereASigningKeyIsRequiredIsRefused checks that a public
// key file where a signing key is required is refused.
func TestAPublicKeyFileWhereASigningKeyIsRequiredIsRefused(t *testing.T) {
	path := generatedKey(t, algES256)
	set, _, err := loadKeySet(resolved{algorithm: algES256, verifyFiles: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	pub := set.entries[0].pub
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := keyFile(t, "pub.pem", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)
	_, _, err = loadKeySet(resolved{algorithm: algES256, signingFile: pubPath})
	if !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("a public key as the signing key file gave %v, want ErrInvalidKey", err)
	}
}

// TestAPrivateKeyOtherUsersCanReadIsWarnedNotFatal checks that a private key
// other users can read is warned not fatal.
func TestAPrivateKeyOtherUsersCanReadIsWarnedNotFatal(t *testing.T) {
	pemBytes, err := GenerateKey(algES256)
	if err != nil {
		t.Fatal(err)
	}
	open := keyFile(t, "open.pem", pemBytes, 0o644)
	set, diags, err := loadKeySet(resolved{algorithm: algES256, signingFile: open})
	if err != nil {
		t.Fatalf("a world-readable private key stopped the startup: %v", err)
	}
	if set.signing == nil {
		t.Fatal("the key was not taken as the signing key")
	}
	if len(diags) != 1 {
		t.Fatalf("the caution count is %d, want 1", len(diags))
	}
	if diags[0].file != open {
		t.Errorf("the caution names %q, want %q", diags[0].file, open)
	}
}

// checks that GenerateKey round-trips every supported algorithm.
func TestGenerateKeyRoundTripsForEverySupportedAlgorithm(t *testing.T) {
	for _, alg := range []string{algES256, algRS256} {
		t.Run(alg, func(t *testing.T) {
			path := generatedKey(t, alg)
			set, _, err := loadKeySet(resolved{algorithm: alg, signingFile: path})
			if err != nil {
				t.Fatalf("a generated %s key did not load back: %v", alg, err)
			}
			if set.signing == nil || set.signing.alg != alg {
				t.Errorf("the loaded key is signing=%v alg=%s, want a %s signing key", set.signing != nil, alg, alg)
			}
		})
	}
}

// checks that GenerateKey refuses an unsupported algorithm.
func TestGenerateKeyRefusesAnUnsupportedAlgorithm(t *testing.T) {
	if _, err := GenerateKey("HS256"); !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("GenerateKey(HS256) gave %v, want ErrInvalidKey", err)
	}
}

// TestASignatureRoundTripsThroughTheHeaderAndTheClaims checks that a
// signature round trips through the header and the claims.
func TestASignatureRoundTripsThroughTheHeaderAndTheClaims(t *testing.T) {
	path := generatedKey(t, algES256)
	set, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: path})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := tokenClaims{
		Issuer:    "https://auth.example.com",
		Subject:   "acct-1",
		Audience:  []string{"https://api.example.com"},
		Expiry:    now.Add(time.Minute).Unix(),
		NotBefore: now.Unix(),
		IssuedAt:  now.Unix(),
		ID:        "jti-1",
		Session:   "sess-1",
		Methods:   []string{"pwd"},
		Assurance: "1",
		AuthTime:  now.Unix(),
		ClientID:  "speed",
	}
	payload, err := claims.marshal()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sign(*set.signing, accessTokenType, payload)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	parsed, err := parseSigned(raw, set.algorithms)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if got := parsed.headerKID(); got != set.signing.kid {
		t.Errorf("the header kid is %q, want %q", got, set.signing.kid)
	}
	if got := parsed.headerAlgorithm(); got != algES256 {
		t.Errorf("the header alg is %q, want %q", got, algES256)
	}
	typ, ok := parsed.headerType()
	if !ok || typ != accessTokenType {
		t.Errorf("the header typ is %q present=%v, want %q", typ, ok, accessTokenType)
	}
	if parsed.criticalHeader() {
		t.Error("the header names a critical extension")
	}

	var got tokenClaims
	if err := parsed.verify(set.signing.pub, &got); err != nil {
		t.Fatalf("verifying: %v", err)
	}
	if got.Subject != claims.Subject || got.Session != claims.Session || got.ID != claims.ID {
		t.Errorf("the verified claims read %+v, want %+v", got, claims)
	}
}

// TestAnAlgorithmOutsideTheAllowListIsRefusedAtParse checks that an algorithm
// outside the allow list is refused at parse.
func TestAnAlgorithmOutsideTheAllowListIsRefusedAtParse(t *testing.T) {
	path := generatedKey(t, algES256)
	set, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: path})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := tokenClaims{Subject: "a"}.marshal()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sign(*set.signing, accessTokenType, payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseSigned(raw, []string{algRS256}); err == nil {
		t.Error("a token signed with ES256 parsed under an RS256-only allow-list")
	}
}

// TestAVerificationWithTheWrongKeyFails checks that a verification with the
// wrong key fails.
func TestAVerificationWithTheWrongKeyFails(t *testing.T) {
	one := generatedKey(t, algES256)
	two := generatedKey(t, algES256)
	oneSet, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: one})
	if err != nil {
		t.Fatal(err)
	}
	twoSet, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: two})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := tokenClaims{Subject: "a"}.marshal()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sign(*oneSet.signing, accessTokenType, payload)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseSigned(raw, oneSet.algorithms)
	if err != nil {
		t.Fatal(err)
	}
	var got tokenClaims
	if err := parsed.verify(twoSet.signing.pub, &got); err == nil {
		t.Error("a token verified against another key")
	}
}

// TestTheAccessTokenTypeIsTheRFC9068Literal holds the typ value the design
// fixes, both as the constant and as what the signed header actually carries, so
// a change to the constant is caught and not only compared with itself.
func TestTheAccessTokenTypeIsTheRFC9068Literal(t *testing.T) {
	if accessTokenType != "at+jwt" {
		t.Errorf("the access token typ is %q, want at+jwt", accessTokenType)
	}
	if accessTokenTypeAlt != "application/at+jwt" {
		t.Errorf("the alternate access token typ is %q, want application/at+jwt", accessTokenTypeAlt)
	}
	set, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: generatedKey(t, algES256)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := tokenClaims{Subject: "a"}.marshal()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sign(*set.signing, accessTokenType, payload)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseSigned(raw, set.algorithms)
	if err != nil {
		t.Fatal(err)
	}
	if typ, ok := parsed.headerType(); !ok || typ != "at+jwt" {
		t.Errorf("the signed header typ is %q present=%v, want at+jwt", typ, ok)
	}
}

// TestANonP256KeyIsRefused checks that a non p256 key is refused.
func TestANonP256KeyIsRefused(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	path := keyFile(t, "p384.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	if _, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: path}); !errors.Is(err, authn.ErrInvalidKey) {
		t.Fatalf("a P-384 key gave %v, want ErrInvalidKey", err)
	}
}

// TestAGroupReadablePrivateKeyIsWarned holds the permission caution's mask: a
// private key readable by the owner's group, though not by others, is still a
// caution.
func TestAGroupReadablePrivateKeyIsWarned(t *testing.T) {
	pemBytes, err := GenerateKey(algES256)
	if err != nil {
		t.Fatal(err)
	}
	path := keyFile(t, "group.pem", pemBytes, 0o640)
	set, diags, err := loadKeySet(resolved{algorithm: algES256, signingFile: path})
	if err != nil {
		t.Fatalf("a group-readable private key stopped the startup: %v", err)
	}
	if set.signing == nil {
		t.Fatal("the key was not taken as the signing key")
	}
	if len(diags) != 1 {
		t.Fatalf("the caution count is %d, want 1", len(diags))
	}
}

// rfc7638Thumbprint computes the RFC 7638 JWK thumbprint of a P-256 public key
// independently of the package, so the kid's derivation is pinned by an outside
// calculation rather than by the code under test.
func rfc7638Thumbprint(t *testing.T, pub *ecdsa.PublicKey) string {
	t.Helper()
	if pub.Curve != elliptic.P256() {
		t.Fatalf("the fixture key is not P-256")
	}
	point, err := pub.Bytes()
	if err != nil {
		t.Fatalf("encoding the public key's point: %v", err)
	}
	if len(point) != 65 || point[0] != 4 {
		t.Fatalf("the key's point is %d bytes, want an uncompressed 65-byte point", len(point))
	}
	enc := base64.RawURLEncoding
	canon := `{"crv":"P-256","kty":"EC","x":"` + enc.EncodeToString(point[1:33]) +
		`","y":"` + enc.EncodeToString(point[33:65]) + `"}`
	sum := sha256.Sum256([]byte(canon))
	return enc.EncodeToString(sum[:])
}

// TestTheKidIsTheRFC7638Thumbprint holds the kid's derivation: it is the
// base64url SHA-256 JWK thumbprint of the key, which an outside verifier can
// recompute, not a name from anywhere.
func TestTheKidIsTheRFC7638Thumbprint(t *testing.T) {
	path := generatedKey(t, algES256)
	set, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: path})
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := set.signing.pub.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("the signing key is a %T, want ECDSA", set.signing.pub)
	}
	if want := rfc7638Thumbprint(t, pub); set.signing.kid != want {
		t.Errorf("the kid is %q, want the RFC 7638 thumbprint %q", set.signing.kid, want)
	}
}

// TestAVerifyOnlyKeyFromAPublicPEM holds that a bare public-key PEM is a usable
// verification file: it becomes an entry with no signer and produces no
// caution.
func TestAVerifyOnlyKeyFromAPublicPEM(t *testing.T) {
	signSet, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: generatedKey(t, algES256)})
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(signSet.entries[0].pub)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := keyFile(t, "verify.pem", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)
	set, diags, err := loadKeySet(resolved{algorithm: algES256, verifyFiles: []string{pubPath}})
	if err != nil {
		t.Fatalf("loading a public-key PEM as a verification file: %v", err)
	}
	if len(set.entries) != 1 {
		t.Fatalf("the set holds %d entries, want 1", len(set.entries))
	}
	if set.entries[0].signer != nil {
		t.Error("a public-key PEM carried a signer")
	}
	if set.signing != nil {
		t.Error("a public-key verification file produced a signing key")
	}
	if len(diags) != 0 {
		t.Errorf("a public-key file produced %d diagnostics, want 0", len(diags))
	}
}

// TestSigningWithAVerifyOnlyKeyIsRefused holds the nil-signer guard: an entry
// with a public half and no private one, which is what a public-key verify file
// loads as, refuses to sign with a message that names the missing half rather
// than reaching the library with nothing to sign with.
func TestSigningWithAVerifyOnlyKeyIsRefused(t *testing.T) {
	signSet, _, err := loadKeySet(resolved{algorithm: algES256, signingFile: generatedKey(t, algES256)})
	if err != nil {
		t.Fatal(err)
	}
	entry := keyEntry{kid: signSet.signing.kid, alg: algES256, pub: signSet.signing.pub}
	payload, err := tokenClaims{Subject: "a"}.marshal()
	if err != nil {
		t.Fatal(err)
	}
	_, err = sign(entry, accessTokenType, payload)
	if err == nil {
		t.Fatal("signing with a key that has no private half succeeded")
	}
	if !strings.Contains(err.Error(), "no private half") {
		t.Errorf("the refusal is %q, want it to name the missing private half", err)
	}
}
