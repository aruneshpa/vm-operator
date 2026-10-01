// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmimage_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVMImage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "vmop-mcp VM image tools suite")
}
