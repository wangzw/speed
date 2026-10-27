package sso

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/model"
)

// The config keys each protocol accepts. A key outside a protocol's set is an
// unknown key and is refused, so a SAML setting written into an OIDC config is
// reported rather than quietly ignored.
var (
	commonConfigKeys = map[string]bool{
		"claims":         true,
		"mfa_acr_values": true,
	}
	oidcConfigKeys = map[string]bool{
		"issuer":    true,
		"client_id": true,
		"scopes":    true,
		"algs":      true,
	}
	samlConfigKeys = map[string]bool{
		"idp_entity_id":     true,
		"idp_sso_url":       true,
		"idp_certs":         true,
		"metadata_url":      true,
		"name_id_format":    true,
		"subject_attribute": true,
		"sign_requests":     true,
	}
)

// The closed set of NameID formats a provider may take. transient carries no
// stable subject, and any other format leaves nothing to bind on.
const (
	nameIDPersistent   = "persistent"
	nameIDUnspecified  = "unspecified"
	nameIDEmailAddress = "emailAddress"
)

// validateConfig checks a config against a protocol and returns its canonical
// form, with the map keys sorted. It refuses an unknown key, a missing required
// key and a value outside its rule, each as ErrInvalidConfig naming the key.
// spKeyConfigured says whether the deployment carries the service provider's key,
// which a SAML provider that signs its requests requires.
func validateConfig(protocol string, raw []byte, spKeyConfigured bool) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: the config is empty", authn.ErrInvalidConfig)
	}
	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("%w: the config is longer than %d bytes", authn.ErrInvalidConfig, maxConfigBytes)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("%w: the config is not a JSON object: %w", authn.ErrInvalidConfig, err)
	}
	var allowed map[string]bool
	switch protocol {
	case model.SSOProtocolOIDC:
		allowed = oidcConfigKeys
	case model.SSOProtocolSAML:
		allowed = samlConfigKeys
	default:
		return nil, fmt.Errorf("%w: the protocol is not one of %s and %s",
			authn.ErrInvalidConfig, model.SSOProtocolOIDC, model.SSOProtocolSAML)
	}
	for key := range fields {
		if !allowed[key] && !commonConfigKeys[key] {
			return nil, fmt.Errorf("%w: the config holds the unknown key %q for a %s provider",
				authn.ErrInvalidConfig, key, protocol)
		}
	}
	if err := validateCommonConfig(fields); err != nil {
		return nil, err
	}
	switch protocol {
	case model.SSOProtocolOIDC:
		if err := validateOIDCConfig(fields); err != nil {
			return nil, err
		}
	case model.SSOProtocolSAML:
		if err := validateSAMLConfig(fields, spKeyConfigured); err != nil {
			return nil, err
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: the config cannot be re-encoded: %w", authn.ErrInvalidConfig, err)
	}
	return out, nil
}

// validateCommonConfig checks the keys both protocols share.
func validateCommonConfig(fields map[string]json.RawMessage) error {
	if raw, ok := fields["claims"]; ok {
		if err := validateClaims(raw); err != nil {
			return err
		}
	}
	if raw, ok := fields["mfa_acr_values"]; ok {
		if _, err := decodeNonEmptyStringList("mfa_acr_values", raw); err != nil {
			return err
		}
	}
	return nil
}

// validateOIDCConfig checks an OIDC config's own keys.
func validateOIDCConfig(fields map[string]json.RawMessage) error {
	issuerRaw, ok := fields["issuer"]
	if !ok {
		return missingKey("issuer")
	}
	issuer, err := decodeString("issuer", issuerRaw)
	if err != nil {
		return err
	}
	if err := validateIssuer(issuer); err != nil {
		return err
	}
	clientRaw, ok := fields["client_id"]
	if !ok {
		return missingKey("client_id")
	}
	if clientID, err := decodeString("client_id", clientRaw); err != nil {
		return err
	} else if strings.TrimSpace(clientID) == "" {
		return fmt.Errorf("%w: client_id is empty", authn.ErrInvalidConfig)
	}
	if raw, ok := fields["scopes"]; ok {
		if _, err := decodeStringList("scopes", raw); err != nil {
			return err
		}
	}
	if raw, ok := fields["algs"]; ok {
		algs, err := decodeStringList("algs", raw)
		if err != nil {
			return err
		}
		if len(algs) == 0 {
			return fmt.Errorf("%w: algs, when written, names at least one signature algorithm", authn.ErrInvalidConfig)
		}
		if err := validateALGs(algs); err != nil {
			return err
		}
	}
	return nil
}

// validateIssuer holds an OIDC issuer to a concrete https one.
func validateIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: issuer has to be a concrete https issuer", authn.ErrInvalidConfig)
	}
	if !isConcreteIssuer(u) {
		return fmt.Errorf("%w: issuer names a multi-tenant aggregator, which serves many tenants and "+
			"cannot stand for one", authn.ErrInvalidConfig)
	}
	return nil
}

// validateALGs refuses a signature algorithm set that admits an unsigned or
// symmetric algorithm: none leaves the token unbound, and an HS algorithm lets a
// holder of the client secret mint one.
func validateALGs(algs []string) error {
	for _, alg := range algs {
		switch {
		case alg == "none":
			return fmt.Errorf("%w: algs may not contain none", authn.ErrInvalidConfig)
		case strings.HasPrefix(alg, "HS"):
			return fmt.Errorf("%w: algs may not contain a symmetric algorithm such as %q", authn.ErrInvalidConfig, alg)
		}
	}
	return nil
}

// validateSAMLConfig checks a SAML config's own keys.
func validateSAMLConfig(fields map[string]json.RawMessage, spKeyConfigured bool) error {
	for _, key := range []string{"idp_entity_id", "idp_sso_url", "idp_certs"} {
		if _, ok := fields[key]; !ok {
			return missingKey(key)
		}
	}
	if entity, err := decodeString("idp_entity_id", fields["idp_entity_id"]); err != nil {
		return err
	} else if strings.TrimSpace(entity) == "" {
		return fmt.Errorf("%w: idp_entity_id is empty, which would let the library skip the issuer check",
			authn.ErrInvalidConfig)
	}
	ssoURL, err := decodeString("idp_sso_url", fields["idp_sso_url"])
	if err != nil {
		return err
	}
	if err = validateHTTPSURL("idp_sso_url", ssoURL, false); err != nil {
		return err
	}
	certs, err := decodeNonEmptyStringList("idp_certs", fields["idp_certs"])
	if err != nil {
		return err
	}
	if len(certs) < 1 || len(certs) > 4 {
		return fmt.Errorf("%w: idp_certs holds 1 to 4 certificates, the current one and the next beside it",
			authn.ErrInvalidConfig)
	}
	if raw, ok := fields["metadata_url"]; ok {
		metadataURL, err := decodeString("metadata_url", raw)
		if err != nil {
			return err
		}
		if metadataURL != "" {
			if err := validateHTTPSURL("metadata_url", metadataURL, true); err != nil {
				return err
			}
		}
	}
	subjectAttribute := ""
	if raw, ok := fields["subject_attribute"]; ok {
		value, err := decodeString("subject_attribute", raw)
		if err != nil {
			return err
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: subject_attribute is empty", authn.ErrInvalidConfig)
		}
		subjectAttribute = value
	}
	if subjectAttribute != "" {
		if err := validateSubjectAttribute(fields, subjectAttribute); err != nil {
			return err
		}
	}
	if raw, ok := fields["name_id_format"]; ok {
		format, err := decodeString("name_id_format", raw)
		if err != nil {
			return err
		}
		if err := validateNameIDFormat(format, subjectAttribute); err != nil {
			return err
		}
	}
	if raw, ok := fields["sign_requests"]; ok {
		sign, err := decodeBool("sign_requests", raw)
		if err != nil {
			return err
		}
		if sign && !spKeyConfigured {
			return fmt.Errorf("%w: sign_requests is on and no service provider key is configured, so no "+
				"AuthnRequest could be signed", authn.ErrInvalidConfig)
		}
	}
	return nil
}

// validateNameIDFormat holds a NameID format to the closed set that can carry a
// stable subject. transient is refused for having none, and emailAddress needs a
// subject attribute because the address itself is unstable.
func validateNameIDFormat(format, subjectAttribute string) error {
	switch format {
	case nameIDPersistent, nameIDUnspecified:
		return nil
	case nameIDEmailAddress:
		if subjectAttribute == "" {
			return fmt.Errorf("%w: an emailAddress NameID is not stable, so subject_attribute has to "+
				"name the attribute the subject is taken from", authn.ErrInvalidConfig)
		}
		return nil
	default:
		return fmt.Errorf("%w: name_id_format is %q, which is not one of %s, %s or %s",
			authn.ErrInvalidConfig, format, nameIDPersistent, nameIDUnspecified, nameIDEmailAddress)
	}
}

// validateClaims holds a claims mapping to the closed set of targets and the
// attribute-name bound. The subject is the key of an external binding, so the
// claim it is taken from has to be a stable identifier; an email address claim
// is not one, because a provider-side address change would stop finding the
// binding made before it.
func validateClaims(raw json.RawMessage) error {
	var claims map[string]string
	if err := json.Unmarshal(raw, &claims); err != nil {
		return fmt.Errorf("%w: claims is not an object of target names", authn.ErrInvalidConfig)
	}
	attrs := 0
	for name, target := range claims {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: a claims mapping holds an empty claim name", authn.ErrInvalidConfig)
		}
		switch {
		case target == "email", target == "display_name":
		case target == "subject":
			if isEmailAddressClaim(name) {
				return fmt.Errorf("%w: the claim %q is an address, which changes, so it may not be mapped to "+
					"subject", authn.ErrInvalidConfig, name)
			}
		case strings.HasPrefix(target, "attr."):
			attrName := strings.TrimPrefix(target, "attr.")
			if attrName == "" || len(attrName) > maxAttrNameBytes {
				return fmt.Errorf("%w: the claims target %q names an attribute of 1 to %d bytes",
					authn.ErrInvalidConfig, target, maxAttrNameBytes)
			}
			attrs++
		default:
			return fmt.Errorf("%w: the claims target %q is not one of email, display_name, subject or "+
				"attr.<name>", authn.ErrInvalidConfig, target)
		}
	}
	if attrs > maxAttrTargets {
		return fmt.Errorf("%w: the claims mapping names %d attributes, at most %d",
			authn.ErrInvalidConfig, attrs, maxAttrTargets)
	}
	return nil
}

// isEmailAddressClaim reports whether a claim name is the standard one carrying
// an email address. SAML attribute names vary, so a subject attribute is held to
// the attribute the mapping actually sends the address from instead.
func isEmailAddressClaim(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "email")
}

// validateSubjectAttribute refuses a SAML subject attribute the claims mapping
// uses for the address: the address is not a stable identifier, so a binding
// keyed on it would be lost when the provider changes it.
func validateSubjectAttribute(fields map[string]json.RawMessage, subjectAttribute string) error {
	raw, ok := fields["claims"]
	if !ok {
		return nil
	}
	var claims map[string]string
	if err := json.Unmarshal(raw, &claims); err != nil {
		return fmt.Errorf("%w: claims is not an object of target names", authn.ErrInvalidConfig)
	}
	for name, target := range claims {
		if target == "email" && name == subjectAttribute {
			return fmt.Errorf("%w: subject_attribute %q is the attribute the address is mapped from, and an "+
				"email address is not a stable subject", authn.ErrInvalidConfig, subjectAttribute)
		}
	}
	return nil
}

// validateHTTPSURL holds a URL to https, and refuses user information unless it
// is allowed: a metadata URL and a single sign-on URL carry none.
func validateHTTPSURL(key, raw string, allowEmpty bool) error {
	if raw == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%w: %s is empty", authn.ErrInvalidConfig, key)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: %s has to be an https URL with a host", authn.ErrInvalidConfig, key)
	}
	if u.User != nil {
		return fmt.Errorf("%w: %s carries user information", authn.ErrInvalidConfig, key)
	}
	return nil
}

// missingKey is the failure of a required key that is not there.
func missingKey(key string) error {
	return fmt.Errorf("%w: %s is required", authn.ErrInvalidConfig, key)
}

// decodeString reads one key as a string.
func decodeString(key string, raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: %s is not a string", authn.ErrInvalidConfig, key)
	}
	return value, nil
}

// decodeBool reads one key as a boolean.
func decodeBool(key string, raw json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("%w: %s is not a boolean", authn.ErrInvalidConfig, key)
	}
	return value, nil
}

// decodeStringList reads one key as a list of strings.
func decodeStringList(key string, raw json.RawMessage) ([]string, error) {
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: %s is not a list of strings", authn.ErrInvalidConfig, key)
	}
	return value, nil
}

// decodeNonEmptyStringList reads one key as a list of strings and refuses an
// empty element.
func decodeNonEmptyStringList(key string, raw json.RawMessage) ([]string, error) {
	value, err := decodeStringList(key, raw)
	if err != nil {
		return nil, err
	}
	for _, item := range value {
		if strings.TrimSpace(item) == "" {
			return nil, fmt.Errorf("%w: %s holds an empty value", authn.ErrInvalidConfig, key)
		}
	}
	return value, nil
}
