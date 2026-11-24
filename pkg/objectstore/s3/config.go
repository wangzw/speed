package s3

import (
	"github.com/vislake/speed/pkg/config"
)

const (
	endpointItem     = "endpoint"
	regionItem       = "region"
	bucketItem       = "bucket"
	accessKeyItem    = "access-key"
	secretKeyItem    = "secret-key"
	usePathStyleItem = "use-path-style"
)

var configDefaults = moduleConfig{}

type moduleConfig struct {
	Endpoint     string `config:"endpoint"`
	Region       string `config:"region"`
	Bucket       string `config:"bucket"`
	AccessKey    string `config:"access-key"`
	SecretKey    string `config:"secret-key"`
	UsePathStyle bool   `config:"use-path-style"`
}

func items() map[string]config.Item {
	return map[string]config.Item{
		endpointItem: {
			Required:    true,
			Description: "the S3-compatible service the backend connects to, with an optional http or https scheme",
		},
		regionItem: {
			Description: "the region every call is signed in; left empty, the SDK resolves it",
		},
		bucketItem: {
			Required:    true,
			Description: "the bucket every object's bytes are kept in",
		},
		accessKeyItem: {
			Required:    true,
			Sensitive:   true,
			Description: "the access-key identifier every call is signed with",
		},
		secretKeyItem: {
			Required:    true,
			Sensitive:   true,
			Description: "the access-key secret every call is signed with",
		},
		usePathStyleItem: {
			Description: "address the bucket in the request path rather than in the host name",
		},
	}
}
