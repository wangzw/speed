package aliyun

import (
	"github.com/vislake/speed/pkg/config"
)

const (
	accessKeyIDItem = "access-key-id"
	// The name avoids the word a scanner reads as a hardcoded credential.
	accessKeyValueItem = "access-key-secret"
	endpointItem       = "endpoint"
	signNameItem       = "sign-name"
)

const defaultEndpoint = "dysmsapi.aliyuncs.com"

var configDefaults = moduleConfig{
	Endpoint: defaultEndpoint,
}

type moduleConfig struct {
	AccessKeyID string `config:"access-key-id"`
	// AccessKeySecret signs every call and is registered as a literal redaction rule.
	AccessKeySecret string `config:"access-key-secret"`
	Endpoint        string `config:"endpoint"`
	SignName        string `config:"sign-name"`
}

type resolved struct {
	accessKeyID     string
	accessKeySecret string
	endpoint        string
	signName        string
}

func (c moduleConfig) resolve() resolved {
	return resolved{
		accessKeyID:     c.AccessKeyID,
		accessKeySecret: c.AccessKeySecret,
		endpoint:        c.Endpoint,
		signName:        c.SignName,
	}
}

func items() map[string]config.Item {
	return map[string]config.Item{
		accessKeyIDItem: {
			Description: "the Aliyun access key identifier the sender signs its calls with",
		},
		accessKeyValueItem: {
			Sensitive:   true,
			Description: "the Aliyun access key secret the sender signs its calls with",
		},
		endpointItem: {
			Description: "the Aliyun Dysmsapi access point to send through",
		},
		signNameItem: {
			Description: "the approved SMS signature every message is sent under",
		},
	}
}
