package s3

import (
	"github.com/vislake/speed/pkg/config"
)

// The keys this backend accepts, relative to its namespace. All six are
// configuration, read once at startup: the endpoint points the client at the
// service, the region the SDK signs with, the bucket holds the objects, the two
// credentials authorize every call, and use-path-style chooses how the bucket is
// addressed.
const (
	endpointItem     = "endpoint"
	regionItem       = "region"
	bucketItem       = "bucket"
	accessKeyItem    = "access-key"
	secretKeyItem    = "secret-key"
	usePathStyleItem = "use-path-style"
)

// configDefaults is what this backend is worth without a configuration file.
// There is none: with no endpoint there is nowhere to connect, with no bucket
// nowhere to keep bytes and with no credentials no call is authorized, so each of
// those carries no default and is declared required. Region and use-path-style
// are optional: the SDK resolves a region on its own and picks an address form of
// its own.
var configDefaults = moduleConfig{}

// moduleConfig is the set of input items this backend accepts.
type moduleConfig struct {
	// Endpoint is the service the client connects to. It may carry an http or
	// https scheme, which chooses the transport; without one the transport is
	// https.
	Endpoint string `config:"endpoint"`
	// Region is the region every call is signed in. It is optional: left empty,
	// the SDK resolves the bucket's region itself.
	Region string `config:"region"`
	// Bucket is the bucket every object's bytes are kept in.
	Bucket string `config:"bucket"`
	// AccessKey is the access-key identifier a call is signed with. It is a
	// credential: the factory registers it as a literal redaction rule.
	AccessKey string `config:"access-key"`
	// SecretKey is the access-key secret a call is signed with. It is a
	// credential: the factory registers it as a literal redaction rule.
	SecretKey string `config:"secret-key"`
	// UsePathStyle addresses the bucket in the request path rather than in the
	// host name. It is what a self-hosted service usually needs.
	UsePathStyle bool `config:"use-path-style"`
}

// items declares this backend's input items. The module descriptor mounts them
// under this backend's namespace.
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
