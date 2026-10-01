// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/mcp/test/envtest"
)

var (
	testEnv *envtest.Env
	binary  string
)

func TestVMOPMCP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "vmop-mcp binary suite")
}

var _ = BeforeSuite(func() {
	dir, err := os.MkdirTemp("", "vmop-mcp-bin-")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(os.RemoveAll, dir)
	binary = filepath.Join(dir, "vmop-mcp")
	out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), string(out))

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		return
	}
	testEnv, err = envtest.Start()
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})
