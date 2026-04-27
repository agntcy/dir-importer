# Agent Directory Importer

[![CI](https://github.com/agntcy/dir-importer/actions/workflows/ci.yml/badge.svg)](https://github.com/agntcy/dir-importer/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/agntcy/dir-importer/branch/main/graph/badge.svg)](https://codecov.io/gh/agntcy/dir-importer)
[![Go Reference](https://pkg.go.dev/badge/github.com/agntcy/dir-importer.svg)](https://pkg.go.dev/github.com/agntcy/dir-importer)

The Agent Directory Importer is a Go library that makes it possible to import
different types of records (for example MCP server definitions) into an
[Agent Directory](https://github.com/agntcy/dir) node.

For detailed usage, see the
[Directory CLI reference](https://docs.agntcy.org/dir/directory-cli-reference/#import-operations).

## Installation

```sh
go get github.com/agntcy/dir-importer
```

## Development

Common tasks are exposed via [Taskfile](https://taskfile.dev):

```sh
task build    # go build ./...
task test     # unit tests
task lint     # golangci-lint
task license  # license check
task tidy     # go mod tidy
```

To enable the pre-commit hook that runs `golangci-lint` on staged Go files:

```sh
task deps
```

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) and
[CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md).

## Security

Please report security vulnerabilities following the guidance in
[SECURITY.md](./SECURITY.md).

## Copyright Notice

[Copyright Notice and License](./LICENSE.md)

Distributed under Apache 2.0 License. See LICENSE for more information.
Copyright AGNTCY Contributors (https://github.com/agntcy)
