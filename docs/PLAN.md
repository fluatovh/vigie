# vigie — appliance de bench datastore NFS pour vSphere

> Plan initial rédigé sous le nom « dsprobe » ; projet renommé **Vigie** le 18/11/2026 (le nom est
> remplacé partout ci-dessous). Le diagnostic réseau (§11) a été ajouté le même jour.

Plan d'implémentation destiné à Claude Code. Rédigé le 16/11/2026, à partir de la méthode manuelle
« bench / vérification NFS depuis un ESXi » (SSH root + `dd` + `vsish` + `pktcap-uw`).

## 0. Objectif et contraintes non négociables

Une **seule VM**, Alpine Linux minimale, livrée en **OVA d'environ 50 Mo** (plafond dur : 100 Mo, qui n'est pas un objectif ; voir §4.4),
qui mesure les performances d'un datastore depuis l'hôte ESXi sur lequel elle tourne. Elle se
pilote entièrement par une **interface web auto-hébergée**.

Contraintes :

- **Aucun SSH vers les ESXi.** Tout passe par l'API vSphere (vCenter de préférence ; un ESXi seul
  fonctionne, mais sans vMotion). Le SSH n'existe que *vers* l'appliance, pour l'admin.
- **Aucune dépendance réseau au runtime** : pas de CDN, pas de Google Fonts. Tout (JS, CSS,
  graphs) est embarqué dans le binaire. L'appliance doit marcher sur un réseau isolé.
- **Lecture seule côté infra**, sauf ces opérations explicites, toutes déclenchées par l'utilisateur
  dans l'UI : créer et supprimer *le* disque de test de l'appliance, vMotion de *l'appliance
  elle-même*, et en option un override DRS sur *l'appliance elle-même*.
- **Jamais d'écriture brute sur un périphérique bloc non vérifié** (voir §6.3). C'est la seule
  règle dont la violation détruit des données ; elle prime sur tout le reste.

## 1. Décisions d'architecture (et pourquoi)

| Sujet | Décision | Raison |
|---|---|---|
| Langage backend | **Go**, binaire statique (`CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`) | `govmomi` est la lib vSphere officielle. Un seul binaire d'environ 15 à 25 Mo, sans runtime. Python + pyVmomi dépasse à lui seul le budget de 50 Mo. |
| Moteur de bench | **Natif Go**, `O_DIRECT` sur le périphérique bloc brut (pas de FS, pas de fio) | Aucun cache invité, pas d'overhead de système de fichiers, mesure de latence par I/O, zéro dépendance. |
| Front | HTML + JS vanilla + **uPlot** (environ 45 Ko), embarqués via `embed.FS` | Les graphs sont rendus **par le navigateur** : la VM ne sert que du JSON. |
| Progression | **Server-Sent Events** (`/api/jobs/{id}/events`) | Unidirectionnel, natif navigateur, plus simple que les WebSockets. |
| Stockage | JSON Lines (un fichier par jour) + index en mémoire | Volumes minuscules ; évite SQLite et ses quelques Mo. |
| OS | Alpine `linux-virt` en **mode diskless (run-from-RAM)** | Une fois démarrée, l'appliance ne dépend plus de son disque système. Si le datastore testé rame ou décroche, l'UI et le collecteur restent vivants. C'est précisément le moment où on en a besoin. |
| SSH admin | `dropbear` (plus léger qu'OpenSSH) | Demandé ; à désactiver depuis l'UI si souhaité. |
| Config initiale | Propriétés OVF, **transport ISO** (`ovf-env.xml` sur un CD virtuel) | Fonctionne sans VMware Tools. |
| VMware Tools | `open-vm-tools` **optionnel**, arbitré en phase 1 selon la taille mesurée | Utile (IP affichée dans vSphere, arrêt propre) mais tire glib. |
| vCPU / RAM | **2 vCPU, 1 Go** | Voir §1.1. |
| Contrôleur disque | PVSCSI (`vmw_pvscsi`) ; repli SATA/AHCI si absent de `linux-virt` | À vérifier en phase 1. |
| Hardware version | `vmx-14` (ESXi 6.7+) | Compatibilité large. |

### 1.1 Pourquoi pas 3 à 5 vCPU avec un vCPU dédié à la collecte

Le bench est limité par l'I/O, pas par le CPU : quelques goroutines en `O_DIRECT` consomment
quelques pourcents d'un cœur. Le collecteur et le serveur web font quelques requêtes HTTP toutes
les 20 s. Les graphs sont calculés dans le navigateur. Au-delà de 2 vCPU, on ajoute surtout de
l'attente de co-scheduling côté ESXi, ce qui perturbe la mesure.

On garde quand même l'idée d'isolation, à moindre coût. Le binaire lance le moteur de bench comme
**sous-processus** (`vigie worker`), épinglé sur le CPU 1 via `sched_setaffinity`. Le serveur web
et le collecteur restent sur le CPU 0. Ainsi le GC et le service HTTP ne polluent pas les
chronos de latence. Dans une VM, l'épinglage invité ne garantit pas un cœur physique, mais il
sépare proprement les deux charges.

Exception : en test avancé, sur un stockage très rapide (plusieurs dizaines de milliers d'IOPS en
4 Kio QD32), un seul vCPU de worker peut devenir le goulet. Le worker mesure donc son propre usage
CPU (§3.4). S'il sature, l'UI propose de passer la VM à **4 vCPU** ; le worker prend alors les
CPU 1 à n−1. 2 vCPU reste la valeur par défaut de l'OVA.

## 2. Remplissage du disque thin : réponse à « remplir de zéros, ça marche ? »

Pas de façon fiable sur NFS :

- Sans plugin VAAI-NAS, un VMDK sur NFS est **toujours thin** : c'est le serveur NFS qui décide de
  l'allocation, et « eager zeroed thick » n'est pas disponible.
- Beaucoup de serveurs NFS (ZFS avec compression, NetApp, la plupart des NAS récents) **détectent
  les blocs de zéros** et ne les allouent pas. Le fichier reste creux et la première vraie
  écriture repaie le coût d'allocation. Le remplissage n'aura donc servi à rien.
- Le même biais touche la mesure elle-même : écrire des zéros mesure la compression du serveur,
  pas le chemin réseau. C'est la limite déjà notée sur le `dd if=/dev/zero` actuel.

**Décision :**

1. Créer le disque en essayant d'abord thick eager zeroed (cela marche si VAAI-NAS est présent),
   puis repli thin, sans erreur pour l'utilisateur.
2. **Préremplir une fois avec des données pseudo-aléatoires incompressibles**, avec barre de
   progression. On génère un pool aléatoire de 64 Mo, puis chaque bloc est XORé avec son numéro
   de bloc pour qu'aucun bloc ne soit dédupliquable. Coût CPU négligeable, débit limité par le
   réseau seulement.
3. Écrire un **en-tête de signature** (bloc 0) avec la date de remplissage et la taille préremplie,
   pour ne pas refaire le remplissage après un reboot.
4. Les benchs **réécrivent** ensuite la zone préremplie avec des données elles aussi aléatoires.
   On mesure ainsi un état stable du serveur.

## 3. Comment la mesure remplace SSH

| Méthode manuelle (SSH) | Équivalent appliance (API) |
|---|---|
| `dd` 256 Mo chronométré | Bench natif `O_DIRECT`, au temps (voir §3.1), débit + latences p50/p95/p99 |
| Octets TX vmnic via esxcli | PerformanceManager `net.bytesTx` / `net.transmitted` par vmnic (échantillons de 20 s) |
| Ratio TX / écrit | Même ratio, avec **soustraction d'une ligne de base** (voir §3.2) |
| Delta `vsish` sndrexmitpack | **Pas d'équivalent direct.** C'est le ratio TX/écrit corrigé qui en tient lieu |
| `esxcli network nic stats` (CRC…) | PerformanceManager `net.errorsRx`, `net.errorsTx`, `net.droppedRx/Tx` ; en option esxcli via API (§8, phase 10) |
| `NFS: 6` dans vmkernel.log | `DiagnosticManager.BrowseDiagnosticLog` (clé `vmkernel`), comptage des nouvelles lignes |
| `uplink.transition.down` dans vobd.log | Événements vCenter (`EventManager`, événements de lien vmnic) |
| vmk → vSwitch → vmnic | `HostNetworkSystem` (config réseau de l'hôte) |
| LLDP via `pktcap-uw` | `QueryNetworkHint` (CDP/LLDP selon le type de vSwitch), sans garantie sur vSwitch standard |
| Comparatif simultané de 2 hôtes | **Tournée séquentielle par vMotion** avec alternance A/B/A/B (§3.3) |
| *(absent)* | **Bonus** : `datastore.totalWriteLatency` / `totalReadLatency` vus par l'hôte, `virtualDisk.*` vus par la VM |

### 3.1 Fenêtres de mesure et échantillons de 20 s

Le PerformanceManager temps réel donne un échantillon toutes les 20 s, disponible avec 20 à 40 s
de retard. Le bench est donc **piloté au temps et non à la taille** :

- Ligne de base au repos : **60 s** (3 échantillons).
- Écriture continue : **80 s** par défaut. La zone de test est parcourue en boucle et on compte les
  octets écrits par seconde.
- Lecture continue : 80 s. Elle permet le même calcul côté RX (retransmissions serveur → hôte).
- Attente de la disponibilité des échantillons, affichée dans la barre de progression.
- **On ne garde que les échantillons entièrement contenus dans la fenêtre.** Les bords sont jetés,
  et les octets écrits sont recomptés exactement sur ces intervalles. Cela demande un journal
  d'écriture à la seconde.

**Horloge** : ne pas se fier au NTP invité. Lire l'heure du serveur via
`ServiceInstance.CurrentTime()` au début et à la fin du job, calculer le décalage, et exprimer
toutes les fenêtres en heure serveur.

### 3.2 Ratio TX / écrit corrigé

```
TX_bench   = Σ (débit TX des uplinks NFS × 20 s) sur les échantillons de la fenêtre
TX_fond    = débit TX moyen de la ligne de base × durée de la fenêtre
ratio      = (TX_bench − TX_fond) / octets_écrits
```

- Les uplinks NFS se déterminent ainsi : IP du serveur NFS (`info.nas.remoteHost`), puis le vmk
  dans le même sous-réseau (ou la route), puis son portgroup, puis son vSwitch, puis les **uplinks
  actifs** selon la politique de teaming. S'il y en a plusieurs, on somme et on affiche aussi le
  détail par vmnic.
- Fiabilité : si l'écart-type de la ligne de base dépasse 20 % de la moyenne (trafic des autres VMs),
  le ratio est marqué **« peu fiable »** et l'UI suggère une plage plus calme ou une écriture plus
  longue.
- Seuils repris de la méthode manuelle : ratio > 1,10 = ALERTE ; 1,05 à 1,10 = ATTENTION.

### 3.3 Comparatif entre hôtes avec une seule VM

La simultanéité est perdue ; on compense par l'alternance. Pour une tournée {A, B} : bench sur A,
vMotion vers B, bench sur B, retour sur A, nouveau bench, puis B (cycles configurables, 2 par
défaut). Une dérive de charge du serveur NFS touche alors les deux hôtes à parts égales. Le verdict
compare la **médiane par hôte** ; un écart supérieur à ×3 sur le débit d'écriture déclenche une
alerte, comme dans la méthode manuelle.

### 3.4 Deux types de tests

| | **Test standard** | **Test avancé** |
|---|---|---|
| Question posée | Le chemin hôte → datastore est-il sain ? | Que vaut ce datastore pour des VMs ? |
| Durée par hôte | environ 4 min | environ 13 min (environ 30 min en option « complet ») |
| Contenu | Ligne de base 60 s, écriture séquentielle 1 Mio QD4 80 s, lecture 80 s | Le test standard, puis les paliers aléatoires ci-dessous |
| Résultats | Débit, latence, ratio TX/écrit, erreurs, avertissements | Les mêmes, plus IOPS, courbe IOPS/latence, distribution des latences |
| Usage | Diagnostic, surveillance continue, comparaison d'hôtes | Qualification, **rapport client**, avant/après intervention |

**Matrice du test avancé :**

- Aléatoire **4 Kio** : lecture 100 %, écriture 100 %, mixte 70/30 lecture/écriture.
- Paliers de profondeur de file **QD 1, 4, 16, 32**, soit 12 paliers.
- Chaque palier : 5 s de chauffe (non comptées), puis **40 s de mesure**. La mesure couvre ainsi au
  moins un échantillon PerformanceManager complet, ce qui permet le contrôle croisé des IOPS côté
  hôte.
- Option « complet » : mêmes paliers en **8 Kio** et **64 Kio**.

**Les trois chiffres titres** (ce qu'on montre en premier au client) :

1. **Débit** : écriture et lecture séquentielles 1 Mio, en Mo/s.
2. **IOPS utiles** : en 4 Kio aléatoire, les IOPS du palier le plus élevé dont la **p99 reste sous
   un plafond** (5 ms par défaut, réglable). C'est un chiffre honnête : des IOPS obtenues avec 40 ms
   de latence ne servent à rien à une VM. Le maximum brut figure dans le détail.
3. **Latence** : 4 Kio QD1, p50 et p99, en lecture et en écriture. C'est l'aller-retour pur
   VM → hôte → NFS → disque, le chiffre qui parle aux applications. C'est aussi le plus sensible
   aux pertes réseau : une seule retransmission TCP se voit immédiatement sur la p99.

**Garde-fous d'honnêteté**, affichés dans le rapport s'ils se déclenchent :

- **IOPS appliance vs IOPS hôte** (`datastore.numberReadAveraged` / `numberWriteAveraged`, et
  `virtualDisk.*` pour la VM) : un écart supérieur à 10 % marque le palier « mesure à vérifier ».
- **CPU du worker supérieur à 85 %** sur un palier : le résultat est marqué « limité par
  l'appliance, pas par le stockage ». L'UI suggère alors de passer la VM à 4 vCPU (voir §1.1).
- **Zone de test petite face au cache serveur** : rappel que les IOPS mesurées incluent le cache du
  serveur NFS. La taille de zone est indiquée dans le rapport, et on peut l'agrandir (jusqu'à
  64 Go) pour un test qui sollicite vraiment les disques.

## 4. Image OVA

### 4.1 Contenu

- Alpine (dernière stable au moment du build, version **figée** dans le Makefile), `linux-virt`,
  `alpine-base`, `dropbear`, `chrony` ou le `ntpd` de busybox, et éventuellement `open-vm-tools`
  (phase 1).
- `vigie` empaqueté en **.apk local** (via `abuild`), signé avec une clé de build, et placé dans
  le cache apk du média de boot. Le mode diskless le réinstalle à chaque démarrage.
- Un apkovl (`vigie.apkovl.tar.gz`) contenant `/etc` : world, services OpenRC, réseau,
  `/etc/issue`.

### 4.2 Disque système (un seul VMDK de 512 Mo, thin)

- Partition 1 (FAT32 ou ext4, environ 200 Mo) : noyau, initramfs, modloop, cache apk, apkovl,
  syslinux/extlinux. Montée en lecture seule après le boot.
- Partition 2 (ext4, le reste) : `/data`, qui contient la config persistée (identifiants vSphere
  chiffrés si l'option est choisie, mot de passe UI hashé, certificat TLS) et l'historique JSONL.
  **Toutes les écritures sur `/data` passent par une goroutine dédiée avec timeout** : un datastore
  malade ne doit jamais bloquer l'UI ni le bench.

### 4.3 Propriétés OVF (transport ISO)

`hostname`, `ip` (vide = DHCP), `netmask`, `gateway`, `dns`, `ntp`, `ui_password` (de type
password). Au premier boot, un script OpenRC monte le CD, lit `ovf-env.xml` et applique la config.
Si `ui_password` est vide, l'appliance génère un **jeton de premier accès** affiché sur la console.

Ajouter dans `extraConfig` : `disk.EnableUUID = TRUE`. C'est indispensable pour identifier le disque
de test de façon sûre (§6.3).

### 4.4 Chaîne de build (reproductible, dans Docker)

1. `go build` statique, puis `abuild` → `vigie-<ver>.apk`.
2. Dans un conteneur Alpine `--privileged` (losetup) : création du raw, partitions, copie des
   fichiers de boot issus de l'ISO `alpine-virt`, `apk fetch --recursive` des paquets du world
   vers le cache, génération de l'apkovl, installation d'extlinux.
3. `qemu-img convert -O vmdk -o subformat=streamOptimized,adapter_type=lsilogic disk.raw vigie.vmdk`
   (le contrôleur réel est déclaré dans l'OVF).
4. Génération de l'OVF depuis un template, du manifeste `.mf` (SHA256), puis `tar` dans l'ordre
   **ovf, mf, vmdk** → `vigie-<ver>.ova`.
5. **Contrôle de taille dans le build** : échec si l'OVA dépasse **100 Mo**, avertissement au-delà
   de **60 Mo**. La marge jusqu'à 100 Mo est une tolérance, pas un budget à consommer. Afficher le
   détail des tailles (noyau, modloop, apks, binaire).
6. **Anti-gâchis** : `image/size-budget.json` fixe un budget par composant (valeurs du §4.5,
   ajustées après la phase 1). Le build échoue si un composant dépasse son budget, ou si l'OVA
   grossit de plus de 2 Mo par rapport au build de référence (`image/size-baseline.json`), sans mise
   à jour explicite de ce fichier dans le même commit, avec une justification dans le message.
   Tout nouveau paquet apk ou toute nouvelle dépendance Go doit indiquer son coût en Mo dans le
   commit. `go mod why` et `go tool nm -size -sort size` servent à traquer ce qui gonfle le binaire.

### 4.5 Budget de taille (à mesurer en phase 1)

| Élément | Estimation |
|---|---|
| Noyau + initramfs `linux-virt` | environ 15 Mo |
| modloop-virt (élaguable) | environ 15 à 20 Mo |
| apks de base + dropbear | environ 5 Mo |
| Binaire vigie (front embarqué) | environ 15 à 25 Mo, compressé à environ 8 à 12 Mo dans le VMDK |
| open-vm-tools + glib (optionnel) | environ 5 à 8 Mo |
| **OVA compressée** | **environ 40 à 60 Mo** |

Si le budget déborde, élaguer le modloop : on ne garde que vmxnet3, vmw_pvscsi, ahci, ext4, vfat,
iso9660 et loop. Ne pas utiliser UPX sur le binaire (faux positifs antivirus, démarrage plus lent).

## 5. Interface web

HTTPS uniquement, avec un certificat auto-signé généré au premier boot (empreinte affichée sur la
console). Authentification par mot de passe et session en cookie `HttpOnly`/`Secure`/`SameSite=Strict`.
Une seule page, organisée en étapes.

### 5.1 Parcours

1. **Connexion vSphere** : URL, utilisateur, mot de passe. Afficher l'**empreinte SHA-256** du
   certificat vCenter et demander confirmation (confiance au premier usage, empreinte mémorisée).
   Jamais de `insecure=true` silencieux. Option « mémoriser les identifiants » décochée par défaut ;
   sans elle, ils ne vivent qu'en RAM.
2. **Auto-identification** : l'appliance lit `/sys/class/dmi/id/product_uuid` et appelle
   `SearchIndex.FindByUuid` (essayer les deux ordres d'octets de l'UUID SMBIOS). Elle affiche
   « Je suis la VM X sur l'hôte Y (cluster Z) ». En cas d'échec, on choisit la VM dans une liste
   filtrée par adresse MAC.
3. **Datastore** : liste des datastores visibles par l'hôte courant, NFS en premier, avec serveur,
   export, version NFS, hôtes qui le montent et espace libre.
4. **Disque de test** : taille (1, 4 ou 8 Go, 4 par défaut), création, remplissage avec barre de
   progression. Si un disque de test existe déjà (signature valide), on le réutilise.
5. **Hôtes** (optionnel) : rester sur l'hôte courant, déplacer l'appliance vers un hôte choisi, ou
   tournée de comparaison (multi-sélection). Prévalidation via `CheckMigrate` /
   `QueryVMotionCompatibility` : datastore monté, portgroup présent sur la destination, vMotion
   possible. Si DRS est en mode automatique, proposer un override « manuel » pour l'appliance
   seule, sinon DRS risque de la déplacer pendant la mesure.
6. **Type de test** : *Standard* (§3.4, avec 1 ou 3 répétitions), *Avancé* (§3.4, option
   « complet » et plafond de latence des IOPS utiles réglables), ou *Surveillance continue*
   (standard toutes les N min, hôte courant uniquement). L'UI affiche la durée estimée avant le
   lancement, tournée de vMotion comprise.
7. **Exécution** : barre de progression globale, étape en cours, journal en direct, et mini-graph
   du débit à la seconde pendant l'écriture.
8. **Résultats**, en deux vues :
   - **Technique** : cartes par hôte (débit, latences p50/p99, IOPS par palier, ratio TX/écrit
     corrigé, erreurs NIC, latence datastore vue par l'hôte, nouveaux avertissements NFS, flaps),
     verdict OK / ATTENTION / ALERTE avec sa justification, et graphs d'historique.
   - **Rapport client** (§5.4) : la même mesure, mise en forme pour être montrée et exportée.
9. **Nettoyage** : supprimer le disque de test, oublier les identifiants, annuler l'override DRS.

### 5.2 Ce que montre la progression (exemples de messages)

```
[ 3%] Connexion à vcenter.lab.local … OK (empreinte confirmée)
[ 5%] Identification : VM vigie-01 sur esx43.lab.local
[ 8%] Chemin NFS : 192.168.148.24 → vmk0 → vSwitch0 → vmnic2 (actif)
[10%] Création du disque de test 4 Go sur shared-storage … thin (pas de VAAI-NAS)
[12%] Rescan SCSI … /dev/sdb détecté, numéro de série vérifié ✔
[12–40%] Préremplissage aléatoire : 1,6 / 4,0 Go — 412 Mo/s — reste 6 s
[42%] Ligne de base vmnic2 : 60 s (échantillon 2/3)
[45–70%] Écriture continue : 41 / 80 s — 398 Mo/s — p99 2,1 ms
[72%] Attente des échantillons PerformanceManager (dispo dans ~25 s)
[80%] vMotion vers esx44.lab.local : 63 % (tâche vCenter task-1234)
…
[100%] Terminé — verdict : ALERTE sur esx43 (ratio TX/écrit 2,9 ; débit ×12 plus lent qu'esx44)
```

La progression d'une tâche vCenter (création de disque, vMotion) se suit via `PropertyCollector`
sur `Task.info.progress`. Chaque étape du job a un poids fixe pour le pourcentage global.

### 5.3 API backend (indicative)

```
POST /api/login                     POST /api/logout
POST /api/vsphere/connect           GET  /api/vsphere/thumbprint?url=
GET  /api/self                      GET  /api/datastores
GET  /api/hosts?datastore=          GET  /api/path?host=&datastore=     (vmk/vSwitch/vmnic)
POST /api/testdisk                  DELETE /api/testdisk
POST /api/jobs   {profile, hosts[], cycles}
GET  /api/jobs/{id}                 GET  /api/jobs/{id}/events          (SSE)
POST /api/jobs/{id}/cancel
GET  /api/results?from=&to=&host=   GET  /api/schedule  PUT /api/schedule
GET  /api/health
GET  /api/reports/{runId}           rapport HTML autonome (téléchargement)
GET  /api/reports/compare?a=&b=     rapport comparatif avant/après
PUT  /api/branding                  nom client, site, intervenant, logo, commentaire
```

Un seul job à la fois (verrou global). L'annulation doit laisser un état propre : arrêt des
workers, pas de vMotion abandonnée à mi-chemin (on attend la fin de la tâche), disque conservé.

### 5.4 Rapport client

Objectif : un document qu'on peut poser devant un client et qui se lit sans l'auteur. Il est généré
à partir des données d'un run (ou de deux runs pour un avant/après) ; aucune mesure n'est refaite.

**Formats, sans aucun octet ajouté à l'appliance :**

- **HTML autonome** : un seul fichier, avec données, uPlot et CSS inline. Il s'ouvre hors ligne sur
  n'importe quel poste, les graphs restent interactifs (survol des valeurs), et il se partage par
  mail.
- **PDF** via l'impression du navigateur : feuille `@media print` en A4, sauts de page maîtrisés,
  graphs redimensionnés pour l'impression. Pas de moteur PDF embarqué.

**Structure (une section par page à l'impression) :**

1. **Synthèse** : client, site, datastore, date, intervenant et logo. Les **trois tuiles** débit /
   IOPS utiles / latence (§3.4). Un verdict en une ou deux phrases en français clair. Si une tournée
   a eu lieu, une ligne par hôte avec son statut.
2. **Débit** : graph du débit à la seconde en écriture et en lecture séquentielles, avec la moyenne
   et le minimum.
3. **IOPS et latence** : la courbe **IOPS (x) / latence p99 (y)**, une série par motif (lecture,
   écriture, mixte), avec le plafond de latence tracé en pointillé. On y voit d'un coup d'œil où
   le stockage sature. Suit un tableau des paliers : QD, IOPS, Mo/s, p50, p99, p99.9.
4. **Distribution des latences** : histogramme en échelle log, 4 Kio QD1, lecture et écriture.
   Une queue de distribution longue se voit ici, même quand la moyenne paraît correcte.
5. **Comparatif entre hôtes** (si tournée) : barres par hôte pour les trois chiffres titres, avec
   l'écart à la médiane.
6. **Santé du chemin** : ratio TX/écrit, erreurs NIC, événements de lien, avertissements NFS.
   Présentée en OK / à surveiller / anomalie, avec une phrase d'explication par ligne.
7. **Méthodologie et environnement** : versions vCenter et ESXi, modèle et vitesse de la vmnic,
   serveur et version NFS, taille de la zone de test, données incompressibles, `O_DIRECT`, durées et
   paliers, définition des unités, garde-fous déclenchés et limites (§9). C'est cette page qui rend
   les chiffres défendables.

**Rapport avant/après** : on choisit deux runs, et chaque chiffre est affiché avec son delta en %,
coloré selon qu'il s'améliore ou se dégrade. C'est exactement le cas d'esx43 avant et après la
réparation, et le format le plus parlant pour justifier une intervention.

**Rédaction** : les phrases sont produites par des **gabarits fixes** (pas de génération libre),
par exemple : « Le datastore soutient 48 000 IO/s en 4 Kio aléatoire avec une latence p99 inférieure
à 5 ms. » Les seuils de couleur sont ceux du §7. Les arrondis sont lisibles : 48 000 IO/s, 2 310 Mo/s,
0,42 ms ; deux décimales sous 10 ms.

**Unités** : Mo/s = 10⁶ octets/s (convention des constructeurs de stockage), Kio/Mio pour les
tailles de bloc. C'est indiqué explicitement dans la méthodologie pour éviter toute contestation.

**Personnalisation** (stockée dans `/data`) : nom du client, site, intervenant, logo (PNG ou SVG,
200 Ko maximum, SVG nettoyé de tout script), commentaire libre en tête de rapport.

## 6. Backend : détails qui comptent

### 6.1 Organisation du dépôt

```
vigie/
  cmd/vigie/            main : sous-commandes `serve`, `worker`, `version`
  internal/vsphere/       connexion, self-identify, inventaire, chemin réseau, tâches, perf, logs, events
  internal/testdisk/      création/suppression via ReconfigVM, rescan, identification, signature
  internal/bench/         moteur O_DIRECT, préremplissage, journal à la seconde, histogrammes
  internal/jobs/          machine à états, poids des étapes, SSE, annulation
  internal/analysis/      fenêtres d'échantillons, ratio corrigé, verdicts, seuils
  internal/store/         JSONL /data, rétention, écrivain asynchrone à timeout
  internal/web/           handlers, auth, TLS, embed du front
  internal/report/        rapport HTML autonome (gabarits, données + uPlot inline, CSS print)
  web/                    index.html, app.js, style.css, uplot.min.js (vendored, version figée)
  image/                  Dockerfile de build, scripts, template OVF, apkovl, APKBUILD
  test/                   tests d'intégration vcsim
  Makefile                build, test, image, ova, size-check
  CLAUDE.md               rappel des contraintes du §0
```

### 6.2 Moteur de bench (`vigie worker`)

- Ouvre le disque de test en `O_RDWR|O_DIRECT|O_SYNC`. O_SYNC est réglable : on garde un mode
  « write-back invité désactivé » par défaut pour ne mesurer que l'aller-retour vers le datastore.
- Tampons alignés sur 4 Kio, I/O de 1 Mio par défaut (réglable : 64 Kio, 256 Kio, 1 Mio),
  profondeur de file 4 (N goroutines avec `pwrite`/`pread` concurrents).
- Ne jamais toucher au bloc 0 (signature) : la zone de bench commence à 1 Mio.
- Mesure par I/O avec horloge monotone, histogramme HDR-like en mémoire, et débit agrégé à la
  seconde. Sortie en JSON sur stdout vers le processus parent, une ligne par seconde.
- `sched_setaffinity` sur le CPU 1 au démarrage du worker, s'il existe (CPU 1 à n−1 si la VM en a
  plus de 2).
- **Motifs** : séquentiel ou aléatoire. En aléatoire, les offsets sont uniformes et alignés sur la
  taille de bloc, tirés par un générateur rapide (xorshift) dont la graine est journalisée pour
  rejouer un run. Lecture, écriture ou mixte ; en mixte, le sens de chaque I/O est tiré au sort
  selon la proportion demandée.
- **Profondeur de file** = nombre de goroutines, chacune verrouillée sur son thread OS
  (`runtime.LockOSThread`) avec des I/O synchrones et un tampon aligné propre. io_uring est hors
  périmètre : le gain serait marginal face à la latence NFS, pour une complexité réelle.
- **Histogramme par palier** de 1 µs à 10 s (type HDR, précision 1 %), d'où p50/p95/p99/p99.9/max.
- **Usage CPU du worker** par palier, lu dans `/proc/self/stat`, pour le garde-fou du §3.4.
- **Sortie par palier** : IOPS, Mo/s, percentiles, CPU, nombre d'erreurs I/O, et la série à la
  seconde pour les graphs.

### 6.3 Sécurité du disque de test (critique)

Avant **toute** écriture, le worker refuse de démarrer si une seule de ces conditions échoue :

1. Le périphérique a été ajouté par l'appliance elle-même (sa clé et le chemin du VMDK sont connus
   via l'API).
2. Son numéro de série (`/dev/disk/by-id/…`, exposé grâce à `disk.EnableUUID`) correspond à
   l'**UUID du VMDK** renvoyé par vSphere.
3. Il n'est ni monté, ni utilisé par une partition, ni le disque de boot (comparer les numéros
   majeur/mineur avec ceux du média de boot).
4. Sa taille correspond à celle demandée.
5. Pour un réemploi : le bloc 0 porte la signature vigie avec ce même UUID.

Tests unitaires obligatoires sur chaque refus. Aucun paramètre de l'API ne permet de passer un
chemin de périphérique arbitraire.

### 6.4 Collecte vSphere

- Résoudre les compteurs **par nom** au démarrage (`PerformanceManager.perfCounter`), jamais par
  ID figé. Prévoir des alternatives : `net.bytesTx.average`, sinon `net.transmitted.average`.
- Compteurs : `net.bytesTx/Rx`, `net.errorsRx/Tx`, `net.droppedRx/Tx` (instances = vmnic) ;
  `datastore.totalWriteLatency`, `totalReadLatency`, `write`, `read` (instance = UUID du
  datastore) ; et côté VM `virtualDisk.totalWriteLatency` / `totalReadLatency`.
- Intervalle 20 (temps réel), `QueryPerf` groupé : une requête par hôte et par fenêtre.
- Logs : `BrowseDiagnosticLog(host, "vmkernel", start)` avec suivi de la dernière ligne lue, filtre
  `NFS:` et regex sur les avertissements.
- Événements : `QueryEvents` filtré par hôte et par type (lien vmnic down/up, perte de connectivité
  NFS) sur la fenêtre du job.

### 6.5 Privilèges vSphere minimaux (à valider en phase 2 sur le lab)

Rôle dédié `vigie` proposé, à affiner :

- `System.View`, `System.Read` (inventaire, PerformanceManager)
- `Global.Diagnostics` (BrowseDiagnosticLog)
- `Datastore.AllocateSpace`, `Datastore.Browse`, `Datastore.FileManagement`
- `VirtualMachine.Config.AddNewDisk`, `VirtualMachine.Config.RemoveDisk`
- `Resource.HotMigrate` (+ `Resource.AssignVMToPool` si on change de pool/cluster)
- Optionnel : `Host.Inventory.EditCluster` (override DRS de l'appliance seule)

L'UI affiche, après connexion, les privilèges effectifs (`AuthorizationManager.HasPrivilegeOnEntity`)
et grise les fonctions qui en manquent, plutôt que d'échouer en cours de job.

## 7. Seuils et verdicts (repris de la méthode manuelle)

| Indicateur | ATTENTION | ALERTE |
|---|---|---|
| Ratio TX/écrit corrigé | > 1,05 | > 1,10 |
| Écart de débit d'écriture entre hôtes (médiane) | > ×2 | > ×3 |
| Débit d'écriture absolu | < 50 Mo/s | < 16 Mo/s |
| Δ `net.errorsRx` / `droppedRx` sur la vmnic NFS | — | > 0 |
| Nouvelles lignes NFS d'avertissement | > 0 | > 5 |
| Flaps de lien (événements) | — | > 0 |
| Latence d'écriture datastore (hôte) p moyenne | > 10 ms | > 30 ms |
| *Test avancé* — latence 4 Kio QD1 p99, écriture | > 2 ms | > 10 ms |
| *Test avancé* — latence 4 Kio QD1 p99, lecture | > 2 ms | > 10 ms |
| *Test avancé* — écart IOPS appliance / hôte | > 10 % (mesure à vérifier) | — |
| *Test avancé* — CPU du worker | > 85 % (limité par l'appliance) | — |

Les seuils de latence du test avancé sont **indicatifs** : ils conviennent à un NFS all-flash en
10/25 GbE, où une latence sous la milliseconde en QD1 est attendue. Pour un NAS à disques
mécaniques, il faut les relever. L'UI propose des préréglages (« all-flash », « hybride »,
« disques ») et le préréglage utilisé est cité dans le rapport.

Tous ces seuils sont modifiables dans l'UI et stockés dans `/data`. Le verdict liste les indicateurs
qui l'ont déclenché. Rappel affiché dans l'UI : les erreurs physiques se comptent sur le
**récepteur**. Sans accès au switch, une vmnic qui *émet* mal n'apparaîtra que via le ratio TX/écrit.

## 8. Phases (chacune livrable, testable et commitée)

**Phase 1 — Image minimale bootable.** Build Docker reproductible, Alpine diskless, dropbear,
réseau via les propriétés OVF (transport ISO) ou DHCP, `/etc/issue` affichant l'URL. Binaire
« hello » servant `/api/health`. *Validation* : l'OVA se déploie sur le lab, boote, prend son IP,
SSH fonctionne, `vmw_pvscsi` et `vmxnet3` sont chargés. Taille mesurée et rapportée ; décision prise
sur open-vm-tools.

**Phase 2 — Connexion vSphere et inventaire.** Connexion avec empreinte TOFU, self-identify,
datastores, hôtes, chemin vmk → vmnic, privilèges effectifs. *Tests* : `vcsim` (simulateur
govmomi) en CI ; validation manuelle sur le lab.

**Phase 3 — Cycle de vie du disque de test.** Ajout et suppression via `ReconfigVM`, rescan SCSI,
contrôles du §6.3, signature. *Tests* : unitaires sur chaque refus de sécurité ; création et
suppression sur vcsim, puis sur le lab.

**Phase 4 — Moteur de bench.** Worker séparé, O_DIRECT, préremplissage aléatoire, écriture et
lecture au temps, journal à la seconde, histogrammes, affinité CPU. *Validation* : sur le lab,
débit comparable au `dd` historique (256 Mo en environ 3 à 4 s sur un hôte sain).

**Phase 5 — Collecte et analyse.** Compteurs par nom, fenêtres d'échantillons complets, décalage
d'horloge, ligne de base, ratio corrigé, seuils. *Tests* : unitaires sur des séries synthétiques,
notamment le recalcul en reproduisant le cas esx43 (188 Mo émis pour 64 Mo écrits, soit un ratio
d'environ 2,9).

**Phase 6 — Jobs et progression.** Machine à états, poids des étapes, SSE, annulation propre,
verrou global, suivi des tâches vCenter.

**Phase 7 — Interface web.** Auth, HTTPS, parcours §5.1, progression §5.2, graphs uPlot (direct +
historique), mode clair/sombre, aucune ressource externe (à vérifier avec une CSP stricte
`default-src 'self'`).

**Phase 8 — vMotion et tournée de comparaison.** Prévalidation, override DRS optionnel, alternance
A/B, verdict comparatif. *Validation* : esx43 contre esx44 sur le lab.

**Phase 8b — Test avancé (IOPS).** Motifs aléatoires et mixtes, paliers de QD, histogrammes par
palier, mesure CPU, contrôle croisé IOPS appliance/hôte, IOPS utiles sous plafond de latence.
*Tests* : unitaires sur l'alignement des offsets, les proportions du mixte et le calcul des IOPS
utiles. *Validation* : sur le lab, les IOPS hôte et appliance concordent à moins de 10 % sur
chaque palier.

**Phase 8c — Rapport client.** Vue rapport, export HTML autonome, CSS d'impression A4, rapport
avant/après, personnalisation, gabarits de phrases. *Validation* : le rapport HTML exporté s'ouvre
hors ligne sur un poste sans accès à l'appliance ; l'impression PDF tient sur 7 pages sans graph
coupé ; un rapport avant/après est produit à partir de deux runs du lab.

**Phase 9 — Surveillance continue.** Planificateur, rétention (30 jours par défaut), graphs
d'historique par hôte, export CSV/JSON.

**Phase 10 — Extras optionnels.** esxcli via API (`network nic stats get` pour les CRC et erreurs
de longueur), à condition que les privilèges le permettent ; `QueryNetworkHint` pour CDP/LLDP ;
module Arista eAPI en lecture (compteurs du port, MAC du vmk dans la table d'adresses, LLDP),
désactivé par défaut.
*(24/11/2026 : module Arista abandonné à la demande de l'utilisateur ; aucun accès aux switchs.)*

**Phase 11 — Durcissement et doc.** Dropbear désactivable depuis l'UI, rotation du certificat,
README de déploiement (OVF, rôle vSphere, premier accès), notes de version, taille finale.
*(24/11/2026 : réduite à la demande de l'utilisateur au README de déploiement, aux notes de version et à la
taille finale ; ni désactivation de SSH, ni rotation du certificat, ni page de rôle vSphere.)*

## 9. Limites assumées (à afficher dans l'UI, page « À propos »)

- Pas de compteur de retransmissions TCP du vmkernel : le ratio TX/écrit corrigé est un
  indicateur indirect, moins net et sensible au trafic des autres VMs sur la même vmnic.
- Comparaison séquentielle et non simultanée : l'alternance réduit, sans l'annuler, l'effet d'une
  charge serveur variable.
- Granularité de 20 s : les micro-coupures plus courtes sont lissées.
- IOPS du test avancé : elles incluent l'effet du cache du serveur NFS tant que la zone de test y
  tient. C'est une mesure de ce que voit une VM, pas une mesure des disques du serveur.
- Le côté émission de l'hôte (erreurs vues par le switch) reste invisible sans le module switch
  (phase 10).
- L'appliance teste le chemin de **l'hôte où elle tourne** : elle doit être déplacée (ou dupliquée)
  pour couvrir un autre hôte.

## 10. Consignes pour Claude Code

- Lire ce plan et créer `CLAUDE.md` en reprenant le §0 avant la phase 1.
- Une phase à la fois. Ne pas commencer la suivante avant la validation de la précédente.
- Versions figées partout (Alpine, Go, govmomi, uPlot), aucune dépendance non vendorée au runtime.
- Toute fonction qui écrit sur un périphérique bloc ou qui modifie vSphere passe par une fonction
  unique, testée et journalisée.
- Les tests vcsim tournent en CI ; les validations sur le lab sont listées en fin de phase sous
  forme de checklist pour l'humain.

## 11. Phase N — Diagnostic réseau (ajoutée le 18/11/2026, demande utilisateur)

L'appliance sert aussi au diagnostic réseau : on en déploie une ou plusieurs et on teste le réseau
entre elles et vers des cibles. L'onglet « Mesure » devient « Datastore performance » ; un onglet
« Network » s'ajoute.

- **Ping / latence** : une adresse IP (ou un nom) saisie dans un champ lance un test immédiat
  (RTT min/médiane/moyenne/max, gigue, perte). Option « surveiller » : la cible entre dans la
  surveillance continue.
- **Surveillance façon smokeping** : 20 pings par minute et par cible (espacés de 500 ms), RTT triés
  conservés pour la « fumée » ; consolidation de type RRD (1 min sur 48 h, 5 min sur 14 jours,
  1 h sur 1 an), persistée dans /data ; graph médiane colorée par la perte + bandes de dispersion.
  50 cibles au maximum.
- **Traceroute** : automatique si la cible n'est pas dans le sous-réseau de l'appliance, manuel
  sinon ; ICMP natif, 3 sondes par saut, 30 sauts au maximum, résolution inverse.
- **Table ARP** : affichage (IP, MAC, interface, état), suppression d'une entrée, vidage.
- **Débit iperf3 à la demande** vers une autre appliance (« pair » : URL + jeton d'API) :
  l'appliance demande au pair de démarrer son serveur iperf3 pour la durée du test, lance le client
  (TCP ou UDP, durée, flux parallèles, sens), affiche débit par seconde, retransmissions, gigue et
  perte en UDP. Un serveur iperf3 local peut aussi être ouvert à la main pour un client tiers.
- **Contraintes** : ping et traceroute en Go natif (aucun appel au shell, entrées validées) ;
  iperf3 lancé sans shell avec des arguments validés ; un test de débit est un job soumis au verrou
  global (il fausserait un bench de datastore et inversement) ; la surveillance ping tourne en
  continu ; aucune modification de l'infrastructure (la table ARP modifiée est celle de l'appliance).

## 12. Phase T — TUI console (ajoutée le 20/11/2026, demande utilisateur)

Interface texte sur la console VMware de l'appliance (tty1), **entièrement au clavier** (pas de
souris dans la console web VMware), lisible en **800×600** (on vise 80×25 au minimum, 100×37 si le
framebuffer est actif).

- **Onglet Network** : tous les tests réseau de l'onglet web (ping en direct, surveillance et graphs
  façon smokeping, traceroute, table ARP avec suppression, iperf3 vers un pair), graphs en mode texte.
- **Onglet Storage, tests locaux uniquement, sans vCenter** : l'utilisateur ajoute lui-même un disque
  à la VM (vSphere Client), puis confirme dans la TUI. L'appliance rescanne, et n'accepte le disque
  que s'il passe les contrôles du §6.3 adaptés au mode local : ce n'est pas le disque système, il
  est apparu après la demande, il est vierge (aucune table de partitions ni signature de système de
  fichiers), et l'utilisateur confirme en retapant son identifiant. Ensuite seulement, la signature
  vigie est écrite. Les tests standard (débit, sans ratio TX ni compteurs vSphere) et avancé (IOPS et
  latences) tournent alors en local.
- La règle « jamais d'écriture brute sur un périphérique bloc non vérifié » s'applique sans exception.
- **Écran « ajouter le disque » (demande du 20/11/2026)** : la TUI explique précisément les
  paramètres à saisir dans vSphere Client : taille conseillée (et minimum), contrôleur (le
  contrôleur **PVSCSI existant** de l'appliance, SCSI 0, n'importe quel numéro d'unité libre),
  emplacement (le datastore à mesurer), provisionnement (thick eager-zeroed conseillé, thin
  accepté et signalé), mode **dépendant** (pas indépendant non persistant), pas de partage
  multi-écrivain. Elle indique ensuite ce qu'elle a détecté (taille, contrôleur, unité,
  identifiant) avant de demander la confirmation.

## 13. Phase D — DCUI de l'appliance (ajoutée le 23/11/2026, demande utilisateur)

Console façon DCUI VMware sur tty1 : un écran d'accueil (nom, IP, URL HTTPS, mot de passe,
empreinte, SSH), Entrée/F2 pour entrer dans la TUI, F12 pour arrêter ou redémarrer, retour à
l'accueil après inactivité.

- **Root en console sans mot de passe** (accès console = accès physique) : shell root depuis la TUI,
  tty2 en connexion root automatique. **SSH demande toujours le mot de passe root.**
- **Configuration IP depuis la TUI** : DHCP ou statique (IP, masque, passerelle, DNS, nom d'hôte),
  appliquée à chaud et persistée dans /data ; elle prime au boot sur les propriétés OVF. Test du
  réseau (passerelle, DNS, résolution).
- **Mot de passe root depuis la TUI** (le même sert à l'interface web et à l'API) ; il prime au boot
  sur la propriété OVF ui_password ; la console continue de l'afficher.
- Ces opérations (réseau, mot de passe, alimentation) ne sont accessibles que par le socket de la
  console (root), jamais par HTTP(S).

## 14. Phase P — ISO live et qcow2 (demandée le 23/11/2026)

- **ISO live** (BIOS et UEFI) : tout en RAM, **aucune persistance, même si un disque est présent**
  (décision utilisateur du 23/11/2026 : « un liveCD c'est un liveCD, on perd tout »).
- **qcow2** pour Proxmox et OpenStack : le disque de l'OVA, pilotes virtio, mot de passe et nom
  d'hôte lus depuis les métadonnées (config-drive / user-data) s'ils sont fournis.
