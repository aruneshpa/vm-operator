// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmimage_test

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmimage"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

const ovfSentinel = "SENTINEL-OVF-DEFAULT"

// listCall records one upstream List request.
type listCall struct {
	Kind      string
	Namespace string
	Continue  string
}

func vmi(ns, name, display string) *vmopv1.VirtualMachineImage {
	img := &vmopv1.VirtualMachineImage{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	img.Status.Name = display
	img.Status.OSInfo = vmopv1.VirtualMachineImageOSInfo{Type: "ubuntu64Guest", Version: "24.04"}
	img.Status.Firmware = "efi"
	img.Status.HardwareVersion = new(int32(21))
	img.Status.Conditions = []metav1.Condition{{Type: vmopv1.ReadyConditionType, Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}}
	img.Status.OVFProperties = []vmopv1.OVFProperty{{Key: "password", Type: "string", Default: new(ovfSentinel)}}
	img.Status.ProductInfo = vmopv1.VirtualMachineImageProductInfo{Vendor: "Canonical", Product: "Ubuntu", FullVersion: "24.04"}
	return img
}

func cvmi(name, display string) *vmopv1.ClusterVirtualMachineImage {
	ns := vmi("", name, display)
	return &vmopv1.ClusterVirtualMachineImage{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: ns.Status}
}

var _ = Describe("VM image tools", Label(testlabels.MCP), func() {
	var (
		ctx   context.Context
		opts  fakeenv.Options
		env   *fakeenv.Env
		cs    *mcp.ClientSession
		calls []listCall

		// Continue tokens the interceptor returns on a first page.
		nsToken, clusterToken string
		// expireNS makes a namespaced List with a continue token fail with
		// 410 Gone.
		expireNS bool
	)

	list := func(args map[string]any) fakeenv.Result[contract.VirtualMachineImageList] {
		res, err := fakeenv.Call[contract.VirtualMachineImageList](ctx, cs, "list_virtual_machine_images", args)
		Expect(err).ToNot(HaveOccurred())
		return res
	}
	names := func(l contract.VirtualMachineImageList) []string {
		var out []string
		for _, i := range l.Items {
			out = append(out, i.Kind+"/"+i.Name)
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		calls = nil
		nsToken, clusterToken, expireNS = "", "", false
		opts = fakeenv.Options{
			Objects: []ctrlclient.Object{
				vmi(fakeenv.DefaultNamespace, "vmi-a", "ubuntu-a"),
				vmi(fakeenv.DefaultNamespace, "vmi-b", "ubuntu-b"),
				vmi("prod", "vmi-p", "prod-only"),
				cvmi("cvmi-x", "photon"),
			},
			Interceptor: interceptor.Funcs{
				List: func(ctx context.Context, c ctrlclient.WithWatch, l ctrlclient.ObjectList, o ...ctrlclient.ListOption) error {
					lo := &ctrlclient.ListOptions{}
					lo.ApplyOptions(o)
					kind := fmt.Sprintf("%T", l)
					calls = append(calls, listCall{Kind: kind, Namespace: lo.Namespace, Continue: lo.Continue})
					_, isNS := l.(*vmopv1.VirtualMachineImageList)
					if isNS && expireNS && lo.Continue != "" {
						return apierrors.NewResourceExpired("continue token expired")
					}
					if err := c.List(ctx, l, o...); err != nil {
						return err
					}
					if lo.Continue == "" {
						if isNS && nsToken != "" {
							l.SetContinue(nsToken)
						}
						if !isNS && clusterToken != "" {
							l.SetContinue(clusterToken)
						}
					}
					return nil
				},
			},
		}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, vmimage.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	const (
		nsList      = "*v1alpha6.VirtualMachineImageList"
		clusterList = "*v1alpha6.ClusterVirtualMachineImageList"
	)

	Context("list_virtual_machine_images", func() {
		It("lists namespace images first, then cluster images, in one page", func() {
			res := list(map[string]any{})
			Expect(res.Err).To(BeNil())
			Expect(names(res.Out)).To(Equal([]string{
				"VirtualMachineImage/vmi-a", "VirtualMachineImage/vmi-b", "ClusterVirtualMachineImage/cvmi-x",
			}))
			Expect(res.Out.NextCursor).To(BeEmpty())
			Expect(calls).To(Equal([]listCall{
				{Kind: nsList, Namespace: fakeenv.DefaultNamespace},
				{Kind: clusterList},
			}))
			first := res.Out.Items[0]
			Expect(first.DisplayName.Value).To(Equal("ubuntu-a"))
			Expect(first.OSInfo.Version).To(Equal("24.04"))
			Expect(first.HardwareVersion).To(BeEquivalentTo(21))
			Expect(first.Ready).To(Equal("True"))
		})

		It("lists only namespace images with scope namespace", func() {
			res := list(map[string]any{"scope": "namespace"})
			Expect(names(res.Out)).To(Equal([]string{"VirtualMachineImage/vmi-a", "VirtualMachineImage/vmi-b"}))
			Expect(calls).To(HaveLen(1))
		})

		It("lists only cluster images with scope cluster", func() {
			res := list(map[string]any{"scope": "cluster"})
			Expect(names(res.Out)).To(Equal([]string{"ClusterVirtualMachineImage/cvmi-x"}))
			Expect(calls).To(Equal([]listCall{{Kind: clusterList}}))
		})

		It("rejects an unknown scope", func() {
			res := list(map[string]any{"scope": "everything"})
			Expect(res.Err).ToNot(BeNil())
		})

		It("continues with cluster images when namespace images fill the page", func() {
			res := list(map[string]any{"limit": 2})
			Expect(names(res.Out)).To(HaveLen(2))
			Expect(res.Out.NextCursor).ToNot(BeEmpty())
			Expect(calls).To(HaveLen(1))

			calls = nil
			res = list(map[string]any{"limit": 2, "cursor": res.Out.NextCursor})
			Expect(names(res.Out)).To(Equal([]string{"ClusterVirtualMachineImage/cvmi-x"}))
			Expect(calls).To(Equal([]listCall{{Kind: clusterList}}))
			Expect(res.Out.NextCursor).To(BeEmpty())
		})

		When("the namespaced list has more pages", func() {
			BeforeEach(func() { nsToken = "ns-token" })

			It("resumes the namespace phase, then moves on to cluster images", func() {
				res := list(map[string]any{})
				Expect(res.Out.NextCursor).ToNot(BeEmpty())
				Expect(calls).To(Equal([]listCall{{Kind: nsList, Namespace: fakeenv.DefaultNamespace}}))

				calls = nil
				res = list(map[string]any{"cursor": res.Out.NextCursor})
				Expect(calls).To(Equal([]listCall{
					{Kind: nsList, Namespace: fakeenv.DefaultNamespace, Continue: "ns-token"},
					{Kind: clusterList},
				}))
				Expect(res.Out.NextCursor).To(BeEmpty())
			})

			When("the continue token has expired", func() {
				BeforeEach(func() { expireNS = true })

				It("returns cursor_expired", func() {
					res := list(map[string]any{})
					res = list(map[string]any{"cursor": res.Out.NextCursor})
					Expect(res.Err).ToNot(BeNil())
					Expect(res.Err.Code).To(Equal(contract.CodeCursorExpired))
				})
			})
		})

		When("the cluster list has more pages", func() {
			BeforeEach(func() { clusterToken = "cluster-token" })

			It("resumes the cluster phase only", func() {
				res := list(map[string]any{})
				Expect(res.Out.NextCursor).ToNot(BeEmpty())

				calls = nil
				list(map[string]any{"cursor": res.Out.NextCursor})
				Expect(calls).To(Equal([]listCall{{Kind: clusterList, Continue: "cluster-token"}}))
			})
		})

		When("the namespace allow-list excludes the namespace", func() {
			BeforeEach(func() { opts.Namespaces = []string{"prod"} })

			It("refuses namespace images but still lists cluster images", func() {
				res := list(map[string]any{"namespace": fakeenv.DefaultNamespace})
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodeNamespaceNotAllowed))

				res = list(map[string]any{"namespace": fakeenv.DefaultNamespace, "scope": "cluster"})
				Expect(res.Err).To(BeNil())
				Expect(names(res.Out)).To(Equal([]string{"ClusterVirtualMachineImage/cvmi-x"}))
			})
		})

		It("never returns OVF property values", func() {
			res := list(map[string]any{})
			Expect(res.Text).ToNot(ContainSubstring(ovfSentinel))
			Expect(fmt.Sprint(res.Raw.StructuredContent)).ToNot(ContainSubstring(ovfSentinel))
		})
	})

	Context("get_virtual_machine_image", func() {
		get := func(args map[string]any) fakeenv.Result[contract.VirtualMachineImageDetail] {
			res, err := fakeenv.Call[contract.VirtualMachineImageDetail](ctx, cs, "get_virtual_machine_image", args)
			Expect(err).ToNot(HaveOccurred())
			return res
		}

		It("gets a namespaced image with OVF keys only", func() {
			res := get(map[string]any{"kind": "VirtualMachineImage", "name": "vmi-a"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Namespace).To(Equal(fakeenv.DefaultNamespace))
			Expect(res.Out.OVFPropertyKeys).To(Equal([]string{"password"}))
			Expect(res.Out.ProductInfo.Value).To(Equal("Canonical Ubuntu 24.04"))
			Expect(res.Out.Conditions).To(HaveLen(1))
			Expect(fmt.Sprint(res.Raw.StructuredContent)).ToNot(ContainSubstring(ovfSentinel))
			Expect(res.Text).ToNot(ContainSubstring(ovfSentinel))
		})

		It("gets a cluster image", func() {
			res := get(map[string]any{"kind": "ClusterVirtualMachineImage", "name": "cvmi-x"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Kind).To(Equal("ClusterVirtualMachineImage"))
			Expect(res.Out.Namespace).To(BeEmpty())
		})

		It("rejects an unknown kind", func() {
			res := get(map[string]any{"kind": "Pod", "name": "x"})
			Expect(res.Err).ToNot(BeNil())
		})

		It("returns not_found for a missing image", func() {
			res := get(map[string]any{"kind": "VirtualMachineImage", "name": "nope"})
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeNotFound))
		})

		When("the allow-list permits only another namespace", func() {
			BeforeEach(func() { opts.Namespaces = []string{"prod"} })

			It("still gets cluster images but refuses the excluded namespace", func() {
				res := get(map[string]any{"kind": "ClusterVirtualMachineImage", "name": "cvmi-x"})
				Expect(res.Err).To(BeNil())

				res = get(map[string]any{"kind": "VirtualMachineImage", "name": "vmi-a"})
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodeNamespaceNotAllowed))
			})
		})
	})
})
