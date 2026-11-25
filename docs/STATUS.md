# État d'avancement

Phase 1 **validée le 16/11/2026** (lab OVH, vCenter 8.0.3). Phase 2 **validée le 17/11/2026** (vcsim + lab). Phase 3 **validée le 17/11/2026** (tests, QEMU, lab). Phase 4 **validée le 17/11/2026** (tests, QEMU, lab). Phase 5 **validée le 17/11/2026** (tests, vcsim, lab).
Phase 6 **validée le 17/11/2026** (tests, lab). Phase 7 **validée le 18/11/2026** (tests, navigateur headless sur le lab).
Phase 8b **validée le 18/11/2026** (tests, lab, navigateur), réalisée avant la phase 8.
Phase N (diagnostic réseau) **validée le 18/11/2026** (tests, lab à deux appliances, navigateur).
Phase 8 **validée le 18/11/2026** (tests, vcsim, lab, navigateur).
Phase 8c **validée le 20/11/2026** (tests, Chromium hors ligne + impression PDF, lab, navigateur). 
Phase T (TUI console) **validée le 20/11/2026** (tests, QEMU console VGA, console VMware du lab).
Phase D (DCUI) **validée le 23/11/2026** (tests, QEMU, console VMware du lab).
Langues (console et web) **validées le 23/11/2026** (tests, QEMU, Chromium sur le lab).
Phase P (ISO live, qcow2) **validée le 24/11/2026** (tests, QEMU BIOS/UEFI/virtio, lab VMware BIOS/EFI). 
Phase 9 (surveillance continue) **validée le 24/11/2026** (tests, lab : tests planifiés, reconnexion, Chromium). 
Phase 10 (esxcli et CDP/LLDP, sans module switch) **validée le 24/11/2026** (tests, lab). 
Phase 11 (réduite : README, notes de version, taille finale) **terminée le 24/11/2026**. Toutes les
phases du plan sont réalisées.

## Phase 11 — Documentation et taille finale

- [x] Périmètre réduit à la demande de l'utilisateur : ni désactivation de SSH depuis l'UI, ni
  rotation du certificat (déjà généré au premier démarrage, propre à chaque appliance), ni page de
  rôle vSphere.
- [x] `README.md` : formats, déploiement de l'OVA et propriétés OVF, premier accès (console, mot de
  passe, empreinte, SSH), mesure d'un datastore, mode local, réseau, surveillance, API, ce que
  l'appliance modifie, limites, construction.
- [x] `docs/RELEASE-NOTES.md` : notes de la première version.
- [x] Taille finale : OVA 45,1 Mo, ISO 50,3 Mo, qcow2 45,3 Mo ; référence `size-baseline.json` mise à jour.


## Phase 10 — Extras (sans module switch)

- [x] Module Arista eAPI abandonné (demande du 24/11/2026) : l'appliance n'interroge aucun switch.
- [x] esxcli via l'API (`cli/esx` de govmomi, déjà présent : binaire +80 Ko) : `network nic stats get`
  sur les uplinks du chemin NFS, avant et après chaque test standard ; Δ par vmnic (CRC, trame,
  longueur, FIFO, manquées, débordements, abandons…). Liste blanche testée : `network nic stats get`,
  `network nic list`, `network nic get`, option `-n vmnicN` seulement.
- [x] Indicateurs : « Erreurs physiques en réception (CRC, trame, longueur) » (ALERTE au-delà de 0)
  et « Trames manquées par la carte » (ATTENTION au-delà de 1000) ; seuils modifiables ; INCONNU si
  esxcli est refusé. Les seuils enregistrés avant gardent les valeurs fournies pour les nouvelles clés.
- [x] CDP/LLDP (`QueryNetworkHint`) : voisin (switch, port, VLAN, MTU, adresse de management) par
  uplink dans le chemin NFS, le résultat et le rapport ; note si le MTU annoncé est inférieur à
  celui du vmk, ou si aucune annonce n'est reçue.
- [x] Lab : esxcli autorisé pour le compte de test ; Δ nuls sur vmnic0/vmnic1 pendant un test
  standard (écriture puis lecture séquentielles, bloc 1 Mio, QD 4, 60 s chacune, ligne de base 30 s,
  O_DIRECT+O_SYNC : 441 Mo/s en écriture). Aucune annonce CDP/LLDP sur le PCC (6 vmnics) : la
  lecture des voisins n'est couverte que par les tests unitaires.
- [ ] Humain : vérifier sur un vSwitch avec CDP ou LLDP en écoute que le switch et le port
  s'affichent ; identifier le privilège minimal pour esxcli (phase 11, rôle vSphere minimal).


## Phase 9 — Surveillance continue

- [x] Historique des mesures `/data/vigie/results.jsonl` (paquet `results`) : un point par test standard
  (par répétition), avancé, passage de tournée ou test local, avec son profil d'I/O ; alimenté à la fin de
  chaque job, reconstruit depuis les jobs conservés à la première mise à jour.
- [x] Planificateur (`GET/PUT /api/schedule`) : test standard (vCenter) ou local toutes les 15 à 1440 min
  sur l'hôte courant ; test sauté (et raison affichée) si un job tourne, sans vCenter ou sans disque.
  Jobs planifiés suffixés « s », 96 conservés à part des 200 jobs manuels.
- [x] Rétention des mesures réglable (30 jours par défaut), appliquée toutes les 6 h.
- [x] `GET /api/results?period=|from=&to=&host=&datastore=&format=csv|json` ; onglet Historique,
  carte « Tendances » (débit, latence p99, ratio TX/écrit) avec export CSV/JSON.
- [x] Seuils modifiables par préréglage (`GET/PUT /api/thresholds`, `thresholds.json`), appliqués aux
  tests manuels, planifiés et API ; retour aux valeurs fournies.
- [x] Mot de passe vCenter mémorisable (case à cocher), chiffré AES-256-GCM (clé `secret.key`, 0600) ;
  reconnexion automatique au démarrage et après perte de session ; « Se déconnecter » l'oublie.
- [x] Textes ajoutés dans les 6 langues ; parcours Chromium de et en sans nouveau texte manquant.
- [x] Lab : test planifié (461 Mo/s écriture, 1 413 Mo/s lecture, OK), export CSV, redémarrage du
  service → reconnexion vCenter et planning conservés. OVA 45,1 Mo.
- [ ] Humain : redémarrer la VM depuis une OVA à jour et vérifier la reconnexion ; laisser tourner
  la surveillance une journée et relire la carte Tendances.

Constat : avec 20 s d'écriture, le ratio TX/écrit n'a souvent pas d'échantillon complet (compteurs
vSphere à 20 s) ; la courbe du ratio n'affiche alors que les tests plus longs.


## Phase P — ISO live et qcow2 (§14 du plan)

- [x] `make release` : OVA (45,0 Mo), ISO hybride BIOS/UEFI (50,3 Mo), qcow2 (45,2 Mo), même média ;
  `docs/formats.md` (Proxmox, OpenStack, ISO).
- [x] ISO live : isolinux (BIOS) et GRUB autonome (UEFI) ; `vigie.live` → service `vigie-live` retire
  /data de fstab avant localmount : rien n'est conservé, même avec un disque vigie présent ;
  l'accueil l'annonce. UEFI : `simpledrm` reprend le framebuffer du firmware (console visible).
- [x] KVM : virtio_blk/scsi/net dans l'initramfs et le modloop ; plateforme détectée au boot
  (open-vm-tools retiré hors VMware, qemu-guest-agent seulement avec le canal virtio).
- [x] Métadonnées : config-drive (config-2), NoCloud (cidata), service 169.254.169.254
  (`vigie-cloud`) : nom d'hôte, mot de passe, clés SSH de root ; la console prime ; aucun script.
- [x] Disques hors VMware (wwid t10.…) : UUID dérivé stable, mode local de la phase T utilisable ;
  assistant adapté (VirtIO SCSI, cache=none).
- [x] ethtool, smartmontools, ipmitool (demande du 24/11/2026) : +3,8 Mo.
- [x] Tests : cloudinit (config-drive, NoCloud, clés invalides, priorité console), UUID dérivé.
- [x] QEMU (`image/qemu-formats.sh`, 18 contrôles) : ISO BIOS, ISO + disque vigie (rien de monté),
  ISO UEFI (OVMF), qcow2 virtio + NoCloud (nom, mot de passe, clé, plateforme kvm, guest agent,
  outils), qcow2 + config-drive.
- [x] Lab VMware : ISO démarrée sur deux VM jetables (BIOS et EFI), DHCP, API, accueil live
  visible ; VM et ISO supprimées ensuite.

Constats : l'API répond avant la bannière de la console série (script de test : attendre le mot de
passe) ; sans `simpledrm`, l'écran UEFI restait figé sur GRUB.

## Langues — console et interface web (demande du 23/11/2026)

- [x] Six langues : anglais (défaut), français, espagnol, italien, polonais, allemand. Un seul
  catalogue, `web/i18n.tsv` (842 textes sources × 6), partagé par la console (Go) et le navigateur
  (`web/i18n.js`, même moteur) : traduction au moment de l'affichage ; gabarits (« %d/%d reçus »)
  reconnus sur le texte affiché, du plus précis au plus général, morceaux variables traduits
  récursivement. Les messages du serveur (contrôles de sécurité, profils d'I/O, verdicts, étapes)
  passent par le même catalogue. Nombres et dates au format de la langue.
- [x] Console : Système > Langue, enregistrée dans /data (`PUT /api/system/language`, console seule).
  Polonais : police Lat2-Terminus16 (256 glyphes, couleurs vives conservées ; eurlatgr, à 512
  glyphes, ternissait les lettres accentuées) ; glyphes affichables lus dans la table unicode du
  noyau (GIO_UNIMAP), translittération sinon.
- [x] Web : sélecteur de langue dans le bandeau et sur l'écran de connexion (mémorisé par le
  navigateur), `alert`/`confirm` et libellés des graphs uPlot traduits sans toucher à app.js ;
  changer de langue recharge la page.
- [x] Tests : catalogue bien formé (mêmes variables partout, menus de la console ≤ 16 colonnes) ;
  parcours des écrans de la console dans les 5 langues étrangères sans texte français restant ;
  QEMU (polonais, allemand, espagnol) ; Chromium sur le lab (`image/lab-qa-i18n.sh`, `test/ui/i18n.mjs`) :
  aucun texte français restant en allemand, italien, anglais, aucune erreur JS en de, pl, es, it, en.
- Limite : le rapport client (HTML/PDF) reste en français.

## Phase D — DCUI de l'appliance (§13 du plan, demande du 23/11/2026)

- [x] Accueil façon DCUI sur tty1 (bandeau gris : version, nom, vCPU, mémoire, état vCenter ;
  bandeau jaune : URL HTTPS et origine de l'adresse, mot de passe, empreinte, SSH) ; Entrée/F2 :
  console vigie ; F12 : arrêt ou redémarrage ; Échap depuis le menu et 10 min d'inactivité : retour
  à l'accueil.
- [x] Root sans mot de passe en console : menu « Shell root » (bascule sur tty2) et tty2 en connexion
  root automatique (`getty -n -l` → `login -f root`). SSH (dropbear) demande toujours le mot de passe.
- [x] Onglet Système : accès et état, réseau (DHCP ou statique : IP, masque ou préfixe, passerelle,
  DNS, nom d'hôte ; validé avant envoi, confirmé, appliqué à chaud par ifdown/ifup), test du réseau
  (ping de la passerelle et des DNS, résolution d'un nom), mot de passe (saisie en clair contrôlée
  deux fois, 8 à 64 caractères ASCII), shell root, alimentation.
- [x] `internal/sysconf` : seules fonctions qui modifient l'appliance (ApplyNetwork, SetPassword,
  Power), validées, journalisées, testées. Configuration réseau persistée dans
  /data/vigie/network.conf et rejouée au boot (`vigie sysconf boot`, appelé par init.sh après les
  propriétés OVF) ; mot de passe changé en console marqué par password.local (la propriété OVF
  ui_password ne l'écrase plus). Les sessions web sont fermées au changement de mot de passe.
- [x] API `/api/system/network` (GET, PUT), `/api/system/password` (PUT), `/api/system/power` (POST) :
  socket console uniquement ; en HTTP(S), 403 même avec le mot de passe (testé).
- [x] nmap 7.99 et tcpdump 4.99.6 ajoutés à l'image (demande du 23/11/2026) : +4,6 Mo d'OVA (nmap,
  libpcap, lua, libssh2, libstdc++, libcrypto/libssl) ; budget apks relevé de 14 à 20 Mo.

QEMU (console VGA) : accueil ; tty2 root ; SSH avec l'ancien mot de passe, changement dans la TUI,
SSH et HTTPS avec le nouveau, ancien refusé ; passage en statique sans couper les services ; F12
redémarrer : statique, nom d'hôte et mot de passe conservés ; démarrage avec propriétés OVF (IP
statique, nom, mot de passe), puis DHCP, nom et mot de passe changés en console : après
redémarrage, la console l'emporte et le mot de passe OVF est refusé.

Lab (vigie-dev, 192.168.0.123) : accueil DCUI sur la console VMware, tty2 root, SSH (mauvais mot de
passe refusé), open-vm-tools démarré, nmap -sn et tcpdump fonctionnels ; aller-retour DHCP →
statique (même adresse, passerelle 192.168.0.254, DNS 172.16.0.10) → DHCP depuis la console, HTTPS
toujours joignable.

Constats :
- `/etc/init.d/networking restart` arrêtait aussi les services dépendant du réseau (dont le serveur
  vigie, au milieu de la requête, et dropbear) : remplacé par ifdown (ancienne configuration) puis
  ifup (nouvelle).
- La console est en QWERTY (clavier noyau par défaut) : le mot de passe est saisi en clair pour
  vérifier les caractères ; un choix de clavier (AZERTY) reste à faire si besoin.

## Phase T — TUI console (tty1, clavier, 80 × 25)

- [x] `vigie tui` sur tty1 (inittab, relancée si elle s'arrête) ; tty2 garde une connexion root
  (Alt+F2). Console texte VGA 80 × 25 (720 × 400 px, tient dans 800 × 600). Aucune dépendance :
  terminal brut, décodage des touches de la console Linux (F1-F5 en ESC [[A…) et de xterm, tampon
  d'écran avec envoi des seules différences, jeu de caractères limité à la police VGA (CP437 :
  « À », « Ê », flèches et tirets longs translittérés).
- [x] Bandeau permanent : version, nom, IP, **mot de passe**. Onglets F1 Network, F2 Storage, F3 Accès.
- [x] Network : ping en direct (graph en demi-blocs, perte, gigue ; traceroute automatique hors
  sous-réseau), traceroute, surveillance (liste, ajout, retrait, graph façon smokeping : fumée
  p10–p90, médiane colorée par la perte, périodes 3 h à 1 an), table ARP (suppression, vidage),
  iperf3 vers un pair ou un serveur quelconque (graph par intervalle, résultat et profil), pairs
  (ajout avec vérification de l'empreinte, serveur iperf3 local), interfaces.
- [x] Storage sans vCenter : assistant d'ajout de disque qui explique chaque paramètre à saisir
  dans le client vSphere (taille conseillée et minimale, emplacement, provisionnement, contrôleur
  SCSI 0 PVSCSI existant, mode dépendant, pas de partage), instantané des disques présents, rescan,
  contrôles, confirmation en retapant le nom, adoption ; état du disque, test standard et avancé
  locaux (graph en direct, résultat avec profil d'I/O complet), résultats, libération.
- [x] Sécurité du mode local (§6.3) : le disque doit être apparu après la demande, vierge (premier
  et dernier Mio à zéro, ou sa propre signature), d'au moins 2 Gio, avec un identifiant unique, et
  passer tous les contrôles de Verify ; la seule écriture est WriteSignature ; le worker revérifie
  avant chaque test ; la libération n'écrit rien. Tests : refus MBR, ext4, autre signature, trop
  petit, mauvaise confirmation, disque présent avant la demande.
- [x] Socket Unix `/run/vigie/console.sock` (0600, root) : la TUI passe par la même API que l'interface
  web, authentifiée « console » ; `GET /api/console` (mot de passe pour le bandeau) n'existe que là.
- [x] API : `GET /api/localdisk`, `POST|DELETE /api/localdisk/prepare`, `POST /api/localdisk/scan`,
  `POST /api/localdisk/adopt` {name, confirm, label}, `POST /api/localdisk/release` ; job `local`
  {write_s, read_s, advanced, preset}.
- [x] Tests Go : décodeur de touches, translittération, chaque écran en 80 × 25 sans glyphe hors
  police, assistant d'ajout joué au clavier contre le vrai serveur sur un sysfs simulé.
- [x] QEMU : console VGA réelle pilotée par `image/qemu-keys.sh` (sendkey) et capturée ; disque
  ajouté à chaud par le moniteur, détecté, adopté, test standard local mené au bout.
- [x] Lab : console VMware pilotée par `govc vm.keystrokes` et capturée par `govc vm.console`.

Lab (20/11/2026), vigie-dev sur 172.17.136.50, sans passer par vCenter côté appliance :
- Disque de 16 Go ajouté à chaud (thick eager-zeroed, ssd-001557, SCSI 0:1) pendant que l'assistant
  attendait ; détecté (naa.6000c29c771b…), 15 contrôles OK, adopté après confirmation.
- Test standard local — profil : écriture puis lecture séquentielles, bloc 1 Mio, QD 4, 80 s
  chacune, zone 16 Gio − 1 Mio, O_DIRECT+O_SYNC, données incompressibles renouvelées à chaque passe
  (préremplissage préalable) : 448 Mo/s en écriture (p99 12,7 ms), 1 108 Mo/s en lecture
  (p99 4,26 ms), OK. Cohérent avec le test via vCenter du même jour sur une zone de 4 Gio
  (452 / 1 092 Mo/s).
- Network depuis la console : ping de 172.16.0.10 (routé, 10/10, médiane 0,80 ms) avec traceroute
  automatique (100.64.0.1 → zfactory.zpod.lab) ; surveillance de la passerelle ; iperf3 TCP 1 flux
  10 s vers vigie-dev2 (hôte .53) : 10 480 Mbit/s émis, 63 retransmissions.
- Test avancé local, lancé depuis la console — profil : le test standard ci-dessus, puis 4 Kio
  aléatoire aligné en lecture, écriture et mixte 70/30, QD 1, 4, 16, 32, 5 s de chauffe et 40 s de
  mesure par palier, zone 16 Gio − 1 Mio, O_DIRECT+O_SYNC, données incompressibles : 435 / 1 109 Mo/s ;
  IOPS utiles (p99 < 5 ms) 53 140 en lecture (QD 16), 2 375 en écriture (QD 4), 7 544 en mixte
  (QD 4 ; max brut 10 349 à QD 32) ; QD1 p99 lecture 0,18 ms, écriture 3,41 ms → ATTENTION (seuil
  all-flash 2 ms), comme en mode vCenter (3,28 ms). CPU du worker ≤ 24 %.
- Disque libéré depuis la console (rien d'écrit), puis retiré de la VM dans vSphere (govc).

Constats :
- `/home` de vmforia saturé par un disque QEMU de 4 Gio prérempli (VM mise en pause) : disque de
  test QEMU en RAM, puis `/home` étendu à 153 Go (disque ajouté par l'utilisateur, LVM en ligne).
- Messages du noyau (ajout à chaud d'un disque) : niveau de la console abaissé par la TUI, et
  redessin complet chaque minute.

## Phase 8c — rapport client

- [x] Paquet `internal/report` : rapport HTML autonome (données JSON, uPlot 1.6.32, CSS et JS en
  ligne, CSP `default-src 'none'` dans le document : aucune requête réseau possible), une section par
  page A4 : synthèse (trois tuiles, verdict, hôtes de la tournée), débit, IOPS et latence (avancé ;
  courbe IOPS / p99 en SVG avec plafond), distribution des latences (log), comparatif entre hôtes
  (tournée), santé du chemin (OK / à surveiller / anomalie + explication), méthodologie (versions,
  chemin, protocole, garde-fous, unités, limites du §9)
- [x] Avant/après : chaque chiffre avec sa variation, verte ou rouge, stable sous ±5 % ; conditions
  différentes (hôte, datastore, profil, type de test) signalées, jamais bloquées
- [x] Phrases par gabarits fixes (`text.go`), arrondis du §5.4, Mo/s = 10⁶ octets/s
- [x] Personnalisation dans /data (client, site, intervenant, commentaire, logo PNG ou SVG ≤ 200 Kio ;
  SVG actif refusé : script, on*, foreignObject, référence externe, entité)
- [x] Tests : formats, gabarits, échappement, logos piégés, rapports standard / avancé / tournée /
  avant-après, routes web ; `vigie report job.json [job2.json]` hors appliance
- [x] QA `image/qa-report.sh` (Chromium, `--network none`) : ouverture file://, graphs tracés, aucune
  erreur ni violation CSP, PDF A4 = une page par section (hauteur mesurée), pas de débordement à 390 px
- [x] Lab (`image/lab-qa-report.sh`) et navigateur (`FLOW=ui image/lab-qa-ui.sh`)

API : `GET /api/reports/{id}` (`?download=1` : pièce jointe), `GET /api/reports/compare?a=&b=`
(l'avant est le plus ancien), `GET|PUT /api/branding`. 422 pour un job sans rapport (iperf, disque…).
Les jobs terminés portent un résumé (hôte, datastore, verdict, chiffres) affiché dans l'historique.

Lab (20/11/2026), vigie-dev sur 172.17.136.50, datastore NFS ssd-001557, disque de test 4 Gio thin :
- Test avancé — profil : ligne de base 60 s ; écriture puis lecture séquentielles, bloc 1 Mio, QD 4,
  80 s chacune ; puis 4 Kio aléatoire lecture / écriture / mixte 70/30, QD 1, 4, 16, 32, 5 s de
  chauffe + 40 s de mesure par palier ; zone 4 Gio − 1 Mio, O_DIRECT+O_SYNC, données incompressibles
  renouvelées. Résultat : 452 Mo/s en écriture, 1 092 Mo/s en lecture, 7 820 IO/s utiles
  (mixte 70/30, p99 < 5 ms, QD 4 ; maximum brut 11 000 à QD 32, p99 9,4 ms), QD1 p99 lecture
  0,23 ms / écriture 3,28 ms → ATTENTION (seuil all-flash 2 ms).
- Test standard — même profil séquentiel : 430 Mo/s / 1 179 Mo/s, ratio TX/écrit 1,030, OK.
- Rapports téléchargés par l'API (simple, avancé, avant/après) : 5 à 6 pages A4, 80 à 91 Ko,
  contrôles hors ligne et impression au vert. Rapports aussi produits à partir des runs archivés des
  17-18/11 (standard ×2, avancé, tournée).

Constats :
- Contrôle croisé IOPS appliance / vSphere : au plus 6,9 % sur les paliers mesurés (la marge de 3 s
  de la phase 8b est validée). Les paliers QD4 n'ont aucun échantillon de 20 s entièrement couvert :
  la fenêtre utile (37 s) et la période des paliers (45 s) les alignent mal à chaque fois. Le rapport
  l'affiche « — ». Piste : 43 s de mesure ou fenêtre recalée (à traiter avec la phase 9).
- `html/template` coûtait +5,7 Mo de binaire (réflexion → pas d'élagage des méthodes) : rendu écrit à
  la main avec échappement explicite, coût final +0,5 Mo de binaire, +0,15 Mo d'OVA (33,7 Mo).
- Heures du rapport en UTC (l'appliance n'embarque pas tzdata), indiqué dans la méthodologie.

## Phase 8 — vMotion et tournée de comparaison

- [x] Tests Go : comparaison (médianes par hôte, écart au meilleur, seuils ×2 / ×3, cas esx43 ×11,9
  en ALERTE, hôtes homogènes en OK), ratio incohérent (< 0,97) marqué peu fiable ; vcsim :
  prévalidation, vMotion de l'appliance (RelocateVM), override DRS manuel posé puis état restauré
- [x] Lab (`image/lab-qa-tour.sh`) et rendu dans l'interface (historique)

API : `GET /api/migrate/check?datastore=` (prévalidation par hôte + état DRS), jobs `move`
{target_host} et `tour` {hosts[], cycles, drs_override, return_home, baseline_s, write_s, read_s, preset}.
Seules opérations vSphere : `vsphere.MigrateSelf` (vMotion de l'appliance seule, tâche menée à son
terme même en cas d'annulation) et `vsphere.SetDRSOverride` (override de l'appliance seule, état
précédent toujours restauré).

Tournée sur le lab (18/11/2026), hôtes 172.17.136.50 et .51, datastore NFS ssd-001557, disque 4 Gio
thin, 2 cycles en alternance A/B/A/B, override DRS (cluster en « fully automated ») — profil de
chaque passage : ligne de base 40 s ; écriture puis lecture séquentielles, bloc 1 Mio, QD 4, 60 s
chacune sans chauffe, zone 4 Gio − 1 Mio, O_DIRECT+O_SYNC, données incompressibles renouvelées à
chaque passe. Durée 712 s, trois vMotion de 2 s.

| Hôte | Écriture (médiane) | Lecture (médiane) | p99 écriture | vs meilleur |
|---|---|---|---|---|
| 172.17.136.50 | 448 Mo/s | 1 196 Mo/s | 13,0 ms | ×1,03 |
| 172.17.136.51 | 463 Mo/s | 1 168 Mo/s | 12,7 ms | ×1,00 |

Verdict comparatif : hôtes homogènes (écart ×1,03). Après la tournée : appliance revenue sur .50,
aucun override DRS restant (vérifié par govc).

Constats :
- La prévalidation refusait à tort les hôtes .51 à .54 : le cluster OVH place le fichier d'échange
  des VM sur le stockage local de l'hôte ; corrigé (un datastore qui ne porte que le .vswp n'est pas
  requis).
- Avec 60 s d'écriture (2 échantillons), le ratio TX/écrit varie de 0,835 à 1,078 d'un passage à
  l'autre et déclenche des ATTENTION isolées : garder 80 s (défaut) pour la tournée. Un ratio sous
  0,97 est désormais marqué peu fiable (trafic de fond variable).


**Renommage dsprobe → Vigie (18/11/2026)** : module Go, binaire, paquet apk et services OpenRC,
`/data/vigie`, cookie `vigie_session`, en-tête `X-Vigie`, jetons `vg_…`, signature du disque de test
`VIGIE`, dossier de test `[ds] vigie-<vm>/`, labels de partitions, OVF et OVA `vigie-<ver>.ova`,
interface, console, scripts, documentation ; sur la machine de build, le dépôt synchronisé et le cache associé ;
VM de lab `vigie-dev` et `vigie-dev2` (les VM `dsprobe-*` ont été supprimées, sans reste sur le
datastore). Les appliances déployées avant le renommage ne sont pas compatibles (chemins et jetons).
Revalidé après renommage : 10 paquets de tests, QA réseau à deux appliances et QA navigateur.

Constat de la revalidation : DRS a déplacé de lui-même vigie-dev2 de l'hôte .51 vers .53 entre le
vMotion manuel et les tests. C'est exactement le risque que l'override DRS de la phase 8 doit couvrir.

## Phase N — diagnostic réseau (onglet Network, tout accessible par l'API)

- [x] Tests Go : statistiques (perte, médiane, p90, gigue), refus des cibles douteuses (injection),
  consolidation RRD 1 min → 5 min et capacité bornée, table ARP et passerelle, appartenance au
  sous-réseau, options iperf3 validées et ligne de commande ; ping et traceroute ICMP réels en root
- [x] Lab (`image/lab-qa-net.sh`, deux appliances vigie-dev et vigie-dev2) et navigateur

Résultats sur le lab (18/11/2026), appliances en 192.168.0.139 et 192.168.0.140, passerelle
192.168.0.254, MTU 1500 :

| Test (profil) | Résultat |
|---|---|
| Ping passerelle (20 échos ICMP 64 o, 200 ms) | 0 % de perte, médiane 0,38 ms, p90 0,45 ms, gigue 0,06 ms |
| Ping de l'autre appliance, même hôte (20 × 200 ms) | 0 %, médiane 0,20 ms |
| Ping de l'autre appliance, hôtes .50 et .51 (20 × 200 ms) | 0 %, médiane 0,41 ms, p90 0,44 ms |
| Ping 172.16.0.10, hors sous-réseau (10 × 200 ms) | 0 %, médiane 0,74 ms ; traceroute automatique : 192.168.0.254 → 100.64.0.1 → 172.16.0.10 (zfactory.zpod.lab), 3 sauts |
| Surveillance continue (20 pings/min) | passerelle et pair : 20/20 par salve, série de 4 points en résolution 1 min |
| ARP : suppression de l'entrée du pair puis ping | entrée retirée puis réapprise (REACHABLE) |
| Ajout de pair | jeton et empreinte vérifiés ; mauvaise empreinte refusée (400) ; jeton jamais renvoyé |

Débit iperf3 3.20, vigie-dev vers vigie-dev2 (le pair ouvre son serveur le temps du test puis
le referme), 10 s par test :

| Profil | Même hôte (.50) | Hôtes .50 → .51 |
|---|---|---|
| TCP, 1 flux, vigie-dev émet | 41,8 Gbit/s, 0 retransmission | 7,65 Gbit/s, 128 retransmissions |
| TCP, 4 flux, vigie-dev émet | 46,9 Gbit/s, 4 retransmissions | 19,3 Gbit/s, 250 retransmissions |
| TCP, 1 flux, le pair émet (-R) | 46,7 Gbit/s, 102 retransmissions | 14,1 Gbit/s, 0 retransmission |
| UDP, 1 flux, débit cible 500 Mbit/s / 2 Gbit/s | 500 Mbit/s, gigue 0,003 ms, 0 % | 2 000 Mbit/s, gigue 0,006 ms, 0,03 % |

Constats :
- Sur un même hôte, le trafic reste dans le vSwitch (40 à 47 Gbit/s) : pour tester le réseau
  physique, les deux appliances doivent être sur des hôtes différents.
- Entre .50 et .51, un flux unique passe à 7,65 Gbit/s dans un sens avec 128 retransmissions et à
  14,1 Gbit/s sans retransmission dans l'autre : asymétrie à creuser (uplink, file d'émission, LBT).
- Avant suppression, la table ARP de vigie-dev associait 192.168.0.140 et une dizaine d'autres
  adresses (192.168.x.252, .156, .188) à une même MAC da:20:2d:fe:bd:0d ; après suppression et
  nouveau ping, la vraie MAC de vigie-dev2 (00:50:56:ae:64:4a) a été apprise : un équipement
  semble répondre en proxy ARP sur ce segment.


## Phase 8b — test avancé

- [x] Tests Go : matrice (12 paliers en 4 Kio, 36 en « complet »), IOPS utiles sous plafond et
  maximum brut, indicateurs QD1 p99 lecture et écriture, écart appliance / vSphere, CPU du worker,
  préréglages
- [x] Lab (`image/lab-qa-advanced.sh`, jeton d'API) et rendu dans l'interface (historique)

Test avancé sur le lab (18/11/2026, hôte 172.17.136.50, datastore NFS ssd-001557, disque 4 Gio thin,
préréglage all-flash), 770 s :
- profil séquentiel : écriture puis lecture séquentielles, bloc 1 Mio, QD 4, 80 s chacune sans
  chauffe, zone 4 Gio − 1 Mio, O_DIRECT+O_SYNC, données incompressibles renouvelées à chaque passe ;
- profil aléatoire : uniforme aligné, bloc 4 Kio, lecture / écriture / mixte 70/30, QD 1, 4, 16 et
  32, 5 s de chauffe puis 40 s de mesure par palier, même zone, O_DIRECT+O_SYNC, données renouvelées
  à chaque écriture.

| Chiffre titre | Valeur |
|---|---|
| Débit séquentiel écriture / lecture | 469 / 1 244 Mo/s |
| IOPS utiles 4 Kio mixte 70/30 (p99 < 5 ms) | 7 620 à QD 4 (brut 11 192 à QD 16, p99 6,0 ms) |
| IOPS utiles lecture / écriture | 34 208 à QD 4 / 2 856 à QD 4 (bruts 48 467 / 3 517 à QD 16) |
| Latence 4 Kio QD1 lecture p50 / p99 | 0,11 / 0,19 ms |
| Latence 4 Kio QD1 écriture p50 / p99 | 0,59 / 3,38 ms → ATTENTION (seuil all-flash 2 ms) |

Contrôle croisé IOPS appliance / disque virtuel vu par vSphere (`virtualDisk.number*Averaged`,
instance scsi0:1) : 10 paliers sur 12 à moins de 8 % (la plupart à moins de 5 %), lecture QD4 à
+10 % et mixte QD4 à +11,6 %, tous deux juste avant le saut vers QD16. Hypothèse : l'échantillon de
20 s de l'hôte déborde sur la chauffe du palier suivant ; correctif (marge de 3 s en fin de palier)
appliqué, **à revalider sur le lab**. CPU du worker ≤ 22 % (pas de limitation par l'appliance).


## Phase 7 — checklist de validation

- [x] Tests Go : chaque fichier servi porte la CSP stricte (`default-src 'self'`, sans `unsafe`) et les
  en-têtes de sécurité ; aucun chargement externe dans les fichiers embarqués ; aucun script, style ni
  gestionnaire en ligne ; session par cookie (HttpOnly, Secure, SameSite=Strict), refus d'un POST par
  cookie sans en-tête `X-Vigie`, cookie invalide après déconnexion ; limitation après 5 échecs ;
  redirection HTTP → HTTPS sauf `/api/health` ; certificat persistant
- [x] Navigateur (Chromium headless, `image/lab-qa-ui.sh`, puppeteer-core 25.16.0) sur le lab :
  redirection HTTP, connexion (mauvais puis bon mot de passe), lecture et confirmation de l'empreinte
  vCenter, identification, datastores, chemin NFS, création du disque, test standard lancé et suivi
  en direct (barre, journal, mini-graph), verdict et tuiles, historique, réglages, à propos, thème
  sombre, mobile 390 px sans débordement ; aucune erreur JS, aucune violation CSP, aucune requête
  hors de l'appliance (seuls les 401 attendus : sonde avant connexion et mauvais mot de passe)
- [x] Console : URL HTTPS, empreinte SHA-256 du certificat et mot de passe (généré ou fourni par OVF)

Défauts trouvés par cette QA et corrigés : colonnes « Libre » et « Hôtes » dessinées en pastilles
(collision de la classe CSS `num`), bouton grisé sans explication, raison transitoire fausse avant le
chargement des datastores, liste de jobs `null` sur une appliance neuve (onglet Historique cassé),
débordement de l'empreinte et en-tête trop haut sur mobile, décimales anglaises dans les verdicts.

Test standard lancé depuis l'interface (18/11/2026, hôte 172.17.136.50, ssd-001557, disque 4 Gio
thin) — profil : écriture puis lecture séquentielles, bloc 1 Mio, QD 4, 80 s chacune sans chauffe,
zone 4 Gio − 1 Mio, O_DIRECT+O_SYNC, données incompressibles renouvelées à chaque passe, ligne de
base 60 s : verdict OK, écriture 462 Mo/s (p99 13,2 ms), lecture 1 215 Mo/s, ratio TX/écrit 1,037.

## Accès (phase 7)

- `https://<ip>/` (certificat auto-signé, empreinte sur la console) ; `http://<ip>/` redirige,
  sauf `/api/health`.
- Mot de passe affiché sur la console (choix utilisateur du 17/11/2026), qu'il soit généré au premier
  boot ou fourni par la propriété OVF. VM de dev : `dist/lab-password`, stable d'un déploiement à l'autre.
- Scripts : `Authorization: Bearer <mot de passe ou jeton vg_…>` en HTTPS (`-k` sur le lab).

## Phase 6 — checklist de validation

- [x] Tests Go : cycle de vie (verrou global, progression sans recul, journal, événements,
  persistance relue par un nouveau gestionnaire), annulation, échec, panique rattrapée, rejeu après
  la fin ; API : jetons (création, refus d'un jeton qui crée un jeton, révocation), validation des
  profils sans créer de job, flux SSE (content-type, progress, result, state, ids)
- [x] Lab (`image/lab-qa-jobs.sh`) : jeton d'API, testdisk.create, standard suivi en SSE avec second
  job refusé (409), rejeu SSE, bench annulé (disque conservé et prêt), standard × 3 avec médianes,
  historique, testdisk.delete

## API de la phase 6 (remplace les points d'entrée synchrones des phases 3 à 5)

```
POST /api/jobs                {profile, …}  -> 202 + job ; 409 si un job tourne (verrou global)
     profile "standard"        {repetitions 1|3, baseline_s, write_s, read_s, block_kib, qd, sync, prefill, preset}
     profile "bench"           {prefill, sync, seed, steps:[{kind, duration_s, warmup_s, block_kib, qd, read_pct, name}]}
     profile "testdisk.create" {datastore, size_gb}
     profile "testdisk.delete" {}
GET  /api/jobs[?profile=&limit=]   job courant + historique (200 derniers, persistés dans /data)
GET  /api/jobs/{id}                état, progression, étape, journal, résultat, auteur (ui ou token:<nom>)
GET  /api/jobs/{id}/events         SSE : state, progress, log, second, result ; rejeu (Last-Event-ID ou ?after=)
POST /api/jobs/{id}/cancel         arrêt du worker, disque conservé ; une tâche vCenter commencée est menée à son terme
GET  /api/tokens · POST /api/tokens {name} · DELETE /api/tokens/{id}   jetons d'API vg_… (créés avec le mot de passe uniquement)
```
Client shell : `image/vigie-api.sh` (fonctions `api` et `job_run`, suit le flux SSE).

## Constats de la phase 6 (lab, 17/11/2026, hôte 172.17.136.50, datastore NFS ssd-001557, disque 4 Gio thin)

Tout le scénario a été piloté avec un jeton d'API (`token:qa-claude` dans l'historique).

| Job | Profil d'I/O | Résultat |
|---|---|---|
| testdisk.create 4 Go | — | thin (pas de VAAI-NAS), contrôles OK, signature écrite |
| standard × 1 (60/80/80 s) | écriture puis lecture séquentielles, bloc 1 Mio, QD 4, 80 s chacune, zone 4 Gio − 1 Mio, O_DIRECT+O_SYNC, données incompressibles renouvelées à chaque passe | 230 s ; OK ; écriture 466 Mo/s (p99 12,9 ms), lecture 1 153 Mo/s, ratio TX/écrit 1,038 |
| second job pendant le standard | — | refusé, HTTP 409 (verrou global) ; `/api/health` montre le job en cours |
| bench annulé à 8 s | écriture séquentielle, bloc 1 Mio, QD 4, prévue 60 s | `cancelled` ; disque conservé, `ready` |
| standard × 3 (40/40/40 s) | mêmes motifs, 40 s chacun | 472 / 480 / 476 Mo/s en écriture ; ratios 1,056 / 1,077 / 1,097 sur **un seul** échantillon complet chacun → ATTENTION à tort |
| testdisk.delete | — | VMDK et dossier supprimés |

Correctif tiré de ce run : sous 2 échantillons de 20 s complets, le ratio TX/écrit est marqué non
concluant (INCONNU, « écriture trop courte ») au lieu de produire un verdict. Il faut au moins 60 s
d'écriture, 80 s recommandés (défaut). Couvert par un test unitaire ; pas encore rejoué sur le lab.
Le rejeu SSE d'un job terminé renvoie tout l'historique (519 événements pour un standard).


## Phase 5 — checklist de validation

- [x] Tests Go : fenêtres (échantillons entièrement contenus, bords jetés), octets au prorata, ratio
  corrigé sain (1,02), cas esx43 (188 Mo émis pour 64 Mo écrits → 2,94, ALERTE), ligne de base
  bruitée → « peu fiable » (pas d'ALERTE seule), absence d'échantillon → INCONNU, seuils et
  préréglages ; classification des événements, regex des avertissements NFS, somme des uplinks ;
  vcsim : résolution des compteurs par nom, horloges, QueryPerf
- [x] Lab : `image/lab-qa-standard.sh` exécute le test standard complet et produit un verdict

## API de la phase 5

```
POST /api/standard/run   {baseline_s, write_s, read_s, block_kib, qd, sync, prefill, preset}  (synchrone, ~4 min)
GET  /api/standard/last  dernier rapport (aussi dans /data/vigie/standard-last.json)
```

## Test standard sur le lab (17/11/2026)

Contexte : hôte 172.17.136.50 (ESXi 8.0.3), datastore NFS ssd-001557 (172.17.136.11), chemin vmk1
172.17.136.50 MTU 9000 → vDS → vmnic0 et vmnic1 actifs à 50 Gb/s ; disque de test 4 Gio thin ;
horloges hôte et vCenter à +27 ms et +25 ms de l'appliance. Durée totale 229 s.

| Phase (profil) | Résultat |
|---|---|
| Ligne de base au repos, 60 s, aucune I/O | trafic de fond TX 9,5 Mo/s sur les deux uplinks |
| Écriture séquentielle, bloc 1 Mio, QD 4, 80 s, sans chauffe, zone 4 Gio − 1 Mio, O_DIRECT+O_SYNC, données aléatoires incompressibles renouvelées à chaque passe | 466 Mo/s, p50 8,5 ms, p99 12,7 ms, CPU worker 17 % |
| Lecture séquentielle, bloc 1 Mio, QD 4, 80 s, sans chauffe, même zone, O_DIRECT+O_SYNC | 1 099 Mo/s, p50 4,1 ms, p99 4,3 ms |

| Indicateur | Valeur | Verdict |
|---|---|---|
| Ratio TX/écrit corrigé | 1,036 (3 échantillons complets, 60 s, 29 005 Mo émis, 570 Mo de fond, 27 438 Mo écrits) | OK |
| Ratio RX/lu (informatif) | 1,028, marqué peu fiable (trafic de fond RX instable) | — |
| Erreurs et pertes en réception sur vmnic0/vmnic1 | 0 | OK |
| Latence d'écriture datastore vue par l'hôte | 8,3 ms (lecture 3 ms) ; IOPS hôte 450 en écriture, 1 052 en lecture | OK |
| Changements d'état de lien | 0 | OK |
| Avertissements NFS dans vmkernel.log | non lisible : `BrowseDiagnosticLog` refusé au compte du lab | INCONNU |

Constats :
- Le trafic d'écriture sort par vmnic1 (478 Mo/s) et la lecture rentre par vmnic0 (1 129 Mo/s) : le
  détail par vmnic est indispensable, la somme des uplinks actifs donne le bon ratio.
- Les IOPS vues par l'hôte (450 en écriture) recoupent celles de l'appliance (466 Mo/s en blocs de
  1 Mio, soit 444 IOPS) à 1,3 % près.
- Le compte de test du Private Cloud OVH n'a pas le droit de lire les journaux d'hôte, bien que
  `HasPrivilegeOnEntity` annonce Global.Diagnostics : l'indicateur passe en INCONNU avec la cause,
  sans bloquer le verdict. À vérifier avec un compte disposant réellement du privilège.
- Un ratio de 1,036 sur un hôte sain (en-têtes TCP/IP et RPC NFS avec MTU 9000) est proche du seuil
  ATTENTION de 1,05 : à surveiller sur d'autres hôtes avant d'ajuster le seuil.


## Phase 4 — checklist de validation

- [x] Tests Go : histogramme (précision < 2 %, percentiles vs tri exact), réservoir incompressible et
  secteurs uniques, moteur sur fichier (prefill sans toucher le bloc 0, seq/rand/mix, annulation, zone
  trop petite), worker de bout en bout (événements start/checks/prefill/second/step/done, signature
  mise à jour, pas de second préremplissage) et refus (taille fausse, UUID inconnu)
- [x] QEMU : `POST /api/bench/run` sur le second disque PVSCSI (prefill 1 Gio + paliers), worker
  épinglé sur le CPU 1, aucune erreur d'I/O
- [x] Lab : `image/lab-qa-bench.sh` sur un datastore NFS 4 Go : préremplissage, écriture et lecture
  séquentielles 20 s, lecture aléatoire 4k QD16, écriture 4k QD1 ; débit cohérent avec le `dd`
  historique (256 Mo en 3 à 4 s sur un hôte sain, soit > 64 Mo/s) ; second run sans préremplissage

## API de la phase 4 (brute, remplacée par les jobs en phase 6)

```
POST /api/bench/run   {prefill, sync, seed, steps:[{kind, duration_s, warmup_s, block_kib, qd, read_pct, name}]}
                      kinds : seqwrite, seqread, randread, randwrite, randmix ; synchrone, un run à la fois
GET  /api/bench/last  dernier rapport (aussi persisté dans /data/vigie/bench-last.json)
```
Rapport : par palier, octets, ops, IOPS, Mo/s (10^6), latences p50/p95/p99/p99.9/max/moyenne, CPU
du worker, série à la seconde (octets, ops, p99), histogramme, graine.

## Constats de la phase 4

Mesures sur le lab OVH (17/11/2026, datastore NFS ssd-001557, hôte 172.17.136.50, 2 × 50 Gb/s,
MTU 9000, disque de test 4 Go thin) :

| Palier | Débit | IOPS | p50 | p99 | CPU worker |
|---|---|---|---|---|---|
| Préremplissage 1 Mio QD4 | 444 Mo/s | | | 15,3 ms | |
| Écriture séquentielle 1 Mio QD4, 20 s | 468 Mo/s | 447 | 8,26 ms | 13,4 ms | 19 % |
| Lecture séquentielle 1 Mio QD4, 20 s | 1 423 Mo/s | 1 357 | 3,05 ms | 4,03 ms | 6 % |
| Lecture aléatoire 4 Kio QD16, 10 s | 188 Mo/s | 45 981 | 0,14 ms | 5,24 ms | 24 % |
| Écriture aléatoire 4 Kio QD1, 10 s | 5 Mo/s | 1 336 | 0,61 ms | 3,18 ms | 3 % |

Correctifs du 17/11/2026 et nouvelle mesure (même hôte, même datastore, disque 4 Gio thin, zone
4 Gio − 1 Mio, données aléatoires incompressibles **renouvelées à chaque passe**) :

| Palier (profil) | O_DIRECT + O_SYNC | O_DIRECT sans O_SYNC |
|---|---|---|
| Écriture séquentielle, bloc 1 Mio, QD 4, 20 s | 475 Mo/s, p99 12,3 ms | 494 Mo/s, p99 12,1 ms |
| Lecture séquentielle, bloc 1 Mio, QD 4, 20 s | 1 405 Mo/s, p99 3,3 ms | 1 403 Mo/s, p99 4,2 ms |
| Lecture aléatoire uniforme, bloc 4 Kio, QD 16, 10 s après 3 s de chauffe | 48 745 IOPS, p99 5,6 ms | 49 301 IOPS, p99 5,2 ms |
| Écriture aléatoire uniforme, bloc 4 Kio, QD 1, 10 s après 3 s de chauffe | 1 370 IOPS, p50 0,60 ms, p99 3,2 ms | 1 334 IOPS, p50 0,63 ms, p99 3,3 ms |

Ni `O_SYNC` ni le renouvellement des données ne changent les résultats au-delà du bruit : sur un
disque virtuel PVSCSI, `O_DIRECT` suffit déjà à attendre l'acquittement de l'hôte, et le serveur NFS
OVH ne court-circuite pas les réécritures identiques. Les mesures précédentes n'étaient donc pas
optimistes. `O_SYNC` reste actif par défaut (§6.2) et le profil effectif accompagne chaque résultat.

Hôte sain : l'écriture dépasse largement le seuil du `dd` historique (> 64 Mo/s). La lecture
séquentielle et les IOPS en lecture bénéficient du cache du serveur NFS (zone de 4 Go) : c'est la
limite assumée du §9, la taille de zone sera paramétrable jusqu'à 64 Go. Aucune erreur d'I/O, worker
bien épinglé sur le CPU 1, préremplissage non refait au second run.


- Le worker (`vigie worker`) est un sous-processus épinglé sur les CPU 1..n-1 par
  `sched_setaffinity` ; il reçoit l'enregistrement du disque (UUID, taille, unité) et **refait tous
  les contrôles** du §6.3 avant d'ouvrir le périphérique : aucun chemin n'est transmis par le serveur.
- Chaque goroutine d'I/O est verrouillée sur son thread avec un tampon aligné 4 Kio ; `pwrite`/`pread`
  en O_DIRECT (O_SYNC en option), profondeur de file = nombre de goroutines.
- Le préremplissage écrit le réservoir aléatoire de 64 Mio XORé par numéro de secteur (4 Kio) : les
  secteurs sont tous distincts (anti-dédup) et incompressibles ; la signature mémorise les octets
  préremplis pour ne pas recommencer après un reboot.
- Histogramme type HDR : 64 sous-paniers par puissance de deux (précision environ 1,6 %), de 1 µs à
  18 min, fusionné depuis les histogrammes locaux des goroutines toutes les 200 ms.


## Phase 3 — checklist de validation

- [x] Tests Go : chaque refus du §6.3 (pas d'enregistrement, mauvais UUID, mauvaise unité SCSI, monté,
  partitionné, holder LVM/md, disque système, taille) bloque `WriteSignature` ; signature aller-retour
  et CRC ; vcsim : ajout, relecture, refus d'un enregistrement falsifié, suppression, idempotence,
  65 Go refusé
- [x] QEMU (`make qemu-test ARGS=--testdisk`) : un second disque PVSCSI au wwn connu est détecté par
  `vigie testdisk -uuid … -size … -sign`, signé, puis refusé avec une mauvaise taille ; le disque
  système est refusé
- [x] Lab (`image/lab-qa-testdisk.sh`) : création 1 Go sur un datastore NFS (eager zeroed puis thin),
  wwid = UUID du VMDK, contrôles OK, signature, réemploi (signature relue), doublon refusé,
  suppression avec destruction du VMDK

## API de la phase 3

```
GET    /api/testdisk              état : enregistrement, présence vSphere, périphérique invité, contrôles, signature, ready
POST   /api/testdisk              {datastore: "Datastore:…", size_gb: 1..64}  -> 201 état prêt, 409 créé mais contrôles KO
DELETE /api/testdisk              supprime le disque (refusé s'il est monté) et son VMDK
```
CLI : `vigie testdisk [-uuid U -size N [-sign]]` (état côté invité, sans vSphere).

## Constats de la phase 3

- Avec `disk.EnableUUID=TRUE`, l'UUID du VMDK (`backing.uuid`, ex. `6000C292-9ca7-…`) apparaît dans
  l'invité comme `wwid naa.6000c2929ca7…` (`/sys/block/sdX/device/wwid`) : c'est la clé de
  l'identification sûre du §6.3. L'unité SCSI annoncée par vSphere est recoupée avec le hctl.
- Le nom du périphérique (`sda`, `sdb`…) n'est pas stable d'un boot à l'autre (vu sous QEMU : le
  disque de test est sorti en `sda`). Rien ne doit jamais s'appuyer sur un nom : la résolution se fait
  par wwid, puis le nœud `/dev` est recoupé avec le majeur:mineur de sysfs (contrôle « nœud /dev
  cohérent »).
- Le VMDK est créé dans `[datastore] vigie-<vm>/testdisk.vmdk` (dossier créé par l'appliance et
  supprimé avec le disque s'il est vide, jamais de suppression récursive). Sur le NFS OVH sans VAAI,
  le thick eager zeroed est refusé et le repli thin est transparent.
- Deux fonctions seulement touchent à l'infrastructure : `testdisk.reconfigure` (ReconfigVM, ajout et
  suppression, journalisé) et `testdisk.WriteSignature` (seule écriture bloc, exige un `Verified`
  produit par `Verify` : aucun chemin de périphérique arbitraire n'est acceptable).


## Phase 2 — checklist de validation

- [x] Tests Go : unitaires (chemin réseau, UUID, store, mot de passe, auth HTTP) et vcsim (connexion
  avec empreinte, refus sans/mauvaise empreinte, identification UUID/MAC, datastore NFS, hôtes, chemin,
  privilèges) : `make test`
- [x] Taille : binaire 14,8 Mo (+8,1 Mo pour govmomi), OVA 32,8 Mo (+1,8 Mo), budget respecté
- [x] Lab : `image/lab-qa.sh` passe contre le vCenter OVH (empreinte, connexion, self, datastores NFS,
  hôtes, chemin vmk → vmnic, privilèges, déconnexion ; 401 sans jeton, 502 sur mauvaise empreinte)

## Constats de la phase 2

- Le certificat du vCenter OVH est signé par une autorité reconnue : govmomi ne consulte alors pas
  l'empreinte. `Connect` compare donc lui-même l'empreinte confirmée au certificat présenté, et
  l'API expose `trusted` pour que l'UI distingue « CA reconnue » de « auto-signé, à confirmer ».
- L'identification se fait par UUID SMBIOS en ordre d'octets inversé (`method: uuid-swapped`) sur
  ce lab ; le repli par MAC est testé sous vcsim.
- Les vmk NFS du lab sont sur un vDS : la politique de teaming du dvportgroup n'est pas lue (tous les
  uplinks du vDS sont listés, note dans la réponse). À affiner en phase 5 si le ratio TX/écrit par
  vmnic en a besoin.
- Authentification de l'API : `Authorization: Bearer <mot de passe initial>` (bcrypt dans
  `/data/vigie/ui.hash`, écrit par `vigie passwd` depuis `vigie-init`). Les sessions par cookie
  arrivent en phase 7.
- `image/deploy.sh` génère un mot de passe pour la VM de dev si `lab.env` n'en fixe pas
  (`dist/lab-password`, hors git) ; `image/lab-qa.sh` l'utilise.

## API de la phase 2 (toutes sous `Authorization: Bearer <mot de passe initial>`, sauf `/api/health`)

```
GET  /api/vsphere/thumbprint?url=   empreinte SHA-256 du certificat (à confirmer par l'utilisateur)
POST /api/vsphere/connect           {url, username, password, thumbprint}  -> statut
GET  /api/vsphere/status            connecté ?, serveur, utilisateur, empreinte
POST /api/vsphere/disconnect[?forget=1]
GET  /api/self                      « je suis la VM X sur l'hôte Y (cluster Z) », méthode uuid/uuid-swapped/mac
GET  /api/datastores[?host=]        datastores vus par l'hôte (NFS en premier : serveur, export, libre, hôtes)
GET  /api/hosts?datastore=          hôtes qui montent le datastore
GET  /api/path?datastore=[&host=]   serveur NFS -> vmk -> portgroup -> vSwitch/vDS -> uplinks (teaming)
GET  /api/privileges[?datastore=]   privilèges effectifs du §6.5 sur la VM, l'hôte, le cluster, le datastore
```
Les références d'objets sont au format `Type:valeur` (ex. `Datastore:datastore-12`).


## Phase 1 — checklist de validation (lab)

- [x] `make ova` produit `dist/vigie-<ver>.ova` (31,0 Mo), `make size-check` passe, tailles dans `docs/sizes.md`
- [x] Boot sous QEMU (`make qemu-test`) : DHCP, bannière avec IP sur la console, `/api/health`, SSH root avec le jeton
- [x] Build reproductible : deux `make ova` consécutifs donnent le même SHA-256 (horloge figée par
  libfaketime pour mkfs.vfat/mtools/syslinux, dates FAT du label et de ldlinux normalisées, graine de
  hachage ext4 fixe, CID du VMDK figé, `SOURCE_DATE_EPOCH` = date du commit)
- [x] `make deploy` (govc, vCenter 8.0.3 OVH) : import OVA direct, boot, IP DHCP 192.168.0.100 affichée sur la console VMware avec le jeton
- [x] `GET http://<ip>/api/health` renvoie `status: ok` et la version
- [x] `ssh root@<ip>` (dropbear) avec le jeton fonctionne ; clé d'hôte et mot de passe stables après reboot (testé sous QEMU)
- [x] `lsmod` : `vmxnet3`, `vmw_vmci`, `vsock` chargés ; disque système sur PVSCSI (pilote en dur) ; `/` en tmpfs, `/data` en ext4
- [x] vSphere affiche l'IP de la VM (open-vm-tools « running, unmanaged ») ; `govc vm.power -s` (arrêt invité via Tools) éteint la VM en 15 s
- [x] IP statique par propriétés OVF (`ovf-env.xml` sur le CD) : hostname, IP, passerelle, DNS et mot de passe appliqués (testé sous QEMU avec `--static`)

## Constats de la phase 1

- Alpine 3.24.4 (dernière stable ; v3.25 n'a pas encore de release). Noyau `linux-virt` 6.18.63-r0,
  pris dans le paquet apk de la branche (Alpine ne garde que la version courante : `LINUX_PKG_VERSION`
  dans `image/versions.mk` est la version attendue, un écart déclenche un avertissement au build).
- L'initramfs netboot « virt » d'Alpine ne contient ni `sd_mod`, ni pilote ATA/SCSI, ni FAT : il ne
  peut pas démarrer depuis un disque SATA. On génère le nôtre avec `mkinitfs` (fonctions base, scsi,
  ata, cdrom, squashfs + fonction locale `vigie` pour vfat/nls), dans le conteneur de build.
- modloop Alpine complet : 23 Mo ; élagué à 26 modules (liste `image/modloop-keep`) : 1,4 Mo.
  `modloop_verify=no` sur la ligne de commande noyau puisque l'image n'est plus signée par Alpine.
  Modules qu'il ne faut pas oublier : `af_packet` (sans lui udhcpc ne peut pas ouvrir de socket),
  `evdev` + `button` (acpid), `vmw_vmci` + `vsock` (open-vm-tools), `sr_mod` + `isofs` (ovf-env.xml).
- `vmw_pvscsi`, `ahci` et `ata_piix` sont compilés en dur dans `linux-virt` (d'où leur absence du
  modloop, qui m'avait d'abord induit en erreur) : disque système sur PVSCSI, CD-ROM sur SATA.
- Mode diskless confirmé : `/` en tmpfs, modloop monté en squashfs, `/data` en ext4 (label).
- VMDK : qemu-img écrit `version=1` et un CID aléatoire ; `make-ova.sh` patche `version=3` (comme ovftool)
  et un CID fixe. vCenter 8.0.3 a accepté l'import dans les deux cas.
- Build sans aucun privilège hôte : pas de loop device ; `mke2fs -d`, mtools, `syslinux -i` sur
  fichier. Seule l'étape image tourne en root *dans* le conteneur (installation du paquet noyau).
- Test de boot local : `make qemu-test` (QEMU TCG sur vmforia, sans KVM, environ 2 min),
  `image/qemu-cmd.sh` pour piloter la console série, `image/qemu-screen.sh` pour capturer l'écran.
- Sous QEMU, `open-vm-tools` est en état « crashed » (pas d'hyperviseur VMware) : normal, à vérifier
  sur le lab.
