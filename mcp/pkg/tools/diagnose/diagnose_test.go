// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package diagnose_test

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	vmopv1common "github.com/vmware-tanzu/vm-operator/api/v1alpha6/common"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/diagnose"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

func readyConds() []metav1.Condition {
	return []metav1.Condition{{Type: vmopv1.ReadyConditionType, Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}}
}

func smallClass() *vmopv1.VirtualMachineClass {
	return &vmopv1.VirtualMachineClass{ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: "small"}}
}

func image(name string) *vmopv1.VirtualMachineImage {
	img := &vmopv1.VirtualMachineImage{ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: name}}
	img.Status.Conditions = readyConds()
	return img
}

func find(fs []contract.Finding, check string) *contract.Finding {
	for i := range fs {
		if fs[i].Check == check {
			return &fs[i]
		}
	}
	return nil
}

var _ = Describe("diagnose_virtual_machine", Label(testlabels.MCP), func() {
	var (
		ctx        context.Context
		opts       fakeenv.Options
		env        *fakeenv.Env
		cs         *mcp.ClientSession
		vm         *vmopv1.VirtualMachine
		eventLists int
	)

	call := func(args map[string]any) fakeenv.Result[contract.Diagnosis] {
		res, err := fakeenv.Call[contract.Diagnosis](ctx, cs, "diagnose_virtual_machine", args)
		Expect(err).ToNot(HaveOccurred())
		return res
	}

	BeforeEach(func() {
		ctx = context.Background()
		eventLists = 0
		vm = fakeenv.VM("web-01")
		vm.Status.PowerState = vmopv1.VirtualMachinePowerStateOn
		vm.Status.Conditions = readyConds()
		opts = fakeenv.Options{
			Interceptor: interceptor.Funcs{
				List: func(ctx context.Context, c ctrlclient.WithWatch, l ctrlclient.ObjectList, o ...ctrlclient.ListOption) error {
					if _, ok := l.(*corev1.EventList); ok {
						eventLists++
					}
					return c.List(ctx, l, o...)
				},
			},
		}
	})

	JustBeforeEach(func() {
		opts.Objects = append(opts.Objects, vm)
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, diagnose.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	When("the VM and everything it references are healthy", func() {
		BeforeEach(func() {
			opts.Objects = append(opts.Objects, smallClass(), image(vm.Spec.ImageName))
		})

		It("reports healthy with no blocking findings", func() {
			res := call(map[string]any{"name": "web-01"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Healthy).To(BeTrue())
			Expect(res.Out.Findings).To(BeEmpty())
			Expect(res.Out.VM.Name).To(Equal("web-01"))
			Expect(res.Text).To(ContainSubstring("healthy=true"))
			Expect(eventLists).To(Equal(1))
		})

		It("skips the event lookup with noEvents", func() {
			res := call(map[string]any{"name": "web-01", "noEvents": true})
			Expect(res.Err).To(BeNil())
			Expect(eventLists).To(BeZero())
		})
	})

	When("the image does not resolve", func() {
		BeforeEach(func() {
			opts.Objects = append(opts.Objects, smallClass())
			vm.Status.Conditions = append(vm.Status.Conditions, metav1.Condition{
				Type: vmopv1.VirtualMachineConditionImageReady, Status: metav1.ConditionFalse,
				Reason: "NotFound", LastTransitionTime: metav1.Now(),
			})
		})

		It("puts image.not_found first and suggests recreating the VM", func() {
			res := call(map[string]any{"name": "web-01"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Healthy).To(BeFalse())
			Expect(res.Out.Findings).ToNot(BeEmpty())
			top := res.Out.Findings[0]
			Expect(top.Check).To(Equal("image.not_found"))
			Expect(top.Severity).To(Equal(contract.SeverityBlocking))
			Expect(top.Object.Name).To(Equal(vm.Spec.ImageName))
			Expect(top.Suggestion).To(ContainSubstring("list_virtual_machine_images"))
			Expect(top.Suggestion).To(ContainSubstring("recreated"))
			Expect(res.Text).To(ContainSubstring("[blocking] image.not_found"))
		})
	})

	When("the image name is a cluster image", func() {
		BeforeEach(func() {
			cvmi := &vmopv1.ClusterVirtualMachineImage{ObjectMeta: metav1.ObjectMeta{Name: vm.Spec.ImageName}}
			cvmi.Status.Conditions = readyConds()
			opts.Objects = append(opts.Objects, smallClass(), cvmi)
		})

		It("resolves it as a ClusterVirtualMachineImage", func() {
			res := call(map[string]any{"name": "web-01"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Healthy).To(BeTrue())
			Expect(find(res.Out.Findings, "image.not_found")).To(BeNil())
		})
	})

	When("spec.image names a cluster image that is not ready", func() {
		BeforeEach(func() {
			vm.Spec.Image = &vmopv1.VirtualMachineImageRef{Kind: "ClusterVirtualMachineImage", Name: "cvmi-1"}
			cvmi := &vmopv1.ClusterVirtualMachineImage{ObjectMeta: metav1.ObjectMeta{Name: "cvmi-1"}}
			cvmi.Status.Conditions = []metav1.Condition{{Type: vmopv1.ReadyConditionType, Status: metav1.ConditionFalse, Reason: "X", LastTransitionTime: metav1.Now()}}
			opts.Objects = append(opts.Objects, smallClass(), cvmi)
		})

		It("warns that the image is not ready", func() {
			res := call(map[string]any{"name": "web-01"})
			f := find(res.Out.Findings, "image.not_ready")
			Expect(f).ToNot(BeNil())
			Expect(f.Object).To(Equal(contract.ObjectRef{Kind: "ClusterVirtualMachineImage", Name: "cvmi-1"}))
		})
	})

	When("a bootstrap Secret is missing a required key", func() {
		BeforeEach(func() {
			opts.Objects = append(opts.Objects, smallClass(), image(vm.Spec.ImageName))
			vm.Spec.Bootstrap = &vmopv1.VirtualMachineBootstrapSpec{
				CloudInit: &vmopv1.VirtualMachineBootstrapCloudInitSpec{
					RawCloudConfig: &vmopv1common.SecretKeySelector{Name: "web-01-cloud-init", Key: "user-data"},
				},
			}
			vm.Status.Conditions = append(vm.Status.Conditions, metav1.Condition{
				Type: vmopv1.VirtualMachineConditionBootstrapReady, Status: metav1.ConditionFalse,
				Reason: "RequiredKeyNotFound", Message: "required key user-data not found", LastTransitionTime: metav1.Now(),
			})
		})

		It("reports VM Operator's reason without reading the Secret", func() {
			res := call(map[string]any{"name": "web-01"})
			Expect(res.Err).To(BeNil())
			f := find(res.Out.Findings, "condition."+vmopv1.VirtualMachineConditionBootstrapReady)
			Expect(f).ToNot(BeNil())
			Expect(f.Severity).To(Equal(contract.SeverityBlocking))
			Expect(f.Reason).To(Equal("RequiredKeyNotFound"))
			Expect(res.Out.Healthy).To(BeFalse())
			// AfterEach asserts the trap recorded no Secret request.
		})
	})

	When("volume claims are missing or unbound", func() {
		BeforeEach(func() {
			opts.Objects = append(opts.Objects, smallClass(), image(vm.Spec.ImageName),
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: "data"},
					Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
				})
			for _, claim := range []string{"data", "gone"} {
				v := vmopv1.VirtualMachineVolume{Name: claim}
				v.PersistentVolumeClaim = &vmopv1.PersistentVolumeClaimVolumeSource{}
				v.PersistentVolumeClaim.ClaimName = claim
				vm.Spec.Volumes = append(vm.Spec.Volumes, v)
			}
		})

		It("reports each claim", func() {
			res := call(map[string]any{"name": "web-01"})
			Expect(find(res.Out.Findings, "volume.pvc_not_bound").Object.Name).To(Equal("data"))
			Expect(find(res.Out.Findings, "volume.pvc_not_found").Object.Name).To(Equal("gone"))
		})
	})

	When("reading the class is forbidden", func() {
		BeforeEach(func() {
			opts.Objects = append(opts.Objects, image(vm.Spec.ImageName))
			opts.Interceptor.Get = func(ctx context.Context, c ctrlclient.WithWatch, key ctrlclient.ObjectKey, obj ctrlclient.Object, o ...ctrlclient.GetOption) error {
				if _, ok := obj.(*vmopv1.VirtualMachineClass); ok {
					return apierrors.NewForbidden(schema.GroupResource{Group: "vmoperator.vmware.com", Resource: "virtualmachineclasses"},
						key.Name, fmt.Errorf("denied"))
				}
				return c.Get(ctx, key, obj, o...)
			}
		})

		It("returns an info finding rather than a tool error", func() {
			res := call(map[string]any{"name": "web-01"})
			Expect(res.Err).To(BeNil())
			f := find(res.Out.Findings, "class.unavailable")
			Expect(f).ToNot(BeNil())
			Expect(f.Severity).To(Equal(contract.SeverityInfo))
			Expect(res.Out.Healthy).To(BeTrue())
		})
	})

	When("there are warning events", func() {
		BeforeEach(func() {
			opts.Objects = append(opts.Objects, smallClass(), image(vm.Spec.ImageName),
				&corev1.Event{
					ObjectMeta:     metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: "e1"},
					InvolvedObject: corev1.ObjectReference{Kind: "VirtualMachine", Name: "web-01"},
					Type:           corev1.EventTypeWarning, Reason: "CreateFailed", Message: "boom",
					LastTimestamp: metav1.Now(),
				},
				&corev1.Event{
					ObjectMeta:     metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: "e2"},
					InvolvedObject: corev1.ObjectReference{Kind: "VirtualMachine", Name: "other"},
					Type:           corev1.EventTypeWarning, Reason: "OtherVM", Message: "nope",
					LastTimestamp: metav1.Now(),
				})
		})

		It("reports only the VM's warning events", func() {
			res := call(map[string]any{"name": "web-01"})
			f := find(res.Out.Findings, "event.warning")
			Expect(f).ToNot(BeNil())
			Expect(f.Reason).To(Equal("CreateFailed"))
			Expect(f.Evidence).To(Equal([]string{"boom"}))
			for _, x := range res.Out.Findings {
				Expect(x.Reason).ToNot(Equal("OtherVM"))
			}
		})
	})

	It("returns not_found for a missing VM", func() {
		res := call(map[string]any{"name": "missing"})
		Expect(res.Err).ToNot(BeNil())
		Expect(res.Err.Code).To(Equal(contract.CodeNotFound))
	})
})
