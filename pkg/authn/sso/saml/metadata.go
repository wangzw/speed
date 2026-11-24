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

const callbackPathPrefix = "/callback/"

const typePrefix = "urn:speed:authn:"

const metadataValidity = 7 * 24 * time.Hour

const contentTypeMetadata = "application/samlmetadata+xml"

func (p *product) entityID(id string) string {
	return p.mounter.PublicURL() + p.mounter.BaseIn(speedhttp.Scope("")) + "/sso/saml/" + id + "/metadata"
}

func (p *product) acsURL(id string) string {
	return p.mounter.PublicURL() + p.mounter.BaseIn(speedhttp.Scope("")) + callbackPathPrefix + id
}

func (p *product) mountMetadata() {
	rel, err := mount.RelativePath(mount.DefaultBasePath, samlSpec.ResolvePath(metadataOperation.Path))
	if err != nil {
		panic(diagnosticPrefix + ModuleName + ": " + err.Error())
	}
	mount.Declared(p.mounter, metadataOperation, rel, metadataOperation.Handle(p.serveMetadata))
}

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

func notFoundReply() speedhttp.Reply {
	return speedhttp.EmptyReply(http.StatusNotFound)
}

func unavailableFault() error {
	return speedhttp.Fault{
		Status: http.StatusServiceUnavailable,
		Type:   typePrefix + mount.CodeUnavailable,
	}
}

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

type metadataRefresh struct {
	entityID string
	ssoURL   string
	certs    []string
}

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

func pemCertificate(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
