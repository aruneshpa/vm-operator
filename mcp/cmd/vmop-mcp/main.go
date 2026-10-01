// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Command vmop-mcp is a Model Context Protocol server for VM Operator. AI
// assistants launch it over stdio; it acts on a vSphere Supervisor with the
// user's own kubeconfig credentials.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	klog "k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/buildinfo"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/server"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Stdout is the MCP protocol channel. Every log line must go to stderr.
	fs := flag.NewFlagSet("vmop-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	klog.InitFlags(fs)
	klog.SetOutput(os.Stderr)
	klog.LogToStderr(true)

	var (
		kubeconfig  = fs.String("kubeconfig", "", "Path to the kubeconfig. Defaults to $KUBECONFIG, then ~/.kube/config.")
		kubeContext = fs.String("context", "", "Kubeconfig context to use. Defaults to the current context.")
		namespace   = fs.String("namespace", "", "Default namespace. Defaults to the context's namespace.")
		namespaces  = fs.String("namespaces", "", "Comma-separated allow-list of namespaces. Empty allows all.")
		enableWrite = fs.Bool("enable-write", false, "Enable write tools (power, restart, wait, create).")
		version     = fs.Bool("version", false, "Print the version and exit.")
	)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if *version {
		fmt.Fprintf(os.Stderr, "vmop-mcp %s (%s)\n", buildinfo.Version, buildinfo.Commit)
		return 0
	}

	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Output(os.Stderr)))
	klog.SetLogger(logger)
	ctrllog.SetLogger(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var allow []string
	if *namespaces != "" {
		allow = strings.Split(*namespaces, ",")
	}
	s, env, err := server.New(ctx, server.Options{
		Kubeconfig:  *kubeconfig,
		Context:     *kubeContext,
		Namespace:   *namespace,
		Namespaces:  allow,
		EnableWrite: *enableWrite,
		Warnings:    os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "vmop-mcp: %v\n", err)
		return 1
	}
	info := env.Provider.Info()
	logger.Info("vmop-mcp starting", "version", buildinfo.Version, "context", info.Context,
		"server", info.Server, "namespace", env.Namespaces.Default, "tiers", env.Tiers())

	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "vmop-mcp: %v\n", err)
		return 1
	}
	return 0
}
