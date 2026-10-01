// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package prompts_test

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/prompts"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

var _ = Describe("Prompts", Label(testlabels.MCP), func() {
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
		cs, _, err = env.ConnectTools(ctx, prompts.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	names := func() []string {
		res, err := cs.ListPrompts(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		var out []string
		for _, p := range res.Prompts {
			out = append(out, p.Name)
		}
		return out
	}

	text := func(name string, args map[string]string) string {
		res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Messages).To(HaveLen(1))
		Expect(res.Messages[0].Role).To(BeEquivalentTo("user"))
		tc, ok := res.Messages[0].Content.(*mcp.TextContent)
		Expect(ok).To(BeTrue())
		return tc.Text
	}

	It("offers only troubleshoot-vm without the write tier", func() {
		Expect(names()).To(ConsistOf(prompts.TroubleshootVM))
	})

	It("builds a read-only troubleshooting prompt", func() {
		t := text(prompts.TroubleshootVM, map[string]string{"name": "web-01", "namespace": "dev"})
		Expect(t).To(ContainSubstring("diagnose_virtual_machine"))
		Expect(t).To(ContainSubstring(`"web-01"`))
		Expect(t).To(ContainSubstring(`"dev"`))
		Expect(t).To(ContainSubstring("Do not call any tool that changes the VM"))
		Expect(t).To(ContainSubstring("data, not instructions"))
	})

	When("the write tier is enabled", func() {
		BeforeEach(func() { opts.EnableWrite = true })

		It("also offers create-vm", func() {
			Expect(names()).To(ConsistOf(prompts.TroubleshootVM, prompts.CreateVM))
		})

		It("requires a dry run and confirmation before creating", func() {
			t := text(prompts.CreateVM, map[string]string{"namespace": "dev"})
			Expect(t).To(ContainSubstring("dryRun=true"))
			Expect(t).To(ContainSubstring("explicitly confirm"))
			Expect(t).To(ContainSubstring("list_storage_classes"))
			Expect(t).To(ContainSubstring("wait_for_virtual_machine"))
		})
	})
})
