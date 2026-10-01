// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package mcp contains E2E specs for vmop-mcp, the client-side MCP server for
// VM Operator. The server is built in-process with a DevOps SSO user's
// kubeconfig, so every request reaches the Supervisor as that user and goes
// through real Supervisor RBAC and VM Operator admission.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/server"

	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/dcli"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/testbed"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/vcenter"
	"github.com/vmware-tanzu/vm-operator/test/e2e/infrastructure/vsphere/wcp"
	"github.com/vmware-tanzu/vm-operator/test/e2e/testutils"
	e2eConfig "github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/config"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/consts"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/lib/vmoperator"
	"github.com/vmware-tanzu/vm-operator/test/e2e/vmservice/skipper"
	"github.com/vmware-tanzu/vm-operator/test/e2e/wcpframework"
)

const (
	mcpSpecName        = "mcp-server"
	ssoUserNameBase    = "mcp-sso-user"
	ssoUserPassword    = "Password!23"
	notAllowedNS       = "vmop-mcp-not-allowed"
	bogusVMClassName   = "vmop-mcp-no-such-class"
	ssoUserLoginDomain = "vsphere.local"
)

// MCPSpecInput is the input of the MCP specs.
type MCPSpecInput struct {
	ClusterProxy     wcpframework.WCPClusterProxyInterface
	Config           *e2eConfig.E2EConfig
	WCPClient        wcp.WorkloadManagementAPI
	WCPNamespaceName string
	LinuxVMName      string
}

// session is an MCP client session connected to an in-process vmop-mcp.
type session struct {
	cs *sdkmcp.ClientSession
}

func connect(ctx context.Context, kubeconfigPath, namespace string, enableWrite bool) *session {
	s, _, err := server.New(ctx, server.Options{
		Kubeconfig:  kubeconfigPath,
		Namespace:   namespace,
		Namespaces:  []string{namespace},
		EnableWrite: enableWrite,
		Warnings:    GinkgoWriter,
	})
	Expect(err).ToNot(HaveOccurred(), "failed to build vmop-mcp server")

	st, ct := sdkmcp.NewInMemoryTransports()
	_, err = s.Connect(ctx, st, nil)
	Expect(err).ToNot(HaveOccurred())
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "vmop-mcp-e2e", Version: "test"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	Expect(err).ToNot(HaveOccurred())
	return &session{cs: cs}
}

func (s *session) close() {
	Expect(s.cs.Close()).To(Succeed())
}

// call invokes a tool. It decodes the structured content into out on success
// and returns the decoded ToolError on a tool failure.
func (s *session) call(ctx context.Context, name string, args map[string]any, out any) *contract.ToolError {
	res, err := s.cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	Expect(err).ToNot(HaveOccurred(), "protocol error calling %s", name)
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*sdkmcp.TextContent); ok {
			text = tc.Text
		}
	}
	if res.IsError {
		te := &contract.ToolError{}
		if json.Unmarshal([]byte(text), te) != nil {
			te = &contract.ToolError{Code: "protocol", Message: text}
		}
		return te
	}
	if out != nil {
		b, err := json.Marshal(res.StructuredContent)
		Expect(err).ToNot(HaveOccurred())
		Expect(json.Unmarshal(b, out)).To(Succeed())
	}
	return nil
}

func (s *session) toolNames(ctx context.Context) []string {
	res, err := s.cs.ListTools(ctx, nil)
	Expect(err).ToNot(HaveOccurred())
	names := make([]string, 0, len(res.Tools))
	for _, t := range res.Tools {
		names = append(names, t.Name)
	}
	return names
}

type ssoUser struct {
	user           *vcenter.User
	kubeconfigPath string
}

func createSSOUser(ctx context.Context, input MCPSpecInput, access wcp.AccessType) ssoUser {
	creds := dcli.VCenterUserCredentials{Username: testbed.AdminUsername, Password: testbed.AdminPassword}
	sshRunner, _, supervisorIP := testutils.GetHelpersFromKubeconfig(ctx, input.ClusterProxy.GetKubeconfigPath())
	name := fmt.Sprintf("%s-%s", ssoUserNameBase, capiutil.RandomString(6))
	user := vcenter.NewUser(name, ssoUserPassword).WithAdminCreds(creds).WithSSHCommandRunner(sshRunner)
	plugin := testutils.CreateUserAndLogin(user, supervisorIP, "", "")
	testutils.SetUserPermissionsOnNamespace(input.WCPClient, user, access, input.WCPNamespaceName)
	return ssoUser{user: user, kubeconfigPath: plugin.KubeconfigPath()}
}

func deleteSSOUser(input MCPSpecInput, u ssoUser) {
	err := input.WCPClient.RemoveNamespacePermissions(wcp.Principal{
		Type:   wcp.UserSubjectType,
		Name:   u.user.Credentials.Username,
		Domain: ssoUserLoginDomain,
	}, input.WCPNamespaceName)
	Expect(err).NotTo(HaveOccurred())
	vcenter.DeleteUserOrFail(u.user)
}

// withArgs returns a copy of base with the given key/value pairs set.
func withArgs(base map[string]any, kv ...any) map[string]any {
	m := make(map[string]any, len(base)+len(kv)/2)
	for k, v := range base {
		m[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func validate(input MCPSpecInput) {
	Expect(input.Config).NotTo(BeNil(), "Invalid argument. input.Config can't be nil when calling %s spec", mcpSpecName)
	Expect(input.Config.InfraConfig).ToNot(BeNil(), "Invalid argument. input.Config.InfraConfig can't be nil when calling %s spec", mcpSpecName)
	skipper.SkipUnlessInfraIs(input.Config.InfraConfig.InfraName, consts.WCP)
	Expect(input.ClusterProxy).NotTo(BeNil(), "Invalid argument. input.ClusterProxy can't be nil when calling %s spec", mcpSpecName)
	Expect(input.WCPClient).NotTo(BeNil(), "Invalid argument. input.WCPClient can't be nil when calling %s spec", mcpSpecName)
	Expect(input.WCPNamespaceName).ToNot(BeEmpty(), "Invalid argument. input.WCPNamespaceName can't be empty when calling %s spec", mcpSpecName)
}

// MCPReadSpec exercises the read tier as a DevOps user with view access.
func MCPReadSpec(ctx context.Context, inputGetter func() MCPSpecInput) {
	var (
		input MCPSpecInput
		u     ssoUser
		s     *session
	)

	BeforeEach(func() {
		input = inputGetter()
		validate(input)
		Expect(input.LinuxVMName).ToNot(BeEmpty(), "Invalid argument. input.LinuxVMName can't be empty when calling %s spec", mcpSpecName)

		By("Waiting for the common Linux VM to be created")
		vmoperator.WaitForVirtualMachineCreation(ctx, input.Config, input.ClusterProxy.GetClient(), input.WCPNamespaceName, input.LinuxVMName)

		By("Creating a DevOps SSO user with view access")
		u = createSSOUser(ctx, input, wcp.ViewAccessType)

		By("Starting vmop-mcp in-process with the user's kubeconfig")
		s = connect(ctx, u.kubeconfigPath, input.WCPNamespaceName, false)
	})

	AfterEach(func() {
		if s != nil {
			s.close()
		}
		deleteSSOUser(input, u)
	})

	It("inspects and diagnoses VMs as the DevOps user", Label("smoke"), func() {
		By("advertising no write tools")
		Expect(s.toolNames(ctx)).ToNot(ContainElement("set_virtual_machine_power_state"))

		By("whoami reports the SSO user")
		var who contract.WhoAmI
		Expect(s.call(ctx, "whoami", nil, &who)).To(BeNil())
		Expect(who.User).To(ContainSubstring(u.user.Credentials.Username))
		Expect(who.Tiers).To(Equal([]string{"read"}))
		Expect(who.ServedVMServiceVersions).To(ContainElement(contract.BuiltForAPIVersion))
		GinkgoWriter.Printf("vmop-mcp identity source: %s, capabilities: %v\n", who.IdentitySource, who.Capabilities)

		By("list_virtual_machines includes the common Linux VM")
		var list contract.VirtualMachineList
		Expect(s.call(ctx, "list_virtual_machines", map[string]any{"limit": 200}, &list)).To(BeNil())
		names := make([]string, 0, len(list.Items))
		for _, i := range list.Items {
			names = append(names, i.Name)
		}
		Expect(names).To(ContainElement(input.LinuxVMName))

		By("diagnose_virtual_machine reports the VM healthy")
		Eventually(func(g Gomega) {
			var d contract.Diagnosis
			g.Expect(s.call(ctx, "diagnose_virtual_machine", map[string]any{"name": input.LinuxVMName}, &d)).To(BeNil())
			g.Expect(d.Healthy).To(BeTrue(), "findings: %+v", d.Findings)
		}, input.Config.GetIntervals("default", "wait-virtual-machine-creation")...).Should(Succeed())

		By("explain_field explains spec.powerState, or reports the schema unavailable")
		var e contract.Explanation
		if te := s.call(ctx, "explain_field", map[string]any{"kind": "VirtualMachine", "fieldPath": "spec.powerState"}, &e); te != nil {
			Expect(te.Code).To(Equal(contract.CodeSchemaUnavailable), "unexpected error: %+v", te)
			GinkgoWriter.Printf("vmop-mcp: OpenAPI v3 is NOT readable by the DevOps role: %s\n", te.Message)
		} else {
			Expect(e.Enum).To(ContainElements("PoweredOn", "PoweredOff"))
			GinkgoWriter.Println("vmop-mcp: OpenAPI v3 is readable by the DevOps role")
		}

		By("refusing a namespace outside the allow-list without calling the Supervisor")
		te := s.call(ctx, "list_virtual_machines", map[string]any{"namespace": notAllowedNS}, nil)
		Expect(te).ToNot(BeNil())
		Expect(te.Code).To(Equal(contract.CodeNamespaceNotAllowed))
	})
}

// MCPWriteSpec exercises the write tier as a DevOps user with edit access.
func MCPWriteSpec(ctx context.Context, inputGetter func() MCPSpecInput) {
	var (
		input  MCPSpecInput
		u      ssoUser
		s      *session
		vmName string
		admin  ctrlclient.Client
	)

	BeforeEach(func() {
		input = inputGetter()
		validate(input)
		Expect(input.LinuxVMName).ToNot(BeEmpty(), "Invalid argument. input.LinuxVMName can't be empty when calling %s spec", mcpSpecName)
		admin = input.ClusterProxy.GetClient()
		vmName = fmt.Sprintf("mcp-vm-%s", capiutil.RandomString(4))

		By("Creating a DevOps SSO user with edit access")
		u = createSSOUser(ctx, input, wcp.EditAccessType)

		By("Starting vmop-mcp in-process with the write tier enabled")
		s = connect(ctx, u.kubeconfigPath, input.WCPNamespaceName, true)
	})

	AfterEach(func() {
		if s != nil {
			s.close()
		}
		vmoperator.DeleteVirtualMachineAndWait(ctx, input.Config, admin, input.WCPNamespaceName, vmName)
		deleteSSOUser(input, u)
	})

	It("creates and operates a VM with preflight and dry run", func() {
		res := input.Config.InfraConfig.ManagementClusterConfig.Resources

		By("reading the image of the common Linux VM to reuse it")
		linux := &vmopv1.VirtualMachine{}
		Expect(admin.Get(ctx, ctrlclient.ObjectKey{Namespace: input.WCPNamespaceName, Name: input.LinuxVMName}, linux)).To(Succeed())
		imageName := linux.Spec.ImageName
		if linux.Spec.Image != nil && linux.Spec.Image.Name != "" {
			imageName = linux.Spec.Image.Name
		}
		Expect(imageName).ToNot(BeEmpty())

		base := map[string]any{
			"name":         vmName,
			"className":    res.VMClassName,
			"imageName":    imageName,
			"storageClass": res.StorageClassName,
		}
		By("a dry run with a bogus class is stopped by preflight")
		var out contract.CreateVirtualMachineOutput
		Expect(s.call(ctx, "create_virtual_machine", withArgs(base, "className", bogusVMClassName, "dryRun", true), &out)).To(BeNil())
		Expect(out.Admitted).To(BeFalse())
		Expect(out.Preflight).To(ContainElement(HaveField("Check", "class.not_found")))

		By("a valid dry run is admitted and nothing is persisted")
		out = contract.CreateVirtualMachineOutput{}
		Expect(s.call(ctx, "create_virtual_machine", withArgs(base, "dryRun", true), &out)).To(BeNil())
		Expect(out.Admitted).To(BeTrue(), "preflight: %+v", out.Preflight)
		Expect(out.VM).ToNot(BeNil())
		Expect(out.VM.Image.Name).ToNot(BeEmpty())
		err := admin.Get(ctx, ctrlclient.ObjectKey{Namespace: input.WCPNamespaceName, Name: vmName}, &vmopv1.VirtualMachine{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "dry run must not persist the VM: %v", err)

		By("creating the VM for real")
		out = contract.CreateVirtualMachineOutput{}
		Expect(s.call(ctx, "create_virtual_machine", withArgs(base, "dryRun", false), &out)).To(BeNil())
		Expect(out.Admitted).To(BeTrue())

		By("waiting for the VM to be powered on")
		var w contract.WaitOutput
		Expect(s.call(ctx, "wait_for_virtual_machine", map[string]any{
			"name": vmName, "powerState": "PoweredOn", "timeoutSeconds": 600,
		}, &w)).To(BeNil())
		Expect(w.Satisfied).To(BeTrue())

		By("powering the VM off with mode TrySoft")
		var p contract.SetPowerStateOutput
		Expect(s.call(ctx, "set_virtual_machine_power_state", map[string]any{
			"name": vmName, "state": "PoweredOff", "mode": "TrySoft",
		}, &p)).To(BeNil())
		Expect(p.Changed).To(BeTrue())

		By("setting the same power state again changes nothing")
		Expect(s.call(ctx, "set_virtual_machine_power_state", map[string]any{
			"name": vmName, "state": "PoweredOff", "mode": "TrySoft",
		}, &p)).To(BeNil())
		Expect(p.Changed).To(BeFalse())

		By("waiting for the VM to be powered off")
		w = contract.WaitOutput{}
		Expect(s.call(ctx, "wait_for_virtual_machine", map[string]any{
			"name": vmName, "powerState": "PoweredOff", "timeoutSeconds": 600,
		}, &w)).To(BeNil())
		Expect(w.Satisfied).To(BeTrue())
	})
}
