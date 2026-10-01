// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDiagnoseTool(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "vmop-mcp diagnose tool suite")
}
