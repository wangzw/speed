package saml

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"net/http"
	"strings"
	"time"

	saml2 "github.com/russellhaering/gosaml2"
	"github.com/russellhaering/gosaml2/types"
	dsigtypes "github.com/russellhaering/goxmldsig/types"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// callbackPathPrefix is the engine's callback route, relative to the base path,
// which the ACS address is derived from.
const callbackPathPrefix = "/callback/"

// typePrefix starts the Problem.Type of every failure this capability's handlers
// write, the code following it being one of mount's closed table.
const typePrefix = "urn:speed:authn:"

// metadataValidity is how long the published metadata is declared valid.
const metadataValidity = 7 * 24 * time.Hour

// contentTypeMetadata is the media type the metadata document is served as.
const contentTypeMetadata = "application/samlmetadata+xml"

// entityID is the identity of this deployment acting as a service provider for
// one provider. It is the metadata address itself, so a provider's IdP sees a
// distinct service provider. It is derived from the configured public address
// alone, never from the Host header or a forwarded header.
func (p *product) entityID(id string) string {
	return p.mounter.PublicURL() + p.mounter.Base() + "/sso/saml/" + id + "/metadata"
}

// acsURL is the assertion consumer service address for one provider: the
// engine's callback route for the provider identifier.
func (p *product) acsURL(id string) string {
	return p.mounter.PublicURL() + p.mounter.Base() + callbackPathPrefix + id
}

// mountMetadata registers the service provider metadata route as a public one.
// The route is registered from the operation that declares it, so the handler's
// input and answer types, the fragment and the route are one source. The
// declaration writes the route against the module's API root, and the relative
// form is derived from the default base rather than the configured one: a
// configured base moves the routes without moving the contract, so the mounter
// joins its own base to a path that is already relative.
func (p *product) mountMetadata() {
	rel, err := mount.RelativePath(mount.DefaultBasePath, samlSpec.ResolvePath(metadataOperation.Path))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	p.mounter.Public(http.MethodGet, rel, metadataOperation.Handle(p.serveMetadata))
}

// serveMetadata answers one provider's service provider metadata. A provider
// that does not exist, is disabled or is not a SAML provider is answered a
// not-found: there is no service provider to describe.
func (p *product) serveMetadata(ctx context.Context, in metadataInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	row, err := p.providers.Get(ctx, in.Provider)
	switch {
	case errors.Is(err, authn.ErrProviderNotFound):
		return notFoundReply(), nil
	case err != nil:
		return nil, unavailableFault()
	}
	if !row.Enabled || row.Protocol != providerProtocol {
		return notFoundReply(), nil
	}
	doc, err := p.metadataEntity(row)
	if err != nil {
		return notFoundReply(), nil
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, speedhttp.Fault{Status: http.StatusInternalServerError}
	}
	return speedhttp.BytesReply(http.StatusOK, contentTypeMetadata, append([]byte(xml.Header), body...)), nil
}

// notFoundReply is the refusal of a route whose named provider has no service
// provider to describe. It carries no problem code: the capability's closed code
// set has none for a not-found, and the design answers one bare, as an empty
// reply with neither a body nor a content type.
func notFoundReply() speedhttp.Reply {
	return speedhttp.EmptyReply(http.StatusNotFound)
}

// unavailableFault is the refusal of a route whose provider register could not
// be reached. The cause stays in the log and never reaches the caller.
func unavailableFault() error {
	return speedhttp.Fault{
		Status: http.StatusServiceUnavailable,
		Type:   typePrefix + mount.CodeUnavailable,
	}
}

// metadataEntity builds one provider's entity descriptor: the entity identifier
// is the metadata address itself, the assertion consumer service is the
// callback with the HTTP-POST binding, WantAssertionsSigned is true, and
// AuthnRequestsSigned follows the provider's own sign_requests. The signing key
// descriptors carry the current certificate and, when a rotation is in progress,
// the next one beside it.
func (p *product) metadataEntity(row authn.SSOProvider) (*types.EntityDescriptor, error) {
	cfg, err := parseProviderConfig(row.Config)
	if err != nil {
		return nil, err
	}
	entity := p.entityID(row.ID)
	sp := &types.SPSSODescriptor{
		AuthnRequestsSigned:        cfg.SignRequests,
		WantAssertionsSigned:       true,
		ProtocolSupportEnumeration: saml2.SAMLProtocolNamespace,
		AssertionConsumerServices: []types.IndexedEndpoint{{
			Binding:  saml2.BindingHttpPost,
			Location: p.acsURL(row.ID),
			Index:    1,
		}},
	}
	if p.sp.configured() {
		sp.KeyDescriptors = append(sp.KeyDescriptors, signingKeyDescriptor(p.sp.certDER))
		if len(p.sp.nextDER) > 0 {
			sp.KeyDescriptors = append(sp.KeyDescriptors, signingKeyDescriptor(p.sp.nextDER))
		}
	}
	return &types.EntityDescriptor{
		ValidUntil:      p.clock.Now().Add(metadataValidity),
		EntityID:        entity,
		SPSSODescriptor: sp,
	}, nil
}

// signingKeyDescriptor is one signing key descriptor carrying a certificate, in
// the base64 DER form the metadata schema uses.
func signingKeyDescriptor(der []byte) types.KeyDescriptor {
	return types.KeyDescriptor{
		Use: "signing",
		KeyInfo: dsigtypes.KeyInfo{
			X509Data: dsigtypes.X509Data{
				X509Certificates: []dsigtypes.X509Certificate{{
					Data: base64.StdEncoding.EncodeToString(der),
				}},
			},
		},
	}
}

// metadataEntityForRefresh is the shape a SAML refresh parse needs: the identity
// provider's entity identifier, single sign-on URL and signing certificates. It
// exists so that the parser reads only what the refresh writes.
type metadataRefresh struct {
	entityID string
	ssoURL   string
	certs    []string
}

// parseIDPMetadata reads an identity provider's entity descriptor and pulls out
// the entity identifier, the first single sign-on URL and the signing
// certificates, as PEM. A descriptor that is not an identity provider's, or that
// names none of the three, is refused.
func parseIDPMetadata(raw []byte) (metadataRefresh, error) {
	var desc types.EntityDescriptor
	if err := xml.Unmarshal(raw, &desc); err != nil {
		return metadataRefresh{}, err
	}
	idp := desc.IDPSSODescriptor
	if idp == nil {
		return metadataRefresh{}, errNoIDPDescriptor
	}
	entity := strings.TrimSpace(desc.EntityID)
	if entity == "" {
		return metadataRefresh{}, errNoEntityID
	}
	var ssoURL string
	for _, svc := range idp.SingleSignOnServices {
		if svc.Location != "" {
			ssoURL = svc.Location
			break
		}
	}
	if ssoURL == "" {
		return metadataRefresh{}, errNoSSOService
	}
	out := metadataRefresh{entityID: entity, ssoURL: ssoURL}
	for _, kd := range idp.KeyDescriptors {
		for _, cert := range kd.KeyInfo.X509Data.X509Certificates {
			der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cert.Data))
			if err != nil || len(der) == 0 {
				continue
			}
			out.certs = append(out.certs, pemCertificate(der))
		}
	}
	if len(out.certs) == 0 {
		return metadataRefresh{}, errNoCertificate
	}
	return out, nil
}

// pemCertificate wraps DER certificate bytes as PEM, which is how a provider's
// config holds the identity provider's certificates.
func pemCertificate(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
