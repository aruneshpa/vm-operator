// © Broadcom. All Rights Reserved.
// The term “Broadcom” refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package projection

// Field classifications used by the inventory.
const (
	// Projected fields are copied into a DTO.
	Projected = "projected"

	// Summarized fields are reduced to non-sensitive facts, such as keys,
	// counts, presence, or Secret references.
	Summarized = "summarized"

	// Excluded fields never leave the server.
	Excluded = "excluded"
)

// Inventory records, for each API type the server projects, the
// classification of every top-level JSON field. A unit test parses the API
// source and fails when a field is missing from this inventory, forcing an
// explicit decision whenever the API grows.
var Inventory = map[string]map[string]string{
	"VirtualMachineSpec": {
		"image":                     Projected,
		"imageName":                 Projected,
		"className":                 Projected,
		"class":                     Excluded,
		"affinity":                  Excluded,
		"crypto":                    Excluded,
		"storageClass":              Projected,
		"volumeAttributesClassName": Excluded,
		"bootstrap":                 Summarized,
		"network":                   Summarized,
		"powerState":                Projected,
		"powerOffMode":              Excluded,
		"suspendMode":               Excluded,
		"nextRestartTime":           Excluded,
		"restartMode":               Excluded,
		"volumes":                   Summarized,
		"readinessProbe":            Excluded,
		"advanced":                  Summarized,
		"reserved":                  Excluded,
		"minHardwareVersion":        Excluded,
		"instanceUUID":              Excluded,
		"biosUUID":                  Excluded,
		"guestID":                   Excluded,
		"promoteDisksMode":          Excluded,
		"bootOptions":               Excluded,
		"currentSnapshotName":       Excluded,
		"groupName":                 Projected,
		"hardware":                  Excluded,
		"policies":                  Excluded,
		"resources":                 Excluded,
		"cpuAdvanced":               Excluded,
		"memoryAdvanced":            Excluded,
	},
	"VirtualMachineStatus": {
		"class":               Excluded,
		"nodeName":            Excluded,
		"powerState":          Projected,
		"conditions":          Projected,
		"crypto":              Excluded,
		"network":             Summarized,
		"uniqueID":            Projected,
		"biosUUID":            Projected,
		"instanceUUID":        Projected,
		"volumes":             Excluded,
		"changeBlockTracking": Excluded,
		"zone":                Projected,
		"lastRestartTime":     Excluded,
		"hardwareVersion":     Projected,
		"storage":             Excluded,
		"provider":            Excluded,
		"currentSnapshot":     Summarized,
		"rootSnapshots":       Excluded,
		"guest":               Excluded,
		"hardware":            Excluded,
		"policies":            Excluded,
		"extraConfig":         Summarized,
	},
	"VirtualMachineClassSpec": {
		"controllerName":    Excluded,
		"hardware":          Summarized,
		"policies":          Excluded,
		"description":       Projected,
		"configSpec":        Summarized,
		"reservedProfileID": Projected,
		"reservedSlots":     Excluded,
	},
	"VirtualMachineImageStatus": {
		"name":                   Projected,
		"capabilities":           Projected,
		"firmware":               Projected,
		"hardwareVersion":        Projected,
		"osInfo":                 Projected,
		"ovfProperties":          Summarized,
		"vmwareSystemProperties": Excluded,
		"productInfo":            Summarized,
		"disks":                  Summarized,
		"providerContentVersion": Projected,
		"providerItemID":         Projected,
		"conditions":             Projected,
		"type":                   Projected,
	},
	"VirtualMachineSnapshotSpec": {
		"memory":      Projected,
		"quiesce":     Summarized,
		"description": Projected,
		"vmName":      Projected,
	},
}
