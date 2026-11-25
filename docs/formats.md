# Formats de livraison

`make release` produit les trois formats depuis le même média (Alpine diskless, binaire vigie) :

| Fichier | Taille | Usage |
|---|---|---|
| `dist/vigie-<ver>.ova` | ≈ 45 Mo | vSphere (vCenter ou ESXi) : propriétés OVF (IP, mot de passe…) |
| `dist/vigie-<ver>.qcow2` | ≈ 45 Mo | Proxmox, OpenStack, KVM : métadonnées cloud (NoCloud, config-drive, 169.254.169.254) |
| `dist/vigie-<ver>.iso` | ≈ 50 Mo | ISO live BIOS/UEFI : tout en RAM, **rien n'est conservé**, même si un disque est présent |

## ISO live
- Démarre sur n'importe quelle VM (VMware BIOS/EFI, Proxmox, KVM) ou machine physique.
- Paramètre noyau `vigie.live` : `/data` n'est jamais monté ; mot de passe généré à chaque démarrage et
  affiché sur la console ; l'accueil indique « mode live ».

## Proxmox
```
qm create 9100 --name vigie --memory 1024 --cores 2 --net0 virtio,bridge=vmbr0 --scsihw virtio-scsi-pci
qm importdisk 9100 vigie-<ver>.qcow2 local-lvm
qm set 9100 --scsi0 local-lvm:vm-9100-disk-0 --boot order=scsi0 --agent enabled=1
# facultatif : nom d'hôte, mot de passe, clé SSH par cloud-init (lecteur « cidata » NoCloud)
qm set 9100 --ide2 local-lvm:cloudinit --ciuser root --cipassword 'MotDePasse-1' --sshkeys ~/.ssh/id_ed25519.pub
```
Disques de test (onglet Storage de la console) : contrôleur VirtIO SCSI, `cache=none`.

## OpenStack
```
openstack image create vigie --disk-format qcow2 --container-format bare --file vigie-<ver>.qcow2 \
  --property hw_scsi_model=virtio-scsi --property hw_disk_bus=scsi --property hw_qemu_guest_agent=yes
openstack server create --image vigie --flavor m1.small --network <réseau> --key-name <clé> [--config-drive true] vigie
```
Lus au démarrage : `hostname`, `public_keys` (→ root), `admin_pass`, et un `#cloud-config`
(`hostname`, `password` ou `vigie_password`, `ssh_authorized_keys`). Aucun script n'est exécuté.

## Priorités
Ce qui est choisi dans la console de l'appliance (réseau, mot de passe) prime sur les propriétés OVF et
les métadonnées cloud. SSH demande toujours le mot de passe root (ou une clé fournie par le cloud).
