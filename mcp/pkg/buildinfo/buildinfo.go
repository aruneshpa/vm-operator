// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package buildinfo holds build metadata for the vmop-mcp binary. The values
// are set at link time with -ldflags "-X".
package buildinfo

var (
	// Version is the release version of vmop-mcp.
	Version = "dev"

	// Commit is the git commit from which vmop-mcp was built.
	Commit = "unknown"
)
