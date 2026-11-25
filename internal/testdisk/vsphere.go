package testdisk

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/progress"
	"github.com/vmware/govmomi/vim25/types"

	"vigie/internal/vsphere"
)

// MaxSizeGB borne la taille du disque de test (§3.4 : jusqu'à 64 Go pour déborder le cache serveur).
const MaxSizeGB = 64

// Progress reçoit l'avancement d'une tâche vCenter (0-100) et un libellé.
type Progress func(percent int, msg string)

// reconfigure est LA fonction qui modifie la configuration de la VM côté vSphere (ajout et
// suppression du disque de test). Elle journalise l'opération et attend la fin de la tâche.
func reconfigure(ctx context.Context, vm *object.VirtualMachine, spec types.VirtualMachineConfigSpec, what string, prog Progress) error {
	log.Printf("testdisk: ReconfigVM %s sur %s : %s", vm.Reference(), what, describeSpec(spec))
	task, err := vm.Reconfigure(ctx, spec)
	if err != nil {
		return fmt.Errorf("%s : %w", what, err)
	}
	var sinks []progress.Sinker
	if prog != nil {
		sinks = append(sinks, progressSink{prog})
	}
	info, err := task.WaitForResultEx(ctx, sinks...)
	if err != nil {
		if info != nil && info.Error != nil {
			return fmt.Errorf("%s : %s", what, info.Error.LocalizedMessage)
		}
		return fmt.Errorf("%s : %w", what, err)
	}
	log.Printf("testdisk: ReconfigVM %s terminé (%s)", what, task.Reference())
	return nil
}

func describeSpec(spec types.VirtualMachineConfigSpec) string {
	var parts []string
	for _, c := range spec.DeviceChange {
		s := c.GetVirtualDeviceConfigSpec()
		d := s.Device.GetVirtualDevice()
		parts = append(parts, fmt.Sprintf("%s %T clé %d fichier=%s", s.Operation, s.Device, d.Key, s.FileOperation))
	}
	return strings.Join(parts, "; ")
}

// Create ajoute le disque de test à la VM sur le datastore donné : thick eager zeroed d'abord
// (fonctionne avec VAAI-NAS), sinon thin, sans erreur pour l'utilisateur (§2 du plan).
func Create(ctx context.Context, sess *vsphere.Session, vmRef, dsRef types.ManagedObjectReference, sizeGB int, progress Progress) (*Record, error) {
	if sizeGB < 1 || sizeGB > MaxSizeGB {
		return nil, fmt.Errorf("taille %d Go hors bornes (1 à %d)", sizeGB, MaxSizeGB)
	}
	ds, err := vsphere.Datastore(ctx, sess, dsRef, nil)
	if err != nil {
		return nil, err
	}
	vm := object.NewVirtualMachine(sess.Client, vmRef)
	devices, err := vm.Device(ctx)
	if err != nil {
		return nil, fmt.Errorf("périphériques de la VM : %w", err)
	}
	// Dossier dédié sur le datastore cible : [ds] vigie-<vm> (évite la racine du datastore et
	// toute collision avec un autre fichier).
	var vmMo mo.VirtualMachine
	if err := property.DefaultCollector(sess.Client).RetrieveOne(ctx, vmRef, []string{"name"}, &vmMo); err != nil {
		return nil, fmt.Errorf("nom de la VM : %w", err)
	}
	dc, err := datacenterOf(ctx, sess, dsRef)
	if err != nil {
		return nil, err
	}
	folder := fmt.Sprintf("[%s] vigie-%s", ds.Name, safeName(vmMo.Name))
	fileName := folder + "/testdisk.vmdk"
	log.Printf("testdisk: MakeDirectory %s", folder)
	if err := object.NewFileManager(sess.Client).MakeDirectory(ctx, folder, dc, true); err != nil && !isAlreadyExists(err) {
		return nil, fmt.Errorf("création du dossier %s : %w", folder, err)
	}
	before := map[int32]bool{}
	for _, d := range devices {
		before[d.GetVirtualDevice().Key] = true
	}
	ctrl := devices.PickController(&types.ParaVirtualSCSIController{})
	if ctrl == nil {
		ctrl = devices.PickController(&types.VirtualSCSIController{})
	}
	if ctrl == nil {
		return nil, errors.New("aucun contrôleur SCSI libre sur la VM")
	}
	sizeBytes := int64(sizeGB) << 30
	provisioning := "eagerZeroedThick"
	var lastErr error
	for attempt, eager := range []bool{true, false} {
		// "[ds]" seul : vSphere crée le VMDK dans un dossier au nom de la VM sur CE datastore
		// (un nom vide le placerait dans le dossier de la VM, donc sur son datastore système).
		disk := devices.CreateDisk(ctrl, dsRef, fileName)
		disk.CapacityInBytes = sizeBytes
		disk.CapacityInKB = sizeBytes / 1024
		backing := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)
		backing.DiskMode = string(types.VirtualDiskModePersistent)
		if eager {
			backing.ThinProvisioned = types.NewBool(false)
			backing.EagerlyScrub = types.NewBool(true)
		} else {
			backing.ThinProvisioned = types.NewBool(true)
			backing.EagerlyScrub = types.NewBool(false)
			provisioning = "thin"
		}
		spec, err := object.VirtualDeviceList{disk}.ConfigSpec(types.VirtualDeviceConfigSpecOperationAdd)
		if err != nil {
			return nil, err
		}
		err = reconfigure(ctx, vm, types.VirtualMachineConfigSpec{DeviceChange: spec},
			fmt.Sprintf("ajout du disque de test %d Go (%s) sur %s", sizeGB, provisioning, ds.Name), progress)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt == 0 {
			log.Printf("testdisk: thick eager zeroed refusé (%v), repli thin", err)
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	// Retrouver le disque ajouté (nouvelle clé) et son UUID.
	devices, err = vm.Device(ctx)
	if err != nil {
		return nil, fmt.Errorf("relecture des périphériques : %w", err)
	}
	for _, d := range devices.SelectByType(&types.VirtualDisk{}) {
		disk := d.(*types.VirtualDisk)
		if before[disk.Key] {
			continue
		}
		backing, ok := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)
		if !ok {
			continue
		}
		rec := &Record{
			VMRef: vmRef.String(), DatastoreRef: dsRef.String(), DatastoreName: ds.Name, Folder: folder,
			SizeBytes: disk.CapacityInBytes, FileName: backing.FileName, BackingUUID: backing.Uuid,
			DeviceKey: disk.Key, ControllerKey: disk.ControllerKey, Provisioning: provisioning, CreatedAt: time.Now().UTC(),
		}
		if disk.UnitNumber != nil {
			rec.UnitNumber = *disk.UnitNumber
		}
		if backing.ThinProvisioned != nil && *backing.ThinProvisioned {
			rec.Provisioning = "thin"
		}
		if rec.SizeBytes == 0 {
			rec.SizeBytes = disk.CapacityInKB * 1024
		}
		return rec, nil
	}
	return nil, errors.New("le disque a été ajouté mais n'est pas retrouvé dans la configuration de la VM")
}

// Lookup retrouve le disque de test dans la configuration vSphere de la VM (par clé et UUID).
func Lookup(ctx context.Context, sess *vsphere.Session, rec *Record) (*types.VirtualDisk, error) {
	vmRef, ok := vsphere.Ref(rec.VMRef)
	if !ok {
		return nil, errors.New("référence de VM invalide dans l'enregistrement")
	}
	devices, err := object.NewVirtualMachine(sess.Client, vmRef).Device(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range devices.SelectByType(&types.VirtualDisk{}) {
		disk := d.(*types.VirtualDisk)
		if disk.Key != rec.DeviceKey {
			continue
		}
		backing, ok := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)
		if !ok || !strings.EqualFold(backing.Uuid, rec.BackingUUID) {
			return nil, fmt.Errorf("le périphérique clé %d n'est pas le disque de test attendu", rec.DeviceKey)
		}
		return disk, nil
	}
	return nil, nil
}

// Remove supprime le disque de test (et son fichier VMDK) de la VM. Le disque est identifié par
// sa clé ET son UUID : on ne supprime jamais un autre disque.
func Remove(ctx context.Context, sess *vsphere.Session, rec *Record, progress Progress) error {
	disk, err := Lookup(ctx, sess, rec)
	if err != nil {
		return err
	}
	if disk == nil {
		return nil // déjà absent
	}
	vmRef, _ := vsphere.Ref(rec.VMRef)
	vm := object.NewVirtualMachine(sess.Client, vmRef)
	spec := types.VirtualMachineConfigSpec{DeviceChange: []types.BaseVirtualDeviceConfigSpec{&types.VirtualDeviceConfigSpec{
		Operation: types.VirtualDeviceConfigSpecOperationRemove, FileOperation: types.VirtualDeviceConfigSpecFileOperationDestroy, Device: disk,
	}}}
	if err := reconfigure(ctx, vm, spec, fmt.Sprintf("suppression du disque de test %s (clé %d)", rec.FileName, rec.DeviceKey), progress); err != nil {
		return err
	}
	removeFolderIfEmpty(ctx, sess, rec)
	return nil
}

// removeFolderIfEmpty supprime le dossier vigie-<vm> du datastore s'il ne contient plus rien.
// Jamais de suppression récursive d'un dossier non vide.
func removeFolderIfEmpty(ctx context.Context, sess *vsphere.Session, rec *Record) {
	if rec.Folder == "" {
		return
	}
	dsRef, ok := vsphere.Ref(rec.DatastoreRef)
	if !ok {
		return
	}
	ds := object.NewDatastore(sess.Client, dsRef)
	browser, err := ds.Browser(ctx)
	if err != nil {
		log.Printf("testdisk: navigateur de datastore indisponible : %v", err)
		return
	}
	task, err := browser.SearchDatastore(ctx, rec.Folder, &types.HostDatastoreBrowserSearchSpec{MatchPattern: []string{"*"}})
	if err != nil {
		return
	}
	info, err := task.WaitForResultEx(ctx)
	if err != nil {
		return
	}
	res, ok := info.Result.(types.HostDatastoreBrowserSearchResults)
	if !ok || len(res.File) > 0 {
		log.Printf("testdisk: dossier %s conservé (%d fichier(s))", rec.Folder, len(res.File))
		return
	}
	dc, err := datacenterOf(ctx, sess, dsRef)
	if err != nil {
		return
	}
	log.Printf("testdisk: suppression du dossier vide %s", rec.Folder)
	if t, err := object.NewFileManager(sess.Client).DeleteDatastoreFile(ctx, rec.Folder, dc); err == nil {
		_ = t.WaitEx(ctx)
	}
}

// datacenterOf remonte la hiérarchie d'un objet jusqu'à son Datacenter.
func datacenterOf(ctx context.Context, sess *vsphere.Session, ref types.ManagedObjectReference) (*object.Datacenter, error) {
	pc := property.DefaultCollector(sess.Client)
	cur := ref
	for i := 0; i < 16; i++ {
		if cur.Type == "Datacenter" {
			return object.NewDatacenter(sess.Client, cur), nil
		}
		var me mo.ManagedEntity
		if err := pc.RetrieveOne(ctx, cur, []string{"parent"}, &me); err != nil {
			return nil, fmt.Errorf("datacenter de %s : %w", ref, err)
		}
		if me.Parent == nil {
			break
		}
		cur = *me.Parent
	}
	return nil, fmt.Errorf("datacenter de %s introuvable", ref)
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func isAlreadyExists(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "already exist") || strings.Contains(err.Error(), "FileAlreadyExists")
}
