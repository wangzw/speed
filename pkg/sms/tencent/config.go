package tencent

import (
	"github.com/vislake/speed/pkg/config"
)

const (
	secretIDItem  = "secret-id"
	secretKeyItem = "secret-key"
	sdkAppIDItem  = "sdk-app-id"
	regionItem    = "region"
	signNameItem  = "sign-name"
)

type moduleConfig struct {
	SecretID string `config:"secret-id"`
	// SecretKey is the credential's secret, marked sensitive and registered as a
	// literal redaction rule.
	SecretKey string `config:"secret-key"`
	SDKAppID  string `config:"sdk-app-id"`
	Region    string `config:"region"`
	SignName  string `config:"sign-name"`
}

type resolved struct {
	secretID  string
	secretKey string
	sdkAppID  string
	region    string
	signName  string
}

func (c moduleConfig) resolve() resolved {
	return resolved{
		secretID:  c.SecretID,
		secretKey: c.SecretKey,
		sdkAppID:  c.SDKAppID,
		region:    c.Region,
		signName:  c.SignName,
	}
}

func items() map[string]config.Item {
	return map[string]config.Item{
		secretIDItem: {
			Description: "the identifier of the credential the requests are signed with",
		},
		secretKeyItem: {
			Sensitive:   true,
			Description: "the secret of the credential the requests are signed with",
		},
		sdkAppIDItem: {
			Description: "the carrier application the messages are sent as",
		},
		regionItem: {
			Description: "the carrier region the requests are signed for, such as ap-guangzhou",
		},
		signNameItem: {
			Description: "the approved signature the messages are sent under",
		},
	}
}
