package token

import (
	"crypto"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// This file is the one bridge from the package to the JOSE library. Everything
// the token's signing, verification and publication need of go-jose is here and
// nowhere else: the rest of the package reads a token through the methods of
// signedToken and builds a key set through marshalJWKS, so a change of library
// reaches this file alone. The library is not named in any signature outside it.

// keyID is the RFC 7638 thumbprint of a public key, base64url without padding:
// the kid of the key, taken from the key itself rather than from any name in the
// configuration, so two replicas that read the same file agree on it.
func keyID(pub crypto.PublicKey) (string, error) {
	sum, err := (&jose.JSONWebKey{Key: pub}).Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sum), nil
}

// jwkOf is a key's public JWK: its public half with the kid, the algorithm and
// the signature use. It carries no private field even when the entry holds a
// signer.
func jwkOf(k keyEntry) jose.JSONWebKey {
	return jose.JSONWebKey{Key: k.pub, KeyID: k.kid, Algorithm: k.alg, Use: useSig}
}

// marshalJWKS writes the public JWK set of every key as JSON, the body of
// GET B/jwks.json. The set holds the public half of each key and no private
// field.
func marshalJWKS(keys []keyEntry) ([]byte, error) {
	set := jose.JSONWebKeySet{Keys: make([]jose.JSONWebKey, 0, len(keys))}
	for _, k := range keys {
		set.Keys = append(set.Keys, jwkOf(k))
	}
	return json.Marshal(set)
}

// sign signs payload with the signing key of k and returns the compact JWS. The
// header carries the kid and the type typ; the algorithm is the key's own, so
// the algorithm of a token is fixed by the key rather than by a claim.
func sign(k keyEntry, typ string, payload []byte) (string, error) {
	if k.signer == nil {
		return "", fmt.Errorf("%s: the key %s has no private half", ModuleName, k.kid)
	}
	opts := (&jose.SignerOptions{}).WithType(jose.ContentType(typ))
	signer, err := jose.NewSigner(
		jose.SigningKey{
			Algorithm: jose.SignatureAlgorithm(k.alg),
			Key:       jose.JSONWebKey{Key: k.signer, KeyID: k.kid, Algorithm: k.alg},
		},
		opts,
	)
	if err != nil {
		return "", err
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

// signedToken is a parsed access token. The jose types it holds live in this
// file alone: the rest of the package reads a token through the methods below
// and never names the library.
type signedToken struct {
	token *jwt.JSONWebToken
}

// parseSigned parses raw, accepting only a signature whose algorithm is in
// algorithms. A token that does not carry exactly one signature, or one whose
// algorithm is not listed, is refused here.
func parseSigned(raw string, algorithms []string) (*signedToken, error) {
	allowed := make([]jose.SignatureAlgorithm, len(algorithms))
	for i, a := range algorithms {
		allowed[i] = jose.SignatureAlgorithm(a)
	}
	tok, err := jwt.ParseSigned(raw, allowed)
	if err != nil {
		return nil, err
	}
	if len(tok.Headers) != 1 {
		return nil, fmt.Errorf("%s: the token carries %d signatures", ModuleName, len(tok.Headers))
	}
	return &signedToken{token: tok}, nil
}

func (t *signedToken) header() jose.Header { return t.token.Headers[0] }

// headerKID is the kid of the token's header, empty when there is none.
func (t *signedToken) headerKID() string { return t.header().KeyID }

// headerAlgorithm is the alg of the token's header.
func (t *signedToken) headerAlgorithm() string { return t.header().Algorithm }

// headerType is the typ of the token's header and whether it was present. A
// token without a typ is refused by the verifying sequence, which is why the
// presence is reported apart from the value.
func (t *signedToken) headerType() (string, bool) {
	v, ok := t.header().ExtraHeaders[jose.HeaderType]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

// criticalHeader reports whether the token names any critical header extension,
// which the backend refuses: it understands none of them.
func (t *signedToken) criticalHeader() bool {
	_, ok := t.header().ExtraHeaders[jose.HeaderKey("crit")]
	return ok
}

// verify checks the signature against pub and decodes the claims into dest. The
// claims are handed back only through dest, so a token whose signature does not
// hold yields no claim set. It is the reading a revocation needs, which judges
// no lifetime and no session.
func (t *signedToken) verify(pub crypto.PublicKey, dest any) error {
	return t.token.Claims(pub, dest)
}

// validate is the verifying sequence's claim step: it checks the signature
// against pub, decodes the claims into dest and runs the library's own
// standard-claim validation over them — the issuer compared exactly, the
// audience needing a non-empty intersection with audience, and exp, nbf and iat
// compared against now with leeway. It is the one place the library's Expected
// and ValidateWithLeeway are named, so the verifying sequence states neither the
// library's type nor its rules. A token whose exp is absent passes here, because
// the library leaves it optional; requiring it is the caller's own rule.
func (t *signedToken) validate(pub crypto.PublicKey, dest any, issuer string, audience []string,
	now time.Time, leeway time.Duration,
) error {
	var std jwt.Claims
	if err := t.token.Claims(pub, &std, dest); err != nil {
		return err
	}
	return std.ValidateWithLeeway(jwt.Expected{
		Issuer:      issuer,
		AnyAudience: jwt.Audience(audience),
		Time:        now,
	}, leeway)
}
