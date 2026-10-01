// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"github.com/vmware-tanzu/vm-operator/pkg/constants/testlabels"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/test/envtest"
	"github.com/vmware-tanzu/vm-operator/mcp/test/fakeenv"
)

const interactiveKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: ctx
  context:
    cluster: c
    user: u
current-context: ctx
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: /bin/false
      interactiveMode: Always
`

var _ = Describe("vmop-mcp binary", Label(testlabels.MCP), func() {
	It("prints its version to stderr only", func() {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(binary, "--version")
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		Expect(cmd.Run()).To(Succeed())
		Expect(stdout.String()).To(BeEmpty())
		Expect(stderr.String()).To(HavePrefix("vmop-mcp "))
	})

	It("refuses an exec credential plugin that requires interactive input", func() {
		path := filepath.Join(GinkgoT().TempDir(), "kubeconfig")
		Expect(os.WriteFile(path, []byte(interactiveKubeconfig), 0o600)).To(Succeed())

		var stdout, stderr bytes.Buffer
		cmd := exec.Command(binary, "--kubeconfig", path)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		Expect(ok).To(BeTrue(), "expected a non-zero exit, got %v", err)
		Expect(exitErr.ExitCode()).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring("interactiveMode Always"))
		Expect(stderr.String()).To(ContainSubstring("stdin"))
		Expect(stdout.String()).To(BeEmpty())
	})
})

var _ = Describe("vmop-mcp binary over stdio", Label(testlabels.MCP, testlabels.EnvTest), func() {
	var (
		ctx   context.Context
		user  *envtest.User
		users int
	)

	BeforeEach(func() {
		if testEnv == nil {
			Skip("KUBEBUILDER_ASSETS is not set")
		}
		ctx = context.Background()
		const ns = "stdio"
		_ = testEnv.CreateNamespace(ctx, ns)
		var err error
		users++
		user, err = testEnv.AddUser(ctx, fmt.Sprintf("stdio-user-%d", users), ns, GinkgoT().TempDir(), envtest.VMServiceReadRules)
		Expect(err).ToNot(HaveOccurred())
	})

	start := func(args ...string) (*mcp.ClientSession, *gbytes.Buffer) {
		// gbytes.Buffer is safe for the concurrent writes of the child's
		// stderr copier.
		stderr := gbytes.NewBuffer()
		cmd := exec.Command(binary, append([]string{"--kubeconfig", user.KubeconfigPath}, args...)...)
		cmd.Stderr = stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
		cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		Expect(err).ToNot(HaveOccurred(), string(stderr.Contents()))
		DeferCleanup(cs.Close)
		return cs, stderr
	}

	names := func(cs *mcp.ClientSession) []string {
		res, err := cs.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		var out []string
		for _, t := range res.Tools {
			out = append(out, t.Name)
		}
		return out
	}

	It("initializes, offers only read tools, and answers whoami", func() {
		cs, stderr := start()
		Expect(cs.InitializeResult().ServerInfo.Name).To(Equal("vmop-mcp"))
		Expect(names(cs)).ToNot(ContainElement("set_virtual_machine_power_state"))
		Expect(names(cs)).To(ContainElement("whoami"))

		res, err := fakeenv.Call[contract.WhoAmI](ctx, cs, "whoami", map[string]any{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Err).To(BeNil())
		Expect(res.Out.User).To(Equal(user.Name))
		Expect(res.Out.Tiers).To(Equal([]string{"read"}))
		// Logs go to stderr, never into the protocol stream.
		Eventually(stderr).Should(gbytes.Say("vmop-mcp starting"))
	})

	It("offers write tools with --enable-write", func() {
		cs, _ := start("--enable-write")
		Expect(names(cs)).To(ContainElements(
			"set_virtual_machine_power_state", "restart_virtual_machine",
			"wait_for_virtual_machine", "create_virtual_machine"))
	})
})
