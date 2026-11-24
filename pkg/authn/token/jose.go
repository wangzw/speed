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

// keyID is the RFC 7638 thumbprint of a public key, base64url without padding:
// it is taken from the key itself, so replicas reading the same file agree.
func keyID(pub crypto.PublicKey) (string, error) {
	sum, err := (&jose.JSONWebKey{Key: pub}).Thumbprint(crypto.SHA256)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sum), nil
}

func jwkOf(k keyEntry) jose.JSONWebKey {
	return jose.JSONWebKey{Key: k.pub, KeyID: k.kid, Algorithm: k.alg, Use: useSig}
}

func marshalJWKS(keys []keyEntry) ([]byte, error) {
	set := jose.JSONWebKeySet{Keys: make([]jose.JSONWebKey, 0, len(keys))}
	for _, k := range keys {
		set.Keys = append(set.Keys, jwkOf(k))
	}
	return json.Marshal(set)
}

// sign signs payload with the signing key of k and returns the compact JWS,
// whose header carries the kid, the typ and the key's own algorithm.
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

// signedToken is a parsed access token.
type signedToken struct {
	token *jwt.JSONWebToken
}

// parseSigned parses raw, accepting only an algorithm in algorithms and exactly
// one signature.
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

func (t *signedToken) headerKID() string { return t.header().KeyID }

func (t *signedToken) headerAlgorithm() string { return t.header().Algorithm }

func (t *signedToken) headerType() (string, bool) {
	v, ok := t.header().ExtraHeaders[jose.HeaderType]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

func (t *signedToken) criticalHeader() bool {
	_, ok := t.header().ExtraHeaders[jose.HeaderKey("crit")]
	return ok
}

// verify checks the signature against pub and decodes the claims into dest, so a
// token whose signature does not hold yields no claim set. It judges no lifetime
// and no session.
func (t *signedToken) verify(pub crypto.PublicKey, dest any) error {
	return t.token.Claims(pub, dest)
}

// validate verifies the signature against pub, decodes the claims into dest and
// runs the library's standard-claim validation: issuer exact, audience
// intersecting, exp, nbf and iat against now with leeway. An absent exp passes.
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
