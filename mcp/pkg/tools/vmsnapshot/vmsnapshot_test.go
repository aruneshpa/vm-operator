// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package vmsnapshot_test

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vmsnapshot"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

func snapshot(name, vm string, ready bool) *vmopv1.VirtualMachineSnapshot {
	s := &vmopv1.VirtualMachineSnapshot{ObjectMeta: metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: name}}
	s.Spec.VMName = vm
	s.Spec.Memory = true
	s.Spec.Quiesce = &vmopv1.QuiesceSpec{}
	s.Spec.Description = "before upgrade"
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	s.Status.Conditions = []metav1.Condition{{
		Type: vmopv1.VirtualMachineSnapshotReadyCondition, Status: status, Reason: "R", LastTransitionTime: metav1.Now(),
	}}
	return s
}

var _ = Describe("VM snapshot tools", Label(testlabels.MCP), func() {
	var (
		ctx  context.Context
		opts fakeenv.Options
		env  *fakeenv.Env
		cs   *mcp.ClientSession
	)

	BeforeEach(func() {
		ctx = context.Background()
		opts = fakeenv.Options{Objects: []ctrlclient.Object{
			snapshot("snap-1", "web-01", true),
			snapshot("snap-2", "web-01", false),
			snapshot("snap-3", "db-01", true),
		}}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, vmsnapshot.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	tools := func() map[string]string {
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		out := map[string]string{}
		for _, t := range res.Tools {
			out[t.Name] = t.Description
		}
		return out
	}

	When("the capability is active", func() {
		It("registers the tools without a caveat", func() {
			t := tools()
			Expect(t).To(HaveKey("list_virtual_machine_snapshots"))
			Expect(t).To(HaveKey("get_virtual_machine_snapshot"))
			Expect(t["list_virtual_machine_snapshots"]).ToNot(ContainSubstring("may be disabled"))
		})

		It("lists snapshots, optionally for one VM", func() {
			res, err := fakeenv.Call[contract.VirtualMachineSnapshotList](ctx, cs, "list_virtual_machine_snapshots", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Out.Items).To(HaveLen(3))

			res, err = fakeenv.Call[contract.VirtualMachineSnapshotList](ctx, cs, "list_virtual_machine_snapshots",
				map[string]any{"vmName": "web-01"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Out.Items).To(HaveLen(2))
			for _, s := range res.Out.Items {
				Expect(s.VMName).To(Equal("web-01"))
			}
		})

		It("gets a snapshot", func() {
			res, err := fakeenv.Call[contract.VirtualMachineSnapshotDetail](ctx, cs, "get_virtual_machine_snapshot",
				map[string]any{"name": "snap-1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Memory).To(BeTrue())
			Expect(res.Out.Quiesce).To(BeTrue())
			Expect(res.Out.Ready).To(Equal("True"))
			Expect(res.Out.Description.Value).To(Equal("before upgrade"))
			Expect(res.Out.Conditions).To(HaveLen(1))
		})

		It("returns not_found for a missing snapshot", func() {
			res, err := fakeenv.Call[contract.VirtualMachineSnapshotDetail](ctx, cs, "get_virtual_machine_snapshot",
				map[string]any{"name": "nope"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err.Code).To(Equal(contract.CodeNotFound))
		})
	})

	When("the capability is unknown", func() {
		BeforeEach(func() {
			opts.Capabilities = map[string]string{toolkit.CapabilityVMSnapshots: toolkit.CapabilityUnknown}
		})

		It("registers the tools and notes the feature may be disabled", func() {
			t := tools()
			Expect(t).To(HaveKey("get_virtual_machine_snapshot"))
			Expect(t["list_virtual_machine_snapshots"]).To(ContainSubstring("may be disabled"))
		})
	})

	When("the capability is absent from the environment", func() {
		BeforeEach(func() { opts.Capabilities = map[string]string{} })

		It("treats it as unknown and registers the tools", func() {
			Expect(tools()).To(HaveKey("list_virtual_machine_snapshots"))
		})
	})

	When("the capability is inactive", func() {
		BeforeEach(func() {
			opts.Capabilities = map[string]string{toolkit.CapabilityVMSnapshots: toolkit.CapabilityInactive}
		})

		It("does not register the tools", func() {
			t := tools()
			Expect(t).ToNot(HaveKey("list_virtual_machine_snapshots"))
			Expect(t).ToNot(HaveKey("get_virtual_machine_snapshot"))
		})
	})
})
