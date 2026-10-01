// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmpower_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmpower"
	"github.com/vmware-tanzu/vm-operator/mcp/test/envtest"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

var vmGR = schema.GroupResource{Group: kube.VMOperatorGroup, Resource: "virtualmachines"}

var _ = Describe("VM power tools", Label(testlabels.MCP), func() {
	var (
		ctx  context.Context
		opts fakeenv.Options
		env  *fakeenv.Env
		cs   *mcp.ClientSession

		gets, patches atomic.Int32
		patchOpts     []*ctrlclient.PatchOptions
		conflicts     int
	)

	BeforeEach(func() {
		ctx = context.Background()
		gets.Store(0)
		patches.Store(0)
		conflicts = 0
		patchOpts = nil
		v := fakeenv.VM("web-01")
		v.Status.PowerState = vmopv1.VirtualMachinePowerStateOn
		opts = fakeenv.Options{EnableWrite: true, Objects: []ctrlclient.Object{v}}
		opts.Interceptor = interceptor.Funcs{
			Get: func(ctx context.Context, c ctrlclient.WithWatch, k ctrlclient.ObjectKey, o ctrlclient.Object, opt ...ctrlclient.GetOption) error {
				if _, ok := o.(*vmopv1.VirtualMachine); ok {
					gets.Add(1)
				}
				return c.Get(ctx, k, o, opt...)
			},
			Patch: func(ctx context.Context, c ctrlclient.WithWatch, o ctrlclient.Object, p ctrlclient.Patch, opt ...ctrlclient.PatchOption) error {
				patches.Add(1)
				po := &ctrlclient.PatchOptions{}
				po.ApplyOptions(opt)
				patchOpts = append(patchOpts, po)
				if conflicts > 0 {
					conflicts--
					return apierrors.NewConflict(vmGR, o.GetName(), fmt.Errorf("the object has been modified"))
				}
				return c.Patch(ctx, o, p, opt...)
			},
		}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, vmpower.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	stored := func() *vmopv1.VirtualMachine {
		v := &vmopv1.VirtualMachine{}
		Expect(env.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: fakeenv.DefaultNamespace, Name: "web-01"}, v)).To(Succeed())
		return v
	}

	Context("registration", func() {
		It("sets the annotations per the contract", func() {
			res, err := cs.ListTools(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			ann := map[string]*mcp.ToolAnnotations{}
			for _, t := range res.Tools {
				ann[t.Name] = t.Annotations
			}
			Expect(ann).To(HaveLen(3))
			for _, a := range ann {
				Expect(a.DestructiveHint).ToNot(BeNil())
				Expect(*a.DestructiveHint).To(BeFalse())
				Expect(a.OpenWorldHint).ToNot(BeNil())
				Expect(*a.OpenWorldHint).To(BeFalse())
			}
			Expect(ann["set_virtual_machine_power_state"].ReadOnlyHint).To(BeFalse())
			Expect(ann["set_virtual_machine_power_state"].IdempotentHint).To(BeTrue())
			Expect(ann["restart_virtual_machine"].ReadOnlyHint).To(BeFalse())
			Expect(ann["restart_virtual_machine"].IdempotentHint).To(BeFalse())
			Expect(ann["wait_for_virtual_machine"].ReadOnlyHint).To(BeTrue())
		})

		When("the write tier is disabled", func() {
			BeforeEach(func() { opts.EnableWrite = false })

			It("registers no tools", func() {
				res, err := cs.ListTools(ctx, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Tools).To(BeEmpty())
			})
		})
	})

	Context("set_virtual_machine_power_state", func() {
		call := func(args map[string]any) fakeenv.Result[contract.SetPowerStateOutput] {
			args["name"] = "web-01"
			res, err := fakeenv.Call[contract.SetPowerStateOutput](ctx, cs, "set_virtual_machine_power_state", args)
			Expect(err).ToNot(HaveOccurred())
			return res
		}

		It("maps a power-off mode to powerOffMode", func() {
			res := call(map[string]any{"state": "PoweredOff", "mode": "Soft"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Changed).To(BeTrue())
			v := stored()
			Expect(v.Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
			Expect(v.Spec.PowerOffMode).To(Equal(vmopv1.VirtualMachinePowerOpModeSoft))
			Expect(v.Spec.SuspendMode).To(BeEmpty())
			Expect(patchOpts).To(HaveLen(1))
			Expect(patchOpts[0].FieldManager).To(Equal(vmpower.FieldOwner))
			Expect(patchOpts[0].DryRun).To(BeEmpty())
		})

		It("maps a suspend mode to suspendMode", func() {
			res := call(map[string]any{"state": "Suspended", "mode": "Hard"})
			Expect(res.Err).To(BeNil())
			v := stored()
			Expect(v.Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateSuspended))
			Expect(v.Spec.SuspendMode).To(Equal(vmopv1.VirtualMachinePowerOpModeHard))
			Expect(v.Spec.PowerOffMode).To(BeEmpty())
		})

		It("rejects a mode with PoweredOn", func() {
			res := call(map[string]any{"state": "PoweredOn", "mode": "Soft"})
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeInvalid))
			Expect(patches.Load()).To(BeZero())
		})

		It("rejects a state outside the enum", func() {
			res := call(map[string]any{"state": "Off"})
			Expect(res.Err).ToNot(BeNil())
			Expect(patches.Load()).To(BeZero())
		})

		It("does not patch when the VM is already in the desired state", func() {
			res := call(map[string]any{"state": "PoweredOn"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Changed).To(BeFalse())
			Expect(patches.Load()).To(BeZero())
		})

		It("passes DryRunAll and leaves the stored object unchanged on dry run", func() {
			res := call(map[string]any{"state": "PoweredOff", "dryRun": true})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Changed).To(BeTrue())
			Expect(res.Out.DryRun).To(BeTrue())
			Expect(patchOpts).To(HaveLen(1))
			Expect(patchOpts[0].DryRun).To(Equal([]string{metav1.DryRunAll}))
			Expect(stored().Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOn))
		})

		When("the patch conflicts once", func() {
			BeforeEach(func() { conflicts = 1 })

			It("re-reads and retries", func() {
				res := call(map[string]any{"state": "PoweredOff"})
				Expect(res.Err).To(BeNil())
				Expect(res.Out.Changed).To(BeTrue())
				Expect(gets.Load()).To(BeEquivalentTo(2))
				Expect(patches.Load()).To(BeEquivalentTo(2))
				Expect(stored().Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
			})
		})

		When("the patch keeps conflicting", func() {
			BeforeEach(func() { conflicts = 100 })

			It("gives up with a conflict error", func() {
				res := call(map[string]any{"state": "PoweredOff"})
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodeConflict))
				Expect(patches.Load()).To(BeEquivalentTo(vmpower.MaxAttempts))
			})
		})

		When("the VM belongs to a group", func() {
			BeforeEach(func() {
				v := opts.Objects[0].(*vmopv1.VirtualMachine)
				v.Spec.GroupName = "my-group"
			})

			It("refuses and names the group", func() {
				res := call(map[string]any{"state": "PoweredOff"})
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodePreconditionFailed))
				Expect(res.Err.Message).To(ContainSubstring("my-group"))
				Expect(patches.Load()).To(BeZero())
			})

			It("proceeds with force", func() {
				res := call(map[string]any{"state": "PoweredOff", "force": true})
				Expect(res.Err).To(BeNil())
				Expect(stored().Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
			})
		})
	})

	Context("restart_virtual_machine", func() {
		call := func(args map[string]any) fakeenv.Result[contract.RestartOutput] {
			args["name"] = "web-01"
			res, err := fakeenv.Call[contract.RestartOutput](ctx, cs, "restart_virtual_machine", args)
			Expect(err).ToNot(HaveOccurred())
			return res
		}

		It("sets nextRestartTime and restartMode", func() {
			res := call(map[string]any{"mode": "Soft"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Requested).To(BeTrue())
			Expect(res.Out.NextRestartTime).To(Equal("now"))
			v := stored()
			Expect(v.Spec.NextRestartTime).To(Equal("now"))
			Expect(v.Spec.RestartMode).To(Equal(vmopv1.VirtualMachinePowerOpModeSoft))
		})

		It("issues a patch on every call", func() {
			Expect(call(map[string]any{}).Err).To(BeNil())
			// Simulate the mutating webhook converting "now" to a timestamp.
			v := stored()
			v.Spec.NextRestartTime = "2026-09-30T00:00:00Z"
			Expect(env.Client.Update(ctx, v)).To(Succeed())
			Expect(call(map[string]any{}).Err).To(BeNil())
			Expect(patches.Load()).To(BeEquivalentTo(2))
		})

		When("the VM is not powered on", func() {
			BeforeEach(func() {
				opts.Objects[0].(*vmopv1.VirtualMachine).Status.PowerState = vmopv1.VirtualMachinePowerStateOff
			})

			It("refuses", func() {
				res := call(map[string]any{})
				Expect(res.Err).ToNot(BeNil())
				Expect(res.Err.Code).To(Equal(contract.CodePreconditionFailed))
				Expect(patches.Load()).To(BeZero())
			})
		})
	})

	Context("wait_for_virtual_machine", func() {
		var saved time.Duration
		BeforeEach(func() {
			saved = vmpower.PollInterval
			vmpower.PollInterval = 10 * time.Millisecond
			opts.Objects[0].(*vmopv1.VirtualMachine).Status.Conditions = []metav1.Condition{
				{Type: vmopv1.ReadyConditionType, Status: metav1.ConditionTrue},
			}
		})
		AfterEach(func() { vmpower.PollInterval = saved })

		call := func(args map[string]any) fakeenv.Result[contract.WaitOutput] {
			args["name"] = "web-01"
			res, err := fakeenv.Call[contract.WaitOutput](ctx, cs, "wait_for_virtual_machine", args)
			Expect(err).ToNot(HaveOccurred())
			return res
		}

		It("is satisfied by a power state", func() {
			res := call(map[string]any{"powerState": "PoweredOn"})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Satisfied).To(BeTrue())
		})

		It("is satisfied by a condition", func() {
			res := call(map[string]any{"condition": map[string]any{"type": "Ready", "status": "True"}})
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Satisfied).To(BeTrue())
		})

		When("the VM is not yet in the requested state", func() {
			BeforeEach(func() {
				opts.Objects[0].(*vmopv1.VirtualMachine).Status.PowerState = vmopv1.VirtualMachinePowerStateOff
			})

			It("waits until the state is reached", func() {
				done := make(chan fakeenv.Result[contract.WaitOutput], 1)
				go func() {
					defer GinkgoRecover()
					done <- call(map[string]any{"powerState": "PoweredOn", "timeoutSeconds": 30})
				}()
				Eventually(gets.Load).Should(BeNumerically(">=", 2))
				v := stored()
				v.Status.PowerState = vmopv1.VirtualMachinePowerStateOn
				Expect(env.Client.Status().Update(ctx, v)).To(Succeed())
				var res fakeenv.Result[contract.WaitOutput]
				Eventually(done).WithTimeout(10 * time.Second).Should(Receive(&res))
				Expect(res.Err).To(BeNil())
				Expect(res.Out.Satisfied).To(BeTrue())
			})
		})

		It("times out", func() {
			res := call(map[string]any{"powerState": "Suspended", "timeoutSeconds": 1})
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeTimeout))
		})

		It("requires exactly one of powerState or condition", func() {
			res := call(map[string]any{})
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeInvalid))

			res = call(map[string]any{"powerState": "PoweredOn",
				"condition": map[string]any{"type": "Ready", "status": "True"}})
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeInvalid))
		})
	})
})

var _ = Describe("VM power tools against an API server", Label(testlabels.MCP, testlabels.EnvTest), func() {
	const ns = "power"

	var (
		ctx context.Context
		vm  *vmopv1.VirtualMachine
	)

	BeforeEach(func() {
		if testEnv == nil {
			Skip("KUBEBUILDER_ASSETS is not set")
		}
		ctx = context.Background()
		err := testEnv.CreateNamespace(ctx, ns)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).ToNot(HaveOccurred())
		}
		vm = &vmopv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, GenerateName: "vm-"},
			Spec: vmopv1.VirtualMachineSpec{
				ClassName:    "small",
				ImageName:    "vmi-0123456789abcdef0",
				StorageClass: "wcp-storage",
				PowerState:   vmopv1.VirtualMachinePowerStateOn,
			},
		}
		Expect(testEnv.Admin.Create(ctx, vm)).To(Succeed())
	})

	connect := func(c ctrlclient.Client, user *envtest.User) *mcp.ClientSession {
		kc, err := kubernetes.NewForConfig(user.Config)
		Expect(err).ToNot(HaveOccurred())
		e := &toolkit.Env{
			Provider:    kube.NewStaticProvider(&kube.Clients{Client: c, Kube: kc}, kube.Info{Namespace: ns}),
			Namespaces:  toolkit.NewNamespacePolicy(ns, nil),
			EnableWrite: true,
		}
		s := mcp.NewServer(&mcp.Implementation{Name: "vmop-mcp", Version: "test"}, nil)
		vmpower.Register(toolkit.NewRegistrar(s, e))
		cs, err := fakeenv.Connect(ctx, s)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(cs.Close)
		return cs
	}

	userClient := func(user *envtest.User) ctrlclient.WithWatch {
		c, err := ctrlclient.NewWithWatch(user.Config, ctrlclient.Options{Scheme: kube.NewScheme()})
		Expect(err).ToNot(HaveOccurred())
		return c
	}

	addUser := func(name string, rules []rbacv1.PolicyRule) *envtest.User {
		u, err := testEnv.AddUser(ctx, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()), ns, GinkgoT().TempDir(), rules)
		Expect(err).ToNot(HaveOccurred())
		return u
	}

	It("patches the power state as an editing user", func() {
		u := addUser("editor", envtest.VMServiceEditRules)
		cs := connect(userClient(u), u)
		res, err := fakeenv.Call[contract.SetPowerStateOutput](ctx, cs, "set_virtual_machine_power_state",
			map[string]any{"name": vm.Name, "state": "PoweredOff", "mode": "Hard"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		Expect(res.Out.Changed).To(BeTrue())

		got := &vmopv1.VirtualMachine{}
		Expect(testEnv.Admin.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), got)).To(Succeed())
		Expect(got.Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
		Expect(got.Spec.PowerOffMode).To(Equal(vmopv1.VirtualMachinePowerOpModeHard))
		managers := []string{}
		for _, mf := range got.ManagedFields {
			managers = append(managers, mf.Manager)
		}
		Expect(managers).To(ContainElement(vmpower.FieldOwner))
	})

	It("retries a real optimistic-lock conflict without overwriting the concurrent change", func() {
		u := addUser("racer", envtest.VMServiceEditRules)
		gets := 0
		c := interceptor.NewClient(userClient(u), interceptor.Funcs{
			Get: func(ctx context.Context, c ctrlclient.WithWatch, k ctrlclient.ObjectKey, o ctrlclient.Object, opt ...ctrlclient.GetOption) error {
				if err := c.Get(ctx, k, o, opt...); err != nil {
					return err
				}
				gets++
				if gets == 1 {
					// A concurrent writer changes the VM after our read.
					concurrent := &vmopv1.VirtualMachine{}
					Expect(testEnv.Admin.Get(ctx, k, concurrent)).To(Succeed())
					base := concurrent.DeepCopy()
					concurrent.Labels = map[string]string{"concurrent": "writer"}
					Expect(testEnv.Admin.Patch(ctx, concurrent, ctrlclient.MergeFrom(base))).To(Succeed())
				}
				return nil
			},
		})
		cs := connect(c, u)
		res, err := fakeenv.Call[contract.SetPowerStateOutput](ctx, cs, "set_virtual_machine_power_state",
			map[string]any{"name": vm.Name, "state": "PoweredOff"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		Expect(gets).To(Equal(2))

		got := &vmopv1.VirtualMachine{}
		Expect(testEnv.Admin.Get(ctx, ctrlclient.ObjectKeyFromObject(vm), got)).To(Succeed())
		Expect(got.Spec.PowerState).To(Equal(vmopv1.VirtualMachinePowerStateOff))
		Expect(got.Labels).To(HaveKeyWithValue("concurrent", "writer"))
	})

	It("returns forbidden for a read-only user", func() {
		u := addUser("viewer", envtest.VMServiceReadRules)
		cs := connect(userClient(u), u)
		res, err := fakeenv.Call[contract.SetPowerStateOutput](ctx, cs, "set_virtual_machine_power_state",
			map[string]any{"name": vm.Name, "state": "PoweredOff"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).ToNot(BeNil())
		Expect(res.Err.Code).To(Equal(contract.CodeForbidden))
	})
})
