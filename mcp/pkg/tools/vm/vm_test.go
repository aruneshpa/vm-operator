// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vm_test

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1cloudinit "github.com/vmware-tanzu/vm-operator/api/v1alpha6/cloudinit"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vm"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

var _ = Describe("VM tools", Label(testlabels.MCP), func() {
	var (
		ctx  context.Context
		opts fakeenv.Options
		env  *fakeenv.Env
		cs   *mcp.ClientSession
	)

	BeforeEach(func() {
		ctx = context.Background()
		opts = fakeenv.Options{}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, vm.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	Context("list_virtual_machines", func() {
		BeforeEach(func() {
			for i := range 3 {
				v := fakeenv.VM(fmt.Sprintf("vm-%d", i))
				v.Labels = map[string]string{"app": "web"}
				if i == 2 {
					v.Labels["app"] = "db"
				}
				v.Status.PowerState = vmopv1.VirtualMachinePowerStateOn
				v.Status.Network = &vmopv1.VirtualMachineNetworkStatus{PrimaryIP4: "10.0.0.1"}
				v.Status.Conditions = []metav1.Condition{{Type: vmopv1.ReadyConditionType, Status: metav1.ConditionTrue}}
				opts.Objects = append(opts.Objects, v)
			}
			other := fakeenv.VM("elsewhere")
			other.Namespace = "prod"
			opts.Objects = append(opts.Objects, other)
		})

		It("lists VMs in the default namespace as summaries", func() {
			res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Items).To(HaveLen(3))
			first := res.Out.Items[0]
			Expect(first.Namespace).To(Equal(fakeenv.DefaultNamespace))
			Expect(first.PowerState.Observed).To(Equal("PoweredOn"))
			Expect(first.PrimaryIP4.Value).To(Equal("10.0.0.1"))
			Expect(first.Ready).To(Equal("True"))
			Expect(res.Text).To(ContainSubstring("3 VMs"))
		})

		It("filters by label selector", func() {
			res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines",
				map[string]any{"labelSelector": "app=db"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Out.Items).To(HaveLen(1))
			Expect(res.Out.Items[0].Name).To(Equal("vm-2"))
		})

		It("rejects an invalid label selector", func() {
			res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines",
				map[string]any{"labelSelector": "app in ("})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeInvalid))
		})

		When("the API server returns a continue token", func() {
			var seen []string
			BeforeEach(func() {
				seen = nil
				opts.Interceptor = interceptor.Funcs{
					List: func(ctx context.Context, c ctrlclient.WithWatch, l ctrlclient.ObjectList, o ...ctrlclient.ListOption) error {
						lo := &ctrlclient.ListOptions{}
						lo.ApplyOptions(o)
						seen = append(seen, lo.Continue)
						if err := c.List(ctx, l, o...); err != nil {
							return err
						}
						if lo.Continue == "" {
							l.SetContinue("upstream-token")
						}
						return nil
					},
				}
			})

			It("wraps it in an opaque cursor and passes it back upstream", func() {
				res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines",
					map[string]any{"limit": 2})
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Out.NextCursor).ToNot(BeEmpty())
				Expect(res.Out.NextCursor).ToNot(Equal("upstream-token"))
				Expect(res.Text).To(ContainSubstring("cursor="))

				res2, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines",
					map[string]any{"limit": 2, "cursor": res.Out.NextCursor})
				Expect(err).ToNot(HaveOccurred())
				Expect(res2.Out.NextCursor).To(BeEmpty())
				Expect(seen).To(Equal([]string{"", "upstream-token"}))
			})

			It("rejects a malformed cursor", func() {
				res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines",
					map[string]any{"cursor": "!!!"})
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodeInvalid))
			})
		})

		It("lists another namespace when asked", func() {
			res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines",
				map[string]any{"namespace": "prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Out.Items).To(HaveLen(1))
		})

		When("the namespace allow-list excludes the namespace", func() {
			var calls int
			BeforeEach(func() {
				calls = 0
				opts.Namespaces = []string{fakeenv.DefaultNamespace}
				opts.Interceptor = interceptor.Funcs{
					List: func(ctx context.Context, c ctrlclient.WithWatch, l ctrlclient.ObjectList, o ...ctrlclient.ListOption) error {
						calls++
						return c.List(ctx, l, o...)
					},
				}
			})

			It("refuses before calling the API", func() {
				res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines",
					map[string]any{"namespace": "prod"})
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodeNamespaceNotAllowed))
				Expect(calls).To(BeZero())
			})
		})

		When("the Supervisor forbids the request", func() {
			BeforeEach(func() {
				opts.Interceptor = interceptor.Funcs{
					List: func(context.Context, ctrlclient.WithWatch, ctrlclient.ObjectList, ...ctrlclient.ListOption) error {
						return apierrors.NewForbidden(schema.GroupResource{Group: "vmoperator.vmware.com", Resource: "virtualmachines"},
							"", fmt.Errorf("user cannot list"))
					},
				}
			})

			It("returns the Supervisor's message as a forbidden error", func() {
				res, err := fakeenv.Call[contract.VirtualMachineList](ctx, cs, "list_virtual_machines", map[string]any{})
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodeForbidden))
				Expect(res.Err.Message).To(ContainSubstring("user cannot list"))
			})
		})
	})

	Context("get_virtual_machine", func() {
		const inlineSecret = "SENTINEL-INLINE-PAYLOAD"

		BeforeEach(func() {
			v := fakeenv.VM("web-01")
			v.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
					CloudConfig: &vmopv1cloudinit.CloudConfig{
						RunCmd: []byte(`["echo ` + inlineSecret + `"]`),
					},
					SSHAuthorizedKeys: []string{"ssh-rsa " + inlineSecret},
				},
				VAppConfig: &vmopv1.VirtualMachineBootstrapVAppConfigSpec{
					Properties: []vmopv1common.KeyValueOrSecretKeySelectorPair{
						{Key: "password", Value: vmopv1common.ValueOrSecretKeySelector{Value: new(inlineSecret)}},
						{Key: "token", Value: vmopv1common.ValueOrSecretKeySelector{
							From: &vmopv1common.SecretKeySelector{Name: "vapp-secret", Key: "token"},
						}},
					},
				},
			}
			v.Spec.Advanced = &vmopv1.VirtualMachineAdvancedSpec{
				ExtraConfig: []vmopv1common.KeyValuePair{{Key: "guestinfo.secret", Value: inlineSecret}},
			}
			opts.Objects = append(opts.Objects, v)
		})

		It("never returns inline bootstrap or extraConfig values", func() {
			res, err := fakeenv.Call[contract.VirtualMachineDetail](ctx, cs, "get_virtual_machine",
				map[string]any{"name": "web-01"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).To(BeNil())

			Expect(res.Text).ToNot(ContainSubstring(inlineSecret))
			Expect(fmt.Sprint(res.Raw.StructuredContent)).ToNot(ContainSubstring(inlineSecret))

			b := res.Out.Bootstrap
			Expect(b).ToNot(BeNil())
			Expect(b.Providers).To(ConsistOf("cloudInit", "vAppConfig"))
			Expect(b.InlineFieldsPresent).To(ContainElements(
				"spec.bootstrap.cloudInit.cloudConfig.runcmd",
				"spec.bootstrap.cloudInit.sshAuthorizedKeys[]",
				"spec.bootstrap.vAppConfig.properties[].value.value",
			))
			Expect(b.SecretRefs).To(ContainElement(contract.SecretRef{
				Name: "vapp-secret", Key: "token", FieldPath: "spec.bootstrap.vAppConfig.properties[].value.from",
			}))
			Expect(res.Out.ExtraConfigKeys).To(Equal([]string{"guestinfo.secret"}))
		})

		It("returns not_found for a missing VM", func() {
			res, err := fakeenv.Call[contract.VirtualMachineDetail](ctx, cs, "get_virtual_machine",
				map[string]any{"name": "missing"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeNotFound))
		})

		It("rejects unknown input fields", func() {
			res, err := fakeenv.Call[contract.VirtualMachineDetail](ctx, cs, "get_virtual_machine",
				map[string]any{"name": "web-01", "bogus": true})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).ToNot(BeNil())
		})
	})
})
