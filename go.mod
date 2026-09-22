module github.com/agntcy/dir-importer

go 1.27.1

require (
	buf.build/gen/go/agntcy/oasf/protocolbuffers/go v1.36.12-20260908102259-bc6a98c20798.2
	github.com/agntcy/dir/api v1.7.1
	github.com/agntcy/dir/client v1.7.1
	github.com/agntcy/oasf-sdk/pkg v1.3.1-0.20260910081500-f0c6172a010a
	github.com/mark3labs/mcp-go v0.58.0
	github.com/modelcontextprotocol/registry v1.8.1
	github.com/sashabaranov/go-openai v1.42.1
	golang.org/x/time v0.16.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	buf.build/gen/go/agntcy/oasf-sdk/protocolbuffers/go v1.36.12-20260811133823-5281d0c487b5.1 // indirect
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/ipfs/go-cid v0.6.2 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/mr-tron/base58 v1.3.0 // indirect
	github.com/multiformats/go-base32 v0.1.0 // indirect
	github.com/multiformats/go-base36 v0.2.0 // indirect
	github.com/multiformats/go-multibase v0.3.0 // indirect
	github.com/multiformats/go-multihash v0.2.3 // indirect
	github.com/multiformats/go-varint v0.1.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.45.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/grpc v1.83.2 // indirect
	lukechampine.com/blake3 v1.4.1 // indirect
)

// Cosign does not updated the crypto11 owner
replace github.com/ThalesIgnite/crypto11 => github.com/ThalesGroup/crypto11 v1.6.8
