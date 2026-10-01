// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package events_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/events"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func event(name, kind, obj string, age time.Duration, reason, msg string) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: fakeenv.DefaultNamespace, Name: name},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: obj, Namespace: fakeenv.DefaultNamespace},
		Type:           corev1.EventTypeWarning,
		Reason:         reason,
		Message:        msg,
		Count:          2,
		Source:         corev1.EventSource{Component: "vmop"},
		LastTimestamp:  metav1.NewTime(now.Add(-age)),
	}
}

var _ = Describe("get_events", Label(testlabels.MCP), func() {
	var (
		ctx      context.Context
		opts     fakeenv.Options
		env      *fakeenv.Env
		cs       *mcp.ClientSession
		origNow  func() time.Time
		apiCalls int
	)

	call := func(args map[string]any) fakeenv.Result[contract.EventList] {
		res, err := fakeenv.Call[contract.EventList](ctx, cs, "get_events", args)
		Expect(err).ToNot(HaveOccurred())
		return res
	}

	BeforeEach(func() {
		ctx = context.Background()
		apiCalls = 0
		origNow = projection.Now
		projection.Now = func() time.Time { return now }
		opts = fakeenv.Options{
			Objects: []ctrlclient.Object{
				event("old", "VirtualMachine", "web-01", 2*time.Hour, "Old", "old"),
				event("mid", "VirtualMachine", "web-01", 10*time.Minute, "Mid", "mid"),
				event("new", "VirtualMachine", "web-01", time.Minute, "New", "new"),
				event("other-name", "VirtualMachine", "web-02", time.Minute, "Other", "other"),
				event("other-kind", "VirtualMachineClass", "web-01", time.Minute, "Kind", "kind"),
			},
			Interceptor: interceptor.Funcs{
				List: func(ctx context.Context, c ctrlclient.WithWatch, l ctrlclient.ObjectList, o ...ctrlclient.ListOption) error {
					apiCalls++
					return c.List(ctx, l, o...)
				},
			},
		}
	})

	JustBeforeEach(func() {
		env = fakeenv.New(opts)
		var err error
		cs, _, err = env.ConnectTools(ctx, events.Register)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		projection.Now = origNow
		Expect(cs.Close()).To(Succeed())
		Expect(env.Trap.Violations()).To(BeEmpty())
		Expect(env.KubeViolations()).To(BeEmpty())
	})

	reasons := func(l contract.EventList) []string {
		var out []string
		for _, e := range l.Events {
			out = append(out, e.Reason)
		}
		return out
	}

	It("returns the object's recent events newest first", func() {
		res := call(map[string]any{"kind": "VirtualMachine", "name": "web-01"})
		Expect(res.Err).To(BeNil())
		Expect(reasons(res.Out)).To(Equal([]string{"New", "Mid"}))
		e := res.Out.Events[0]
		Expect(e.Type).To(Equal(corev1.EventTypeWarning))
		Expect(e.Count).To(BeEquivalentTo(2))
		Expect(e.Source).To(Equal("vmop"))
		Expect(e.Message.Value).To(Equal("new"))
		Expect(e.LastTimestamp).To(Equal(now.Add(-time.Minute).Format(contract.RFC3339)))
		Expect(res.Text).To(ContainSubstring("2 events"))
	})

	It("honors sinceMinutes", func() {
		res := call(map[string]any{"kind": "VirtualMachine", "name": "web-01", "sinceMinutes": 180})
		Expect(reasons(res.Out)).To(Equal([]string{"New", "Mid", "Old"}))
		res = call(map[string]any{"kind": "VirtualMachine", "name": "web-01", "sinceMinutes": 5})
		Expect(reasons(res.Out)).To(Equal([]string{"New"}))
	})

	It("honors limit", func() {
		res := call(map[string]any{"kind": "VirtualMachine", "name": "web-01", "sinceMinutes": 180, "limit": 1})
		Expect(reasons(res.Out)).To(Equal([]string{"New"}))
	})

	When("there are many events", func() {
		BeforeEach(func() {
			opts.Objects = nil
			for i := range 150 {
				opts.Objects = append(opts.Objects,
					event(fmt.Sprintf("e%03d", i), "VirtualMachine", "busy", time.Duration(i)*time.Second, fmt.Sprintf("R%03d", i), "m"))
			}
		})

		It("defaults to 20 and caps at 100", func() {
			res := call(map[string]any{"kind": "VirtualMachine", "name": "busy"})
			Expect(res.Out.Events).To(HaveLen(events.DefaultLimit))
			Expect(res.Out.Events[0].Reason).To(Equal("R000"))
			res = call(map[string]any{"kind": "VirtualMachine", "name": "busy", "limit": 1000})
			Expect(res.Out.Events).To(HaveLen(events.MaxLimit))
		})
	})

	When("an event message is long", func() {
		BeforeEach(func() {
			opts.Objects = []ctrlclient.Object{
				event("long", "VirtualMachine", "web-01", time.Minute, "Long", strings.Repeat("é", 300)),
			}
		})

		It("truncates it as untrusted text on a rune boundary", func() {
			res := call(map[string]any{"kind": "VirtualMachine", "name": "web-01"})
			m := res.Out.Events[0].Message
			Expect(m.Truncated).To(BeTrue())
			Expect(len(m.Value)).To(BeNumerically("<=", contract.UntrustedMaxBytes))
			Expect(strings.Trim(m.Value, "é")).To(BeEmpty())
		})
	})

	When("the namespace is not allowed", func() {
		BeforeEach(func() {
			opts.Namespaces = []string{fakeenv.DefaultNamespace}
		})

		It("refuses before calling the API", func() {
			res := call(map[string]any{"kind": "VirtualMachine", "name": "web-01", "namespace": "prod"})
			Expect(res.Err).ToNot(BeNil())
			Expect(res.Err.Code).To(Equal(contract.CodeNamespaceNotAllowed))
			Expect(apiCalls).To(BeZero())
		})
	})

	It("requires kind and name", func() {
		res := call(map[string]any{"name": "web-01"})
		Expect(res.Err).ToNot(BeNil())
	})
})
