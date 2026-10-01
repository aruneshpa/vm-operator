// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

// Package vmpower implements the write-tier VM power tools:
// set_virtual_machine_power_state, restart_virtual_machine, and
// wait_for_virtual_machine.
package vmpower

import (
	"context"
	"fmt"
	"time"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha6"

	"github.com/vmware-tanzu/vm-operator/mcp/pkg/contract"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/projection"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/toolkit"
	"github.com/vmware-tanzu/vm-operator/mcp/pkg/tools/vm"
)

// Wait limits.
const (
	DefaultWaitSeconds = 300
	MaxWaitSeconds     = 600
)

// PollInterval is how often wait_for_virtual_machine polls. Tests may lower
// it.
var PollInterval = 5 * time.Second

var (
	powerStates = []string{
		string(vmopv1.VirtualMachinePowerStateOn),
		string(vmopv1.VirtualMachinePowerStateOff),
		string(vmopv1.VirtualMachinePowerStateSuspended),
	}
	powerModes = []string{
		string(vmopv1.VirtualMachinePowerOpModeHard),
		string(vmopv1.VirtualMachinePowerOpModeSoft),
		string(vmopv1.VirtualMachinePowerOpModeTrySoft),
	}
)

// Register registers the power tools. They are write-tier tools.
func Register(r *toolkit.Registrar) {
	env := r.Env

	toolkit.AddWrite(r, toolkit.Spec[contract.SetPowerStateOutput]{
		Name:  "set_virtual_machine_power_state",
		Title: "Set VM power state",
		Description: "Sets a VirtualMachine's desired power state to PoweredOn, PoweredOff, or Suspended. mode selects " +
			"how the VM is powered off or suspended (Hard, Soft, TrySoft). Refuses VMs whose power is managed by a " +
			"VM group unless force is true. Setting the current desired state again changes nothing.",
		Enums:      map[string][]string{"state": powerStates, "mode": powerModes},
		Idempotent: true,
		Summary: func(o contract.SetPowerStateOutput) string {
			return fmt.Sprintf("changed=%t dryRun=%t %s", o.Changed, o.DryRun, vm.SummaryLine(o.VM))
		},
	}, func(ctx context.Context, in contract.SetPowerStateInput) (contract.SetPowerStateOutput, error) {
		return setPowerState(ctx, env, in)
	})

	toolkit.AddWrite(r, toolkit.Spec[contract.RestartOutput]{
		Name:  "restart_virtual_machine",
		Title: "Restart VM",
		Description: "Requests a restart of a powered-on VirtualMachine. Every call requests a new restart. " +
			"Refuses VMs whose power is managed by a VM group unless force is true.",
		Enums:      map[string][]string{"mode": powerModes},
		Idempotent: false,
		Summary: func(o contract.RestartOutput) string {
			return fmt.Sprintf("requested=%t dryRun=%t nextRestartTime=%s", o.Requested, o.DryRun, o.NextRestartTime)
		},
	}, func(ctx context.Context, in contract.RestartInput) (contract.RestartOutput, error) {
		return restart(ctx, env, in)
	})

	toolkit.AddWrite(r, toolkit.Spec[contract.WaitOutput]{
		Name:  "wait_for_virtual_machine",
		Title: "Wait for VM",
		Description: "Waits until a VirtualMachine reaches an observed power state or a condition status, " +
			"or the timeout expires. Use it after a write instead of polling.",
		Enums:    map[string][]string{"powerState": powerStates},
		ReadOnly: true,
		Summary: func(o contract.WaitOutput) string {
			return fmt.Sprintf("satisfied=%t after %ds: %s", o.Satisfied, o.ElapsedSeconds, vm.SummaryLine(o.VM))
		},
	}, func(ctx context.Context, in contract.WaitInput) (contract.WaitOutput, error) {
		return wait(ctx, env, in)
	})
}

func groupGuard(obj *vmopv1.VirtualMachine, force bool) error {
	if obj.Spec.GroupName == "" || force {
		return nil
	}
	return contract.NewError(contract.CodePreconditionFailed,
		fmt.Sprintf("the power state of VirtualMachine %s/%s is managed by VirtualMachineGroup %q; "+
			"a direct change may be reverted by the group", obj.Namespace, obj.Name, obj.Spec.GroupName),
		"change the power state of the group instead, or pass force=true")
}

func setPowerState(ctx context.Context, env *toolkit.Env, in contract.SetPowerStateInput) (contract.SetPowerStateOutput, error) {
	state := vmopv1.VirtualMachinePowerState(in.State)
	mode := vmopv1.VirtualMachinePowerOpMode(in.Mode)
	if state == vmopv1.VirtualMachinePowerStateOn && mode != "" {
		return contract.SetPowerStateOutput{}, contract.NewError(contract.CodeInvalid,
			"mode applies only to PoweredOff and Suspended", "omit mode when powering on")
	}
	ns, err := env.Namespaces.Resolve(in.Namespace)
	if err != nil {
		return contract.SetPowerStateOutput{}, err
	}

	obj, changed, err := PatchVM(ctx, env, ns, in.Name, in.DryRun, func(obj *vmopv1.VirtualMachine) error {
		if err := groupGuard(obj, in.Force); err != nil {
			return err
		}
		obj.Spec.PowerState = state
		if mode != "" {
			switch state {
			case vmopv1.VirtualMachinePowerStateOff:
				obj.Spec.PowerOffMode = mode
			case vmopv1.VirtualMachinePowerStateSuspended:
				obj.Spec.SuspendMode = mode
			}
		}
		return nil
	})
	if err != nil {
		return contract.SetPowerStateOutput{}, err
	}
	return contract.SetPowerStateOutput{
		Changed: changed,
		DryRun:  in.DryRun,
		VM:      projection.VirtualMachineSummary(obj),
	}, nil
}

func restart(ctx context.Context, env *toolkit.Env, in contract.RestartInput) (contract.RestartOutput, error) {
	ns, err := env.Namespaces.Resolve(in.Namespace)
	if err != nil {
		return contract.RestartOutput{}, err
	}
	obj, changed, err := PatchVM(ctx, env, ns, in.Name, in.DryRun, func(obj *vmopv1.VirtualMachine) error {
		if err := groupGuard(obj, in.Force); err != nil {
			return err
		}
		if obj.Status.PowerState != vmopv1.VirtualMachinePowerStateOn {
			return contract.NewError(contract.CodePreconditionFailed,
				fmt.Sprintf("VirtualMachine %s/%s is not powered on (observed %q)", obj.Namespace, obj.Name, obj.Status.PowerState),
				"only a powered-on VM can be restarted")
		}
		// The mutating webhook converts "now" into a timestamp, so every
		// request is a new restart.
		obj.Spec.NextRestartTime = "now"
		if in.Mode != "" {
			obj.Spec.RestartMode = vmopv1.VirtualMachinePowerOpMode(in.Mode)
		}
		return nil
	})
	if err != nil {
		return contract.RestartOutput{}, err
	}
	return contract.RestartOutput{
		Requested:       changed,
		DryRun:          in.DryRun,
		NextRestartTime: obj.Spec.NextRestartTime,
	}, nil
}

func wait(ctx context.Context, env *toolkit.Env, in contract.WaitInput) (contract.WaitOutput, error) {
	if (in.PowerState == "") == (in.Condition == nil) {
		return contract.WaitOutput{}, contract.NewError(contract.CodeInvalid,
			"exactly one of powerState or condition is required", "")
	}
	timeout := in.TimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultWaitSeconds
	}
	timeout = min(timeout, MaxWaitSeconds)

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	satisfied := func(obj *vmopv1.VirtualMachine) bool {
		if in.PowerState != "" {
			return string(obj.Status.PowerState) == in.PowerState
		}
		return projection.ConditionStatus(obj.Status.Conditions, in.Condition.Type) == in.Condition.Status
	}

	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		obj, err := vm.Get(ctx, env, in.Namespace, in.Name)
		if err != nil && ctx.Err() == nil {
			return contract.WaitOutput{}, err
		}
		if err == nil && satisfied(obj) {
			return contract.WaitOutput{
				Satisfied:      true,
				ElapsedSeconds: int(time.Since(start).Seconds()),
				VM:             projection.VirtualMachineSummary(obj),
			}, nil
		}
		select {
		case <-ctx.Done():
			return contract.WaitOutput{}, contract.NewError(contract.CodeTimeout,
				fmt.Sprintf("VirtualMachine did not reach the requested state within %ds", timeout),
				"diagnose_virtual_machine may explain why")
		case <-ticker.C:
		}
	}
}
