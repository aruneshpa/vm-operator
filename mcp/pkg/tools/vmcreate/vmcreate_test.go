// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmcreate_test

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmcreate"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmpower"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

const (
	className = "small"
	imageName = "vmi-0123456789abcdef0"
	scName    = "wcp-storage"
)

func checks(fs []contract.Finding) map[string]string {
	out := map[string]string{}
	for _, f := range fs {
		out[f.Check] = f.Severity
	}
	return out
}

var _ = Describe("create_virtual_machine", Label(testlabels.MCP), func() {
	var (
		ctx        context.Context
		opts       fakeenv.Options
		env        *fakeenv.Env
		cs         *mcp.ClientSession
		creates    int
		createOpts []*ctrlclient.CreateOptions
		args       map[string]any
	)

	BeforeEach(func() {
		ctx = context.Background()
		creates = 0
		createOpts = nil
		args = map[string]any{
			"name":         "web-01",
			"className":    className,
			"imageName":    imageName,
			"storageClass": scName,
			"dryRun":       true,
		}
		opts = fakeenv.Options{
			EnableWrite: true,
			Objects: []ctrlclient.Object{
				&vmopv1.VirtualMachineClass{ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: className}},
				&vmopv1.VirtualMachineImage{ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: imageName}},
				&corev1.ResourceQuota{
					ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: "quota"},
					Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
						scName + ".storageclass.storage.k8s.io/requests.storage": resource.MustParse("100Gi"),
					}},
				},
			},
			Interceptor: interceptor.Funcs{
				Create: func(ctx context.Context, c ctrlclient.WithWatch, o ctrlclient.Object, opt ...ctrlclient.CreateOption) error {
					if _, ok := o.(*vmopv1.VirtualMachine); ok {
						creates++
						co := &ctrlclient.CreateOptions{}
						co.ApplyOptions(opt)
						createOpts = append(createOpts, co)
					}
					return c.Create(ctx, o, opt...)
				},
			},
		}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, vmcreate.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	call := func() fakeenv.Result[contract.CreateVirtualMachineOutput] {
		res, err := fakeenv.Call[contract.CreateVirtualMachineOutput](ctx, cs, "create_virtual_machine", args)
		Expect(err).ToNot(HaveOccurred())
		return res
	}

	stored := func() (*vmopv1.VirtualMachine, error) {
		v := &vmopv1.VirtualMachine{}
		return v, env.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: fakeenv.DefaultNamespace, Name: "web-01"}, v)
	}

	Context("validation", func() {
		DescribeTable("rejects bad input before any API call",
			func(mutate func(map[string]any)) {
				mutate(args)
				res := call()
				Expect(res.Err).ToNot(BeNil())
				Expect(creates).To(BeZero())
			},
			Entry("invalid DNS name", func(a map[string]any) { a["name"] = "Web_01" }),
			Entry("missing className", func(a map[string]any) { a["className"] = "" }),
			Entry("missing storageClass", func(a map[string]any) { a["storageClass"] = "" }),
			Entry("bad bootstrap provider", func(a map[string]any) {
				a["bootstrap"] = map[string]any{"provider": "ignition", "secretName": "s"}
			}),
			Entry("bootstrap without secretName", func(a map[string]any) {
				a["bootstrap"] = map[string]any{"provider": "cloudInit", "secretName": ""}
			}),
			Entry("bad power state", func(a map[string]any) { a["powerState"] = "Suspended" }),
		)

		It("requires dryRun in the input schema", func() {
			delete(args, "dryRun")
			res := call()
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal("protocol"))
			Expect(creates).To(BeZero())
		})
	})

	Context("preflight", func() {
		When("the name is taken", func() {
			BeforeEach(func() { opts.Objects = append(opts.Objects, fakeenv.VM("web-01")) })

			It("blocks", func() {
				res := call()
				Expect(res.Err).To(BeNil())
				Expect(res.Out.Admitted).To(BeFalse())
				Expect(checks(res.Out.Preflight)).To(HaveKeyWithValue("create.name_taken", contract.SeverityBlocking))
				Expect(creates).To(BeZero())
			})
		})

		It("blocks a missing class without calling Create", func() {
			args["className"] = "huge"
			res := call()
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Admitted).To(BeFalse())
			Expect(checks(res.Out.Preflight)).To(HaveKeyWithValue("class.not_found", contract.SeverityBlocking))
			Expect(creates).To(BeZero())
		})

		It("only warns when the image is not found by object name", func() {
			args["imageName"] = "ubuntu-24.04"
			res := call()
			Expect(res.Err).To(BeNil())
			Expect(checks(res.Out.Preflight)).To(HaveKeyWithValue("image.not_found_by_name", contract.SeverityWarning))
			Expect(res.Out.Admitted).To(BeTrue())
		})

		It("blocks a storage class not assigned to the namespace", func() {
			args["storageClass"] = "gold"
			res := call()
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Admitted).To(BeFalse())
			Expect(checks(res.Out.Preflight)).To(HaveKeyWithValue("storage.not_assigned", contract.SeverityBlocking))
			Expect(creates).To(BeZero())
		})

		When("the namespace has no storage quota", func() {
			BeforeEach(func() { opts.Objects = opts.Objects[:2] })

			It("reports the storage class as unverified", func() {
				res := call()
				Expect(res.Err).To(BeNil())
				Expect(checks(res.Out.Preflight)).To(HaveKeyWithValue("storage.unverified", contract.SeverityInfo))
				Expect(res.Out.Admitted).To(BeTrue())
			})
		})
	})

	Context("create", func() {
		It("dry-runs without persisting and returns the projected VM", func() {
			res := call()
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Admitted).To(BeTrue())
			Expect(res.Out.DryRun).To(BeTrue())
			Expect(res.Out.Preflight).To(BeEmpty())
			Expect(res.Out.VM).ToNot(BeNil())
			Expect(res.Out.VM.Name).To(Equal("web-01"))
			Expect(res.Out.VM.ClassName).To(Equal(className))
			Expect(res.Out.VM.StorageClass).To(Equal(scName))
			Expect(res.Out.VM.PowerState.Desired).To(Equal("PoweredOn"))
			Expect(createOpts).To(HaveLen(1))
			Expect(createOpts[0].DryRun).To(Equal([]string{metav1.DryRunAll}))

			_, err := stored()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("creates for real with the vmop-mcp field owner", func() {
			args["dryRun"] = false
			args["powerState"] = "PoweredOff"
			args["labels"] = map[string]any{"app": "web"}
			res := call()
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Admitted).To(BeTrue())
			Expect(createOpts).To(HaveLen(1))
			Expect(createOpts[0].DryRun).To(BeEmpty())
			Expect(createOpts[0].FieldManager).To(Equal(vmpower.FieldOwner))

			v, err := stored()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Spec.ClassName).To(Equal(className))
			Expect(v.Spec.ImageName).To(Equal(imageName))
			Expect(v.Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
			Expect(v.Labels).To(HaveKeyWithValue("app", "web"))
		})

		It("turns network names into eth0..ethN interfaces", func() {
			args["dryRun"] = false
			args["networkInterfaces"] = []string{"net-a", "net-b"}
			Expect(call().Err).To(BeNil())
			v, err := stored()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Spec.Network).ToNot(BeNil())
			Expect(v.Spec.Network.Interfaces).To(HaveLen(2))
			Expect(v.Spec.Network.Interfaces[0].Name).To(Equal("eth0"))
			Expect(v.Spec.Network.Interfaces[0].Network.Name).To(Equal("net-a"))
			Expect(v.Spec.Network.Interfaces[1].Name).To(Equal("eth1"))
			Expect(v.Spec.Network.Interfaces[1].Network.Name).To(Equal("net-b"))
		})

		It("references a cloud-init Secret with the default key without reading it", func() {
			args["dryRun"] = false
			args["bootstrap"] = map[string]any{"provider": "cloudInit", "secretName": "my-ci"}
			res := call()
			Expect(res.Err).To(BeNil())
			v, err := stored()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Spec.Bootstrap.CloudInit.RawCloudConfig.Name).To(Equal("my-ci"))
			Expect(v.Spec.Bootstrap.CloudInit.RawCloudConfig.Key).To(Equal("user-data"))
			Expect(res.Out.VM.Bootstrap.SecretRefs).To(ContainElement(contract.SecretRef{
				Name: "my-ci", Key: "user-data", FieldPath: "spec.bootstrap.cloudInit.rawCloudConfig",
			}))
		})

		It("references a sysprep Secret with the default key without reading it", func() {
			args["dryRun"] = false
			args["bootstrap"] = map[string]any{"provider": "sysprep", "secretName": "my-sp"}
			Expect(call().Err).To(BeNil())
			v, err := stored()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Spec.Bootstrap.Sysprep.RawSysprep.Name).To(Equal("my-sp"))
			Expect(v.Spec.Bootstrap.Sysprep.RawSysprep.Key).To(Equal("unattend"))
		})

		It("honors an explicit bootstrap key", func() {
			args["dryRun"] = false
			args["bootstrap"] = map[string]any{"provider": "cloudInit", "secretName": "my-ci", "key": "custom"}
			Expect(call().Err).To(BeNil())
			v, err := stored()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Spec.Bootstrap.CloudInit.RawCloudConfig.Key).To(Equal("custom"))
		})
	})

	When("the write tier is disabled", func() {
		BeforeEach(func() { opts.EnableWrite = false })

		It("is not registered", func() {
			res, err := cs.ListTools(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Tools).To(BeEmpty())
		})
	})
})
