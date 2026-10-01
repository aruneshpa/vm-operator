// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/kube"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

type echoIn struct {
	Mode string `json:"mode,omitempty"`
	Size int    `json:"size,omitempty"`
}

type echoOut struct {
	Mode    string `json:"mode"`
	Payload string `json:"payload,omitempty"`
}

func echo(_ context.Context, in echoIn) (echoOut, error) {
	return echoOut{Mode: in.Mode, Payload: strings.Repeat("x", in.Size)}, nil
}

func register(r *toolkit.Registrar) {
	toolkit.AddRead(r, toolkit.Spec[echoOut]{
		Name: "read_tool", Title: "Read", Description: "Reads.",
		Enums:   map[string][]string{"mode": {"a", "b"}},
		Summary: func(o echoOut) string { return "summary:" + o.Mode },
	}, echo)
	toolkit.AddWrite(r, toolkit.Spec[echoOut]{Name: "write_tool", Description: "Writes."}, echo)
	toolkit.AddWrite(r, toolkit.Spec[echoOut]{Name: "idempotent_tool", Description: "Writes.", Idempotent: true}, echo)
	toolkit.AddWrite(r, toolkit.Spec[echoOut]{Name: "readonly_write_tool", Description: "Waits.", ReadOnly: true}, echo)
	toolkit.AddRead(r, toolkit.Spec[echoOut]{Name: "failing_tool", Description: "Fails."},
		func(context.Context, echoIn) (echoOut, error) {
			return echoOut{}, apierrors.NewNotFound(schema.GroupResource{Resource: "virtualmachines"}, "x")
		})
}

var _ = Describe("Toolkit", Label(testlabels.MCP), func() {
	ctx := context.Background()

	Context("registration", func() {
		var (
			env   *fakeenv.Env
			cs    *mcp.ClientSession
			r     *toolkit.Registrar
			write bool
		)

		JustBeforeEach(func() {
			env = fakeenv.New(fakeenv.Options{EnableWrite: write})
			var err error
			cs, r, err = env.ConnectTools(ctx, register)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			Expect(cs.Close()).To(Succeed())
			write = false
		})

		tools := func() map[string]*mcp.Tool {
			res, err := cs.ListTools(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			out := map[string]*mcp.Tool{}
			for _, t := range res.Tools {
				out[t.Name] = t
			}
			return out
		}

		It("omits write tools when the write tier is disabled", func() {
			Expect(tools()).To(SatisfyAll(HaveKey("read_tool"), Not(HaveKey("write_tool")), Not(HaveKey("readonly_write_tool"))))
			Expect(r.Names()).To(Equal([]string{"failing_tool", "read_tool"}))
		})

		When("the write tier is enabled", func() {
			BeforeEach(func() { write = true })

			It("sets every annotation explicitly", func() {
				type hints struct{ readOnly, destructive, idempotent, openWorld bool }
				expected := map[string]hints{
					"read_tool":           {readOnly: true, idempotent: true},
					"failing_tool":        {readOnly: true, idempotent: true},
					"write_tool":          {},
					"idempotent_tool":     {idempotent: true},
					"readonly_write_tool": {readOnly: true, idempotent: true},
				}
				ts := tools()
				Expect(ts).To(HaveLen(len(expected)))
				for name, h := range expected {
					a := ts[name].Annotations
					Expect(a).ToNot(BeNil(), name)
					Expect(a.DestructiveHint).ToNot(BeNil(), name)
					Expect(a.OpenWorldHint).ToNot(BeNil(), name)
					Expect(a.ReadOnlyHint).To(Equal(h.readOnly), name)
					Expect(*a.DestructiveHint).To(Equal(h.destructive), name)
					Expect(a.IdempotentHint).To(Equal(h.idempotent), name)
					Expect(*a.OpenWorldHint).To(Equal(h.openWorld), name)
				}
			})
		})

		It("appends the data-not-instructions notice and sets the title", func() {
			t := tools()["read_tool"]
			Expect(t.Description).To(HaveSuffix(contract.DataNotInstructions))
			Expect(t.Title).To(Equal("Read"))
		})

		It("adds enums to the input schema and rejects other values", func() {
			b, err := json.Marshal(tools()["read_tool"].InputSchema)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(b)).To(ContainSubstring(`"enum":["a","b"]`))

			res, err := fakeenv.Call[echoOut](ctx, cs, "read_tool", map[string]any{"mode": "c"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).ToNot(BeNil())

			res, err = fakeenv.Call[echoOut](ctx, cs, "read_tool", map[string]any{"mode": "a"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).To(BeNil())
			Expect(res.Out.Mode).To(Equal("a"))
		})

		It("uses the summary as text content", func() {
			res, err := fakeenv.Call[echoOut](ctx, cs, "read_tool", map[string]any{"mode": "b"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Text).To(Equal("summary:b"))
		})

		It("refuses results over the size limit", func() {
			res, err := fakeenv.Call[echoOut](ctx, cs, "read_tool", map[string]any{"size": toolkit.MaxResultBytes + 1})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeInvalid))
		})

		It("maps handler errors to tool errors", func() {
			res, err := fakeenv.Call[echoOut](ctx, cs, "failing_tool", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Raw.IsError).To(BeTrue())
			Expect(res.Err.Code).To(Equal(contract.CodeNotFound))
		})
	})

	Context("MapError", func() {
		gr := schema.GroupResource{Group: "vmoperator.vmware.com", Resource: "virtualmachines"}
		code := func(err error) string { return contract.CodeOf(toolkit.MapError(err)) }

		DescribeTable("maps errors to codes",
			func(err error, expected string) {
				Expect(code(err)).To(Equal(expected))
			},
			Entry("unauthorized", apierrors.NewUnauthorized("expired"), contract.CodeUnauthorized),
			Entry("forbidden", apierrors.NewForbidden(gr, "x", errors.New("no")), contract.CodeForbidden),
			Entry("not found", apierrors.NewNotFound(gr, "x"), contract.CodeNotFound),
			Entry("conflict", apierrors.NewConflict(gr, "x", errors.New("changed")), contract.CodeConflict),
			Entry("invalid", apierrors.NewInvalid(schema.GroupKind{Kind: "VirtualMachine"}, "x",
				field.ErrorList{field.Invalid(field.NewPath("spec"), "", "bad")}), contract.CodeInvalid),
			Entry("bad request", apierrors.NewBadRequest("bad"), contract.CodeInvalid),
			Entry("resource expired", apierrors.NewResourceExpired("expired"), contract.CodeCursorExpired),
			Entry("timeout", apierrors.NewTimeoutError("slow", 1), contract.CodeTimeout),
			Entry("server timeout", apierrors.NewServerTimeout(gr, "get", 1), contract.CodeTimeout),
			Entry("deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), contract.CodeTimeout),
			Entry("forbidden kind", fmt.Errorf("x: %w", kube.ErrForbiddenKind), contract.CodeForbidden),
			Entry("admission webhook", apierrors.NewForbidden(gr, "x",
				errors.New(`admission webhook "default.validating.virtualmachine" denied the request: bad`)), contract.CodeInvalid),
			Entry("other", errors.New("boom"), contract.CodeInternal),
		)

		It("keeps the Supervisor message", func() {
			err := toolkit.MapError(apierrors.NewForbidden(gr, "x", errors.New("user bob cannot get")))
			Expect(err.Error()).To(ContainSubstring("user bob cannot get"))
		})

		It("passes through ToolErrors and nil", func() {
			te := contract.NewError(contract.CodePreconditionFailed, "m", "h")
			Expect(toolkit.MapError(te)).To(BeIdenticalTo(te))
			Expect(toolkit.MapError(nil)).To(BeNil())
		})
	})

	Context("NamespacePolicy", func() {
		It("uses the default", func() {
			ns, err := toolkit.NewNamespacePolicy("dev", nil).Resolve("")
			Expect(err).ToNot(HaveOccurred())
			Expect(ns).To(Equal("dev"))
		})

		It("uses an explicit namespace", func() {
			ns, err := toolkit.NewNamespacePolicy("dev", nil).Resolve("prod")
			Expect(err).ToNot(HaveOccurred())
			Expect(ns).To(Equal("prod"))
		})

		It("enforces the allow-list", func() {
			p := toolkit.NewNamespacePolicy("dev", []string{" dev ", "qa", "dev", ""})
			Expect(p.Allowed()).To(Equal([]string{"dev", "qa"}))
			_, err := p.Resolve("qa")
			Expect(err).ToNot(HaveOccurred())
			_, err = p.Resolve("prod")
			Expect(contract.CodeOf(err)).To(Equal(contract.CodeNamespaceNotAllowed))
		})

		It("fails without any namespace", func() {
			_, err := toolkit.NewNamespacePolicy("", nil).Resolve("")
			Expect(contract.CodeOf(err)).To(Equal(contract.CodeInvalid))
		})
	})

	Context("lists", func() {
		It("bounds the limit", func() {
			Expect(toolkit.ListLimit(0)).To(BeEquivalentTo(toolkit.DefaultListLimit))
			Expect(toolkit.ListLimit(-3)).To(BeEquivalentTo(toolkit.DefaultListLimit))
			Expect(toolkit.ListLimit(7)).To(BeEquivalentTo(7))
			Expect(toolkit.ListLimit(10000)).To(BeEquivalentTo(toolkit.MaxListLimit))
		})

		It("fits items within the budget", func() {
			small := []string{"a", "b"}
			out, truncated := toolkit.Fit(small)
			Expect(out).To(Equal(small))
			Expect(truncated).To(BeFalse())

			big := make([]string, 10)
			for i := range big {
				big[i] = strings.Repeat("x", 10<<10)
			}
			out, truncated = toolkit.Fit(big)
			Expect(truncated).To(BeTrue())
			Expect(len(out)).To(BeNumerically("<", len(big)))
			b, _ := json.Marshal(out)
			Expect(len(b)).To(BeNumerically("<", toolkit.MaxResultBytes))
		})

		It("builds list options", func() {
			_, err := toolkit.ListOptions("dev", contract.ListInput{LabelSelector: "a in ("}, "")
			Expect(contract.CodeOf(err)).To(Equal(contract.CodeInvalid))
			opts, err := toolkit.ListOptions("dev", contract.ListInput{LabelSelector: "a=b", Limit: 5}, "tok")
			Expect(err).ToNot(HaveOccurred())
			Expect(opts).To(HaveLen(4))
		})

		It("writes paging hints", func() {
			var b strings.Builder
			toolkit.WritePage(&b, contract.Page{NextCursor: "c1", Truncated: true, Hint: toolkit.TruncationHint(3)})
			Expect(b.String()).To(ContainSubstring("cursor=c1"))
			Expect(b.String()).To(ContainSubstring("limit=3"))
		})
	})

	Context("Env", func() {
		It("reports tiers and capabilities", func() {
			e := &toolkit.Env{}
			Expect(e.Tiers()).To(Equal([]string{toolkit.TierRead}))
			e.EnableWrite = true
			Expect(e.Tiers()).To(Equal([]string{toolkit.TierRead, toolkit.TierWrite}))
			Expect(e.Capability("x")).To(Equal(toolkit.CapabilityUnknown))
			e.Capabilities = map[string]string{"x": toolkit.CapabilityActive}
			Expect(e.Capability("x")).To(Equal(toolkit.CapabilityActive))
		})
	})
})
