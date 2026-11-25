# Vigie

Appliance de diagnostic pour vSphere : **performances d'un datastore** mesurées depuis l'hôte ESXi où
elle tourne, et **diagnostic réseau** (ping, surveillance façon smokeping, traceroute, ARP, iperf3).
Une seule petite VM Alpine Linux (≈ 45 Mo), pilotée par une interface web embarquée, par l'API ou
au clavier depuis la console de la VM.

- Aucun SSH vers les ESXi : tout passe par l'API vSphere (vCenter de préférence, ESXi seul possible).
- Aucune dépendance réseau : interface, graphiques et polices sont dans le binaire ; fonctionne sur
  un réseau isolé.
- Lecture seule côté infrastructure, à l'exception d'opérations explicites (voir « Ce que l'appliance
  modifie »).
- Interface et console en anglais (défaut), français, espagnol, italien, polonais et allemand.

## Formats

| Fichier | Taille | Pour |
|---|---|---|
| `vigie-<version>.ova` | ≈ 45 Mo | vSphere (vCenter ou ESXi) |
| `vigie-<version>.qcow2` | ≈ 45 Mo | Proxmox, OpenStack, KVM |
| `vigie-<version>.iso` | ≈ 50 Mo | ISO live BIOS/UEFI, rien n'est conservé |

Proxmox, OpenStack et l'ISO live sont décrits dans [docs/formats.md](docs/formats.md). La suite de ce
document traite du déploiement sur vSphere.

## Vérifier une release

Les images se téléchargent depuis les [releases GitHub](https://github.com/fluatovh/vigie/releases),
avec `SHA256SUMS` et sa signature `SHA256SUMS.sigstore.json`. Avant de déployer :

```sh
# 1. intégrité : les fichiers téléchargés correspondent aux sommes publiées
sha256sum -c SHA256SUMS --ignore-missing          # macOS : shasum -a 256 -c SHA256SUMS --ignore-missing

# 2. authenticité : SHA256SUMS a été signé par le workflow de release de ce dépôt, pour ce tag
cosign verify-blob --bundle SHA256SUMS.sigstore.json \
  --certificate-identity https://github.com/fluatovh/vigie/.github/workflows/release.yml@refs/tags/vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS

# 3. provenance (facultatif) : l'image a été construite par GitHub Actions depuis ce dépôt
gh attestation verify vigie-vX.Y.Z.ova --repo fluatovh/vigie
```

[cosign](https://docs.sigstore.dev/cosign/system_config/installation/) et la [CLI GitHub](https://cli.github.com/)
s'installent en un binaire. La somme seule (étape 1) détecte un téléchargement corrompu ; la
signature (étape 2) garantit en plus que les sommes viennent bien de ce dépôt. MD5 n'est pas
fourni : il ne protège plus contre une falsification volontaire.

## Déployer sur vSphere

### Import de l'OVA

vSphere Client : *Déployer un modèle OVF* → fichier `vigie-<version>.ova` → choisir l'hôte et le
réseau. **Placez l'appliance sur l'hôte que vous voulez mesurer** : elle teste le chemin de l'hôte où
elle tourne (elle sait ensuite se déplacer par vMotion, voir plus bas).

Matériel de la VM : 2 vCPU, 1 Go de RAM, disque système PVSCSI, carte vmxnet3, CD-ROM SATA (porte la
configuration OVF), matériel virtuel `vmx-14` (ESXi 6.7 et plus récents).

### Propriétés OVF

Toutes facultatives. Sans adresse IP, l'appliance prend une adresse en DHCP.

| Propriété | Rôle |
|---|---|
| `hostname` | Nom d'hôte de l'appliance |
| `ip`, `netmask`, `gateway` | Adresse IPv4 statique ; vide : DHCP |
| `dns` | Serveurs DNS, séparés par des virgules (IP statique) |
| `ntp` | Serveurs NTP, séparés par des virgules ; vide : pool.ntp.org |
| `ui_password` | Mot de passe root (SSH) et de l'interface web ; vide : généré au premier démarrage |

En ligne de commande avec govc :

```sh
govc import.spec vigie-<version>.ova > vigie.json      # puis renseigner PropertyMapping et NetworkMapping
govc import.ova -name vigie -options vigie.json vigie-<version>.ova
govc vm.power -on vigie
```

Le réseau et le mot de passe choisis ensuite dans la console de l'appliance priment sur les
propriétés OVF.

## Premier accès

Ouvrez la console de la VM. L'écran d'accueil (façon DCUI) affiche en permanence :

- l'adresse IP et l'URL de l'interface web ;
- le **mot de passe** (fourni par OVF ou généré au premier démarrage) ;
- l'empreinte SHA-256 du **certificat HTTPS**. Ce certificat auto-signé est généré au premier
  démarrage et propre à chaque appliance : comparez l'empreinte à celle que montre le navigateur.

Ensuite :

- **Interface web** : `https://<ip>/` (le port 80 redirige vers HTTPS), avec le mot de passe affiché.
- **Console** : Entrée ouvre l'interface texte au clavier (tests réseau, tests de stockage sans
  vCenter, configuration réseau, mot de passe, langue). F12 : arrêt ou redémarrage. Alt+F2 : shell
  root sans mot de passe (l'accès à la console de la VM vaut un accès physique).
- **SSH** : `ssh root@<ip>`, avec le mot de passe.

## Mesurer un datastore

Onglet **Datastore performance** de l'interface web, en étapes :

1. **Connexion vSphere** : URL du vCenter (ou de l'ESXi), compte, mot de passe. L'empreinte du
   certificat du serveur est affichée et doit être confirmée. Cochez « Mémoriser le mot de passe »
   pour la reconnexion automatique après un redémarrage (chiffré dans le disque de l'appliance).
2. **Identification** : l'appliance se retrouve dans l'inventaire (VM, hôte, cluster). La page
   indique les privilèges vSphere manquants pour le compte utilisé.
3. **Datastore** : choix du datastore et affichage du chemin NFS (vmk, vSwitch, uplinks, voisin
   CDP/LLDP quand le switch s'annonce).
4. **Disque de test** : l'appliance crée son propre VMDK (1 à 64 Go) sur le datastore, le vérifie
   avant toute écriture (§6.3 du plan : jamais d'écriture sur un disque non vérifié), puis le
   préremplit de données incompressibles au premier test.
5. **Hôtes** : déplacer l'appliance par vMotion, ou lancer une tournée de comparaison entre hôtes.
6. **Type de test** :
   - *standard* : ligne de base, écriture puis lecture séquentielles (1 Mio, QD 4), ratio TX/écrit,
     erreurs des vmnics (dont CRC, trame et longueur via esxcli), latence vue par l'hôte, verdict ;
   - *avancé* : le standard, puis IOPS et latences en 4 Kio aléatoire à QD 1, 4, 16 et 32 ;
   - *surveillance continue* : un test court toutes les N minutes.
7. **Exécution** en direct, 8. **Résultats** avec verdict et profil d'I/O complet,
   9. **Nettoyage** (suppression du disque de test).

Chaque test standard, avancé ou tournée produit un **rapport client** HTML autonome, imprimable en A4,
avec avant/après entre deux tests (onglet Historique). Les seuils des verdicts se règlent par
préréglage (all-flash, hybride, disques) dans Réglages.

### Sans vCenter (mode local)

Depuis la console de l'appliance, onglet Storage : l'appliance explique comment ajouter un disque à
sa VM dans vSphere (taille, contrôleur), puis adopte ce disque après des contrôles stricts (apparu
après la demande, vierge, nom retapé). Les tests locaux mesurent le débit et la latence sans les
compteurs de l'hôte. L'appliance ne retire ni n'efface jamais ce disque.

## Diagnostic réseau

Onglet **Network** : interfaces de l'appliance, ping et traceroute en direct, surveillance continue de
cibles (20 pings par minute, historique d'un an), table ARP, débit iperf3 TCP ou UDP vers une autre
appliance Vigie. Outils disponibles en SSH : nmap, tcpdump, ethtool, smartctl, ipmitool, iperf3.

## Surveillance continue et tendances

Étape 6, « Surveillance continue » : un test standard (ou local) toutes les 15 minutes à 24 heures sur
l'hôte courant. Un test est sauté, avec la raison affichée, si un autre job tourne ou si vCenter
n'est pas joignable. Les mesures alimentent la carte **Tendances** de l'onglet Historique (débit,
latence p99, ratio TX/écrit par hôte et datastore), exportable en CSV ou JSON. Rétention réglable
(30 jours par défaut).

## API

Toute l'interface repose sur une API JSON. Créez un jeton dans **Réglages → Jetons d'API**, puis :

```sh
V=https://<ip>/api; T="Authorization: Bearer vg_..."
curl -k -H "$T" $V/vsphere/status
curl -k -H "$T" -H 'Content-Type: application/json' -X POST $V/jobs -d '{"profile":"standard"}'
curl -k -H "$T" $V/jobs/<id>                  # état et résultat
curl -k -H "$T" -N $V/jobs/<id>/events        # progression en direct (Server-Sent Events)
curl -k -H "$T" $V/reports/<id> > rapport.html
curl -k -H "$T" "$V/results?period=7d&format=csv" > mesures.csv
```

Profils de job : `standard`, `advanced`, `local`, `tour`, `move`, `net.iperf`, `testdisk.create`,
`testdisk.delete`. Un seul job à la fois (HTTP 409 sinon).

## Ce que l'appliance modifie

Tout le reste est en lecture seule. Ces opérations sont toujours déclenchées par l'utilisateur :

- création et suppression de **son** disque de test (sur sa propre VM) ;
- vMotion de **l'appliance elle-même** ;
- en option, un override DRS sur **l'appliance elle-même** pendant une tournée.

Les commandes esxcli passent par l'API vSphere et sont limitées à une liste blanche en lecture
(`network nic stats get`, `network nic list`, `network nic get`).

## Limites

- Pas de compteur de retransmissions TCP du vmkernel : le ratio TX/écrit est un indicateur
  indirect, sensible au trafic des autres VMs sur la même vmnic.
- Granularité de 20 s des compteurs vSphere : les micro-coupures plus courtes sont lissées ; un test
  trop court rend le ratio TX/écrit non concluant.
- Les IOPS du test avancé incluent l'effet du cache du serveur NFS : c'est ce que voit une VM, pas la
  mesure des disques du serveur.
- Les erreurs d'émission de l'hôte ne se voient que côté switch, que l'appliance n'interroge pas.
- L'appliance mesure l'hôte où elle tourne : il faut la déplacer (ou en déployer une autre) pour un
  autre hôte.

## Construire et tester

Sur une machine Linux avec Docker : `make release` produit l'OVA, l'ISO et le qcow2 dans `dist/`.

| Cible | Contrôle |
|---|---|
| `make fmt-check vet test` | formatage, analyse statique, tests Go (dont le simulateur vcsim et le catalogue des langues) |
| `make release` | construction des trois formats et contrôle des budgets de taille |
| `make ci-qemu` | démarrage de l'ISO (BIOS, UEFI, avec un disque présent) et du qcow2 (NoCloud, config-drive) sous QEMU |
| `make ci-ui` | interface dans Chromium headless contre le qcow2 démarré : connexion, onglets, ping, jeton d'API, seuils, langues, CSP, aucune requête externe |

La CI GitHub les rejoue à chaque push et pull request ; un tag `vX.Y.Z` déclenche en plus la
publication signée de la release. Les tests contre un vrai vSphere (`image/lab-qa-*.sh`) se lancent à
la main avec un `lab.env` local. Versions figées : `image/versions.mk`, `go.mod`, `web/vendor/`. Plan et décisions :
[docs/PLAN.md](docs/PLAN.md), [docs/STATUS.md](docs/STATUS.md), notes de version :
[docs/RELEASE-NOTES.md](docs/RELEASE-NOTES.md).

## Licence

[Apache 2.0](LICENSE).
