// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package explain_test

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/mcp/test/envtest"
)

var testEnv *envtest.Env

func TestExplain(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "vmop-mcp explain tool suite")
}

var _ = BeforeSuite(func() {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		return
	}
	var err error
	testEnv, err = envtest.Start()
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})
