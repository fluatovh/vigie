# Notes de version

## v1.1.0 — 26/11/2026

### Corrections
- **Session vCenter expirée** : après un redémarrage du vCenter, l'appliance gardait une session que
  le serveur ne reconnaissait plus et se croyait connectée ; chaque test planifié échouait
  (« The session is not authenticated »). La session est maintenant vérifiée toutes les 5 minutes et
  avant chaque test planifié, puis rétablie avec le mot de passe mémorisé.
- **Traductions de l'interface web** : environ 300 textes produits par le serveur (étapes et journal
  des jobs, détails des verdicts, erreurs, ping) restaient en français dans les autres langues. Ils
  sont au catalogue dans les 6 langues ; les nombres venus du serveur suivent le format de la langue.
- Moteur de traduction (console et web) : « %% » suivi d'une lettre n'est plus pris pour une variable
  (« at most 6,2 % over s step(s) ») ; à préfixe égal, le gabarit le plus précis l'emporte ; les
  gabarits imbriqués sur plus de trois niveaux sont traduits.
- Onglet Datastore performance : une déconnexion pendant la prévalidation des hôtes affichait
  « Cannot read properties of null (reading 'host') ».

### Documentation
- `docs/` : guide illustré de chaque fonction (mesure de datastore, surveillance et historique,
  réseau, réglages, console), captures prises sur un lab vSphere 8.
- Notes de conception (PLAN, STATUS) retirées du dépôt public.

### Tests
- Test Go : tout texte français du serveur doit figurer au catalogue (`TestServerTextsInCatalog`).
- Test d'interface de la CI : échec si un texte reste en français dans une autre langue.

## v1.0.1 — 25/11/2026

Première version publiée sur GitHub (le tag v1.0.0 n'a pas produit de release : la CI échouait sur
les runners GitHub).

- CI : Chromium démarre sur les runners GitHub (répertoire personnel dans /tmp).
- README en anglais.
- Contenu identique à v1.0.0 pour l'appliance, détaillé ci-dessous.

## v1.0.0 — 25/11/2026 (non publiée)

Première version complète de Vigie, appliance de diagnostic datastore et réseau pour vSphere.
Livrée en trois formats construits depuis le même média : OVA (45,1 Mo), qcow2 (≈ 45 Mo) et ISO live
BIOS/UEFI (≈ 50 Mo).

### Appliance
- Alpine Linux 3.24.4 en mode diskless (tout tourne en RAM), noyau `linux-virt` 6.18, binaire Go
  unique (Go 1.27.4, govmomi 0.56.2), graphiques uPlot 1.6.32 embarqués ; aucune dépendance réseau.
- Configuration par propriétés OVF (IP, DNS, NTP, nom d'hôte, mot de passe) ou par métadonnées cloud
  (NoCloud, config-drive, 169.254.169.254) ; DHCP par défaut.
- Certificat HTTPS auto-signé généré au premier démarrage, propre à chaque appliance ; empreinte
  affichée sur la console.
- Console façon DCUI : IP, mot de passe et empreinte affichés ; interface texte au clavier (80 × 25)
  avec tests réseau, tests de stockage sans vCenter, configuration réseau, mot de passe, langue,
  arrêt et redémarrage. SSH root par mot de passe.
- Interface et console en six langues : anglais (défaut), français, espagnol, italien, polonais,
  allemand.
- Outils inclus : open-vm-tools (ou qemu-guest-agent hors VMware), iperf3, nmap, tcpdump, ethtool,
  smartmontools, ipmitool.

### Mesure de datastore
- Connexion vSphere par l'API uniquement (vCenter ou ESXi), empreinte du certificat confirmée,
  contrôle des privilèges ; mot de passe mémorisable (chiffré) avec reconnexion automatique.
- Chemin NFS de l'hôte : vmk, vSwitch ou vDS, uplinks, voisins CDP/LLDP.
- Disque de test créé, vérifié (signature, identité, aucune écriture sur un disque non vérifié) et
  supprimé par l'appliance ; mode local pour adopter un disque ajouté à la main sans vCenter.
- Moteur de bench natif O_DIRECT (worker épinglé, données incompressibles renouvelées, histogrammes
  de latence) ; chaque résultat porte son profil d'I/O complet.
- Test standard : ligne de base, écriture puis lecture séquentielles, ratio TX/écrit corrigé,
  erreurs des vmnics (PerformanceManager et esxcli : CRC, trame, longueur, trames manquées),
  avertissements NFS de vmkernel.log, changements d'état de lien, latence vue par l'hôte ; verdict
  OK / ATTENTION / ALERTE selon un préréglage (all-flash, hybride, disques).
- Test avancé : IOPS et latences en 4 Kio aléatoire (lecture, écriture, mixte 70/30) à QD 1, 4, 16 et
  32, IOPS utiles sous un plafond de latence, contrôle croisé avec vSphere.
- vMotion de l'appliance et tournée de comparaison entre hôtes (alternance A/B/A/B, override DRS
  facultatif sur l'appliance seule).
- Rapport client HTML autonome, imprimable en A4, avant/après, logo et en-tête personnalisables.

### Surveillance
- Surveillance continue : test standard ou local planifié (15 min à 24 h), historique des mesures,
  carte Tendances, export CSV/JSON, rétention réglable (30 jours par défaut).
- Seuils des verdicts modifiables dans l'interface, conservés sur le disque de l'appliance.

### Réseau
- Ping et traceroute en direct, surveillance continue de cibles façon smokeping (historique d'un an),
  table ARP, iperf3 TCP/UDP entre deux appliances.

### API
- API JSON complète avec jetons (`Authorization: Bearer vg_…`), progression des jobs en Server-Sent
  Events, export des rapports et des mesures.

### Limites connues
- Ratio TX/écrit indirect, granularité de 20 s des compteurs vSphere (un test court peut le rendre
  non concluant).
- IOPS du test avancé influencées par le cache du serveur NFS ; certains paliers QD4 restent sans
  contrôle croisé vSphere (alignement sur les échantillons de 20 s).
- Erreurs d'émission de l'hôte invisibles (aucun switch n'est interrogé).
- Rapport client en français uniquement.
- ISO et qcow2 validés sous QEMU/KVM, pas encore sur un Proxmox ni un OpenStack réels.
