// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/server"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

var update = flag.Bool("update", false, "rewrite testdata/tools_list.golden.json")

const goldenPath = "testdata/tools_list.golden.json"

var readTools = []string{
	"check_access",
	"diagnose_virtual_machine",
	"explain_field",
	"get_events",
	"get_virtual_machine",
	"get_virtual_machine_class",
	"get_virtual_machine_image",
	"get_virtual_machine_snapshot",
	"list_storage_classes",
	"list_virtual_machine_classes",
	"list_virtual_machine_images",
	"list_virtual_machine_snapshots",
	"list_virtual_machines",
	"whoami",
}

var writeTools = []string{
	"create_virtual_machine",
	"restart_virtual_machine",
	"set_virtual_machine_power_state",
	"wait_for_virtual_machine",
}

// hints is the annotation table from model.md §3:
// readOnly, destructive, idempotent, openWorld.
type hints struct{ readOnly, destructive, idempotent, openWorld bool }

var annotationTable = map[string]hints{
	"set_virtual_machine_power_state": {false, false, true, false},
	"restart_virtual_machine":         {false, false, false, false},
	"create_virtual_machine":          {false, false, false, false},
	"wait_for_virtual_machine":        {true, false, true, false},
}

func connect(ctx context.Context, enableWrite bool, caps map[string]string) *mcp.ClientSession {
	env := fakeenv.New(fakeenv.Options{EnableWrite: enableWrite, Capabilities: caps})
	cs, err := fakeenv.Connect(ctx, server.NewWithEnv(env.Env, nil))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	DeferCleanup(cs.Close)
	return cs
}

func toolNames(tools []*mcp.Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

var _ = Describe("Server", Label(testlabels.MCP), func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("matches the golden tools/list contract", func() {
		cs := connect(ctx, true, map[string]string{toolkit.CapabilityVMSnapshots: toolkit.CapabilityActive})
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		got, err := json.MarshalIndent(res.Tools, "", "  ")
		Expect(err).ToNot(HaveOccurred())
		got = append(got, '\n')

		if *update {
			Expect(os.MkdirAll(filepath.Dir(goldenPath), 0o755)).To(Succeed())
			Expect(os.WriteFile(goldenPath, got, 0o600)).To(Succeed())
		}
		want, err := os.ReadFile(goldenPath)
		Expect(err).ToNot(HaveOccurred(), "run with -update to create the golden file")
		Expect(string(got)).To(Equal(string(want)),
			"the tool contract changed; review the diff and run go test ./pkg/server/ -update")
	})

	It("offers exactly the read tools when the write tier is disabled", func() {
		cs := connect(ctx, false, nil)
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(toolNames(res.Tools)).To(ConsistOf(readTools))
	})

	It("adds exactly the write tools when the write tier is enabled", func() {
		cs := connect(ctx, true, nil)
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(toolNames(res.Tools)).To(ConsistOf(append(append([]string{}, readTools...), writeTools...)))
	})

	It("omits snapshot tools when the capability is inactive", func() {
		cs := connect(ctx, false, map[string]string{toolkit.CapabilityVMSnapshots: toolkit.CapabilityInactive})
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(toolNames(res.Tools)).ToNot(ContainElement(ContainSubstring("snapshot")))
	})

	It("sets every annotation explicitly and per the contract table", func() {
		cs := connect(ctx, true, nil)
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		for _, t := range res.Tools {
			a := t.Annotations
			Expect(a).ToNot(BeNil(), t.Name)
			Expect(a.DestructiveHint).ToNot(BeNil(), t.Name)
			Expect(a.OpenWorldHint).ToNot(BeNil(), t.Name)
			want, ok := annotationTable[t.Name]
			if !ok {
				want = hints{readOnly: true, destructive: false, idempotent: true, openWorld: false}
			}
			Expect(hints{a.ReadOnlyHint, *a.DestructiveHint, a.IdempotentHint, *a.OpenWorldHint}).To(Equal(want), t.Name)
			Expect(a.Title).ToNot(BeEmpty(), t.Name)
		}
	})

	It("ends every description with the data-not-instructions notice", func() {
		cs := connect(ctx, true, nil)
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		for _, t := range res.Tools {
			Expect(strings.HasSuffix(t.Description, contract.DataNotInstructions)).To(BeTrue(), t.Name)
		}
	})

	It("advertises the implementation and instructions", func() {
		cs := connect(ctx, false, nil)
		init := cs.InitializeResult()
		Expect(init.ServerInfo.Name).To(Equal(server.Name))
		Expect(init.Instructions).To(Equal(server.Instructions))
	})

	It("lists create-vm only with the write tier", func() {
		cs := connect(ctx, false, nil)
		res, err := cs.ListPrompts(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		var names []string
		for _, p := range res.Prompts {
			names = append(names, p.Name)
		}
		Expect(names).To(ConsistOf("troubleshoot-vm"))

		cs = connect(ctx, true, nil)
		res, err = cs.ListPrompts(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		names = nil
		for _, p := range res.Prompts {
			names = append(names, p.Name)
		}
		Expect(names).To(ConsistOf("troubleshoot-vm", "create-vm"))
	})
})

var _ = Describe("Server against an API server", Label(testlabels.MCP, testlabels.EnvTest), func() {
	It("connects with a kubeconfig, verifies served versions, and detects capabilities", func() {
		if testEnv == nil {
			Skip("KUBEBUILDER_ASSETS is not set")
		}
		ctx := context.Background()
		Expect(testEnv.CreateNamespace(ctx, "srv")).To(Succeed())
		u, err := testEnv.AddUser(ctx, "srv-user", "srv", GinkgoT().TempDir(), nil)
		Expect(err).ToNot(HaveOccurred())

		s, env, err := server.New(ctx, server.Options{
			Kubeconfig: u.KubeconfigPath,
			Namespaces: []string{"srv"},
			Warnings:   io.Discard,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(s).ToNot(BeNil())
		Expect(env.ServedVersions).To(ContainElement(contract.BuiltForAPIVersion))
		Expect(env.Namespaces.Default).To(Equal("srv"))
		Expect(env.Tiers()).To(Equal([]string{toolkit.TierRead}))
		// The user may not read the Capabilities object.
		Expect(env.Capability(toolkit.CapabilityVMSnapshots)).To(Equal(toolkit.CapabilityUnknown))

		cs, err := fakeenv.Connect(ctx, s)
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(cs.Close()).To(Succeed()) }()
		res, err := fakeenv.Call[contract.WhoAmI](ctx, cs, "whoami", map[string]any{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		Expect(res.Out.User).To(Equal("srv-user"))
		Expect(res.Out.IdentitySource).To(Equal("SelfSubjectReview"))
	})

	It("fails with a clear error when the kubeconfig is missing", func() {
		_, _, err := server.New(context.Background(), server.Options{
			Kubeconfig: filepath.Join(GinkgoT().TempDir(), "missing"),
			Warnings:   io.Discard,
		})
		Expect(err).To(HaveOccurred())
	})
})
