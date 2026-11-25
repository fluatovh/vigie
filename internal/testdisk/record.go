// Package testdisk gère le cycle de vie du disque de test de l'appliance : création et suppression
// via l'API vSphere (ReconfigVM), détection dans l'invité, contrôles de sécurité du §6.3 du plan
// et signature du bloc 0.
//
// Règle absolue : aucune écriture sur un périphérique bloc sans que Verify ait réussi, et toute
// écriture passe par WriteSignature (phase 3) ou par le worker de bench (phase 4) qui refait Verify.
package testdisk

import "time"

// RecordFile est le document du store qui décrit le disque de test courant.
const RecordFile = "testdisk.json"

// Record décrit le disque de test tel que créé par l'appliance (ce que l'API vSphere nous a dit).
type Record struct {
	VMRef         string    `json:"vm_ref"`
	DatastoreRef  string    `json:"datastore_ref"`
	DatastoreName string    `json:"datastore_name"`
	SizeBytes     int64     `json:"size_bytes"`
	FileName      string    `json:"file_name"`    // [datastore] vigie-<vm>/testdisk.vmdk
	Folder        string    `json:"folder"`       // [datastore] vigie-<vm>, créé par l'appliance, supprimé avec le disque s'il est vide
	BackingUUID   string    `json:"backing_uuid"` // backing.uuid, visible dans l'invité comme wwid naa.<uuid>
	DeviceKey     int32     `json:"device_key"`
	ControllerKey int32     `json:"controller_key"`
	UnitNumber    int32     `json:"unit_number"`
	Provisioning  string    `json:"provisioning"` // eagerZeroedThick ou thin
	CreatedAt     time.Time `json:"created_at"`
	Mode          string    `json:"mode,omitempty"` // "" : créé via vCenter ; "local" : ajouté à la main puis adopté (phase T)
}
