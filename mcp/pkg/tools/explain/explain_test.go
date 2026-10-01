// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package explain_test

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/kube-openapi/pkg/spec3"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/openapi"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/explain"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

func names(e contract.Explanation) []string {
	var out []string
	for _, c := range e.Children {
		out = append(out, c.Name)
	}
	return out
}

var _ = Describe("explain_field", Label(testlabels.MCP), func() {
	var (
		ctx context.Context
		cs  *mcp.ClientSession
	)

	BeforeEach(func() {
		ctx = context.Background()
		env := fakeenv.New(fakeenv.Options{})
		ex := openapi.NewExplainer(func() (*spec3.OpenAPI, error) { return &spec3.OpenAPI{}, nil })
		var err error
		cs, _, err = env.ConnectTools(ctx, func(r *toolkit.Registrar) { explain.Register(r, ex) })
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(cs.Close)
	})

	It("constrains kind to the VM Service kinds in the input schema", func() {
		res, err := fakeenv.Call[contract.Explanation](ctx, cs, "explain_field", map[string]any{"kind": "Pod"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).ToNot(BeNil())
		Expect(res.Err.Code).To(Equal("protocol"))
	})

	It("is a read-tier tool", func() {
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Tools).To(HaveLen(1))
		Expect(res.Tools[0].Annotations.ReadOnlyHint).To(BeTrue())
	})
})

var _ = Describe("explain_field against an API server", Label(testlabels.MCP, testlabels.EnvTest), func() {
	var (
		ctx context.Context
		cs  *mcp.ClientSession
	)

	BeforeEach(func() {
		if testEnv == nil {
			Skip("KUBEBUILDER_ASSETS is not set")
		}
		ctx = context.Background()
		clients, err := kube.NewClients(testEnv.Config, io.Discard)
		Expect(err).ToNot(HaveOccurred())
		env := &toolkit.Env{
			Provider:   kube.NewStaticProvider(clients, kube.Info{Namespace: "default"}),
			Namespaces: toolkit.NewNamespacePolicy("default", nil),
		}
		s := mcp.NewServer(&mcp.Implementation{Name: "vmop-mcp", Version: "test"}, nil)
		explain.Register(toolkit.NewRegistrar(s, env), nil)
		cs, err = fakeenv.Connect(ctx, s)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(cs.Close)
	})

	call := func(kind, path string) fakeenv.Result[contract.Explanation] {
		res, err := fakeenv.Call[contract.Explanation](ctx, cs, "explain_field",
			map[string]any{"kind": kind, "fieldPath": path})
		Expect(err).ToNot(HaveOccurred())
		return res
	}

	It("explains spec.powerOffMode from the real CRD schema", func() {
		// The API server publishes CRD schemas asynchronously, and the
		// explainer caches only a successful fetch.
		var res fakeenv.Result[contract.Explanation]
		Eventually(func(g Gomega) {
			res = call("VirtualMachine", "spec.powerOffMode")
			g.Expect(res.Err).To(BeNil())
		}).Should(Succeed())
		Expect(res.Out.Type).To(Equal("string"))
		Expect(res.Out.Enum).To(ConsistOf("Hard", "Soft", "TrySoft"))
		Expect(res.Out.Description).ToNot(BeEmpty())
	})

	It("explains spec.powerState and the kind root", func() {
		Eventually(func(g Gomega) {
			g.Expect(call("VirtualMachine", "spec.powerState").Err).To(BeNil())
		}).Should(Succeed())
		res := call("VirtualMachine", "spec.powerState")
		Expect(res.Out.Type).To(Equal("string"))

		root := call("VirtualMachine", "")
		Expect(root.Err).To(BeNil())
		Expect(names(root.Out)).To(ContainElements("spec", "status", "metadata"))
	})

	It("reports an unknown field as not_found", func() {
		Eventually(func(g Gomega) {
			res := call("VirtualMachine", "spec.noSuchField")
			g.Expect(res.Err).ToNot(BeNil())
			g.Expect(res.Err.Code).To(Equal(contract.CodeNotFound))
		}).Should(Succeed())
	})
})
