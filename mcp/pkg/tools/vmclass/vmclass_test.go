// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmclass_test

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmclass"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

const configSpecSentinel = "SENTINEL-CONFIGSPEC"

func class(ns, name string, cpus int64, mem string, configSpec bool) *vmopv1.VirtualMachineClass {
	c := &vmopv1.VirtualMachineClass{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	c.Spec.Description = name + " class"
	c.Spec.Hardware.Cpus = cpus
	c.Spec.Hardware.Memory = resource.MustParse(mem)
	if configSpec {
		c.Spec.ConfigSpec = []byte(`{"_typeName":"VirtualMachineConfigSpec","extraConfig":[{"key":"guestinfo.x","value":"` + configSpecSentinel + `"}]}`)
	}
	return c
}

var _ = Describe("VM class tools", Label(testlabels.MCP), func() {
	var (
		ctx  context.Context
		opts fakeenv.Options
		env  *fakeenv.Env
		cs   *mcp.ClientSession
	)

	BeforeEach(func() {
		ctx = context.Background()
		opts = fakeenv.Options{Objects: []ctrlclient.Object{
			class(fakeenv.DefaultNamespace, "small", 2, "4Gi", false),
			class(fakeenv.DefaultNamespace, "gpu", 8, "32Gi", true),
			class("prod", "prod-only", 4, "8Gi", false),
		}}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, vmclass.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	It("lists the namespace's classes with CPU and memory", func() {
		res, err := fakeenv.Call[contract.VirtualMachineClassList](ctx, cs, "list_virtual_machine_classes", map[string]any{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		Expect(res.Out.Items).To(HaveLen(2))
		byName := map[string]contract.VirtualMachineClassSummary{}
		for _, c := range res.Out.Items {
			byName[c.Name] = c
		}
		Expect(byName["small"].CPUs).To(BeEquivalentTo(2))
		Expect(byName["small"].Memory).To(Equal("4Gi"))
		Expect(byName["small"].HasConfigSpec).To(BeFalse())
		Expect(byName["gpu"].HasConfigSpec).To(BeTrue())
		Expect(res.Text).To(ContainSubstring("2 classes"))
		Expect(res.Text).ToNot(ContainSubstring(configSpecSentinel))
		Expect(fmt.Sprint(res.Raw.StructuredContent)).ToNot(ContainSubstring(configSpecSentinel))
	})

	It("gets a class without its configSpec", func() {
		res, err := fakeenv.Call[contract.VirtualMachineClassSummary](ctx, cs, "get_virtual_machine_class",
			map[string]any{"name": "gpu"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		Expect(res.Out.HasConfigSpec).To(BeTrue())
		Expect(res.Out.Memory).To(Equal("32Gi"))
		Expect(res.Out.Description).To(Equal("gpu class"))
		Expect(res.Text).ToNot(ContainSubstring(configSpecSentinel))
		Expect(fmt.Sprint(res.Raw.StructuredContent)).ToNot(ContainSubstring(configSpecSentinel))
	})

	It("returns not_found for a class in another namespace", func() {
		res, err := fakeenv.Call[contract.VirtualMachineClassSummary](ctx, cs, "get_virtual_machine_class",
			map[string]any{"name": "prod-only"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).ToNot(BeNil())
		Expect(res.Err.Code).To(Equal(contract.CodeNotFound))
	})

	When("the namespace allow-list excludes the namespace", func() {
		BeforeEach(func() { opts.Namespaces = []string{fakeenv.DefaultNamespace} })

		It("refuses", func() {
			res, err := fakeenv.Call[contract.VirtualMachineClassList](ctx, cs, "list_virtual_machine_classes",
				map[string]any{"namespace": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err.Code).To(Equal(contract.CodeNamespaceNotAllowed))
		})
	})
})
