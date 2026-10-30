# VM Mobility During Supervisor Upgrades

VM mobility safety rails depend on both vCenter and Supervisor. vCenter enforces extension compatibility constraints, while VM Operator registers and reconciles those constraints on Supervisor-managed VMs when the capability is enabled. Upgrading vCenter alone does not establish the constraints on every managed VM.

## Upgrade window

During an upgrade in which vCenter is upgraded before Supervisor, there is a window where the new enforcement support is present in vCenter but the Supervisor components responsible for registering the constraints have not yet been upgraded. Do not rely on the new safety rails to reject an incompatible relocation during this window.

An operation accepted by the vSphere Client migration wizard or a vSphere relocation API is not, by itself, evidence that the destination preserves Supervisor's requirements. In particular, changing a managed VM's namespace resource pool, folder, or storage policy outside a supported Supervisor workflow can leave the VM inconsistent with its Kubernetes configuration.

!!! warning "vCenter upgrade alone does not activate all mobility protections"

    Between completing the vCenter upgrade and completing the Supervisor upgrade that supplies mobility constraint management, treat direct relocations of Supervisor-managed VMs with the same caution as before the upgrade. Avoid discretionary direct relocations during this interval. Follow the supported upgrade sequence for your product release.

## Operational guidance

1. Complete the Supervisor upgrade and confirm its controllers are healthy before relying on the new mobility protections.
2. Confirm that the mobility constraint capability is enabled for the release and environment. Component versions alone do not prove that the capability is active.
3. Allow VM Operator to reconcile existing VMs. Installing the new controller does not atomically register constraints on the entire inventory.
4. Before a planned administrative relocation, verify that the VM has the expected extension compatibility constraints and use the supported mobility workflow for the intended change. Do not test protection by attempting an unsafe move on a production VM.

The safety rails protect the VM's service association, folder, resource pool, VM storage policy, disk storage policies, and devices. They are not a blanket prohibition on host or datastore changes that preserve the applicable constraints.

If a VM was already moved incompatibly during the upgrade window, do not assume that completing the upgrade will repair its placement or storage configuration. Preserve the operation details and follow the product's supported recovery procedure before making further changes.
