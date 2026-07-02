// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package scanner

import (
	scannerconfig "github.com/agntcy/dir-importer/scanner/config"
	"github.com/agntcy/dir-importer/scanner/factory"
	dirscanner "github.com/agntcy/dir/utils/scanner"
)

func init() {
	factory.Register("mcp", func(cfg scannerconfig.Config) dirscanner.Runner {
		return dirscanner.NewMCPRunner(dirscanner.MCPConfig{CLIPath: cfg.CLIPath})
	})
}
