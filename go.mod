module github.com/agntcy/dir-importer

go 1.26.5

require (
	buf.build/gen/go/agntcy/oasf/protocolbuffers/go v1.36.12-20260721113505-cf7db8888586.1
	github.com/agntcy/dir/api v1.6.3
	github.com/agntcy/dir/client v1.6.3
	github.com/agntcy/oasf-sdk/pkg v1.1.0
	github.com/mark3labs/mcp-go v0.58.0
	github.com/modelcontextprotocol/registry v1.8.1
	github.com/sashabaranov/go-openai v1.42.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
)

require (
	buf.build/gen/go/agntcy/oasf-sdk/protocolbuffers/go v1.36.11-20260714100340-94ed518b08ce.1 // indirect
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260709200747-435963d16310.1 // indirect
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/ipfs/go-cid v0.6.2 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
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
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.15.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260630182238-925bb5da69e7 // indirect
	google.golang.org/grpc v1.82.1 // indirect
	lukechampine.com/blake3 v1.4.1 // indirect
)

// Cosign does not updated the crypto11 owner
replace github.com/ThalesIgnite/crypto11 => github.com/ThalesGroup/crypto11 v1.6.8
