# Sécurité

## Signaler une vulnérabilité

Merci de ne pas ouvrir d'issue publique : utilisez **Security → Report a vulnerability** sur la page
GitHub du dépôt (signalement privé). Indiquez la version, le format (OVA, qcow2, ISO) et les étapes
pour reproduire.

## Intégrité des releases

Chaque release est construite par GitHub Actions (`.github/workflows/release.yml`) depuis le tag
`vX.Y.Z`, après les tests, le démarrage des trois formats sous QEMU et le test de l'interface. Elle
contient :

- `SHA256SUMS` : empreintes SHA-256 de l'OVA, de l'ISO et du qcow2 ;
- `SHA256SUMS.sigstore.json` : signature Sigstore de `SHA256SUMS`, sans clé à gérer ; le certificat
  lie la signature au workflow de release de ce dépôt et au tag ; elle est inscrite dans le journal
  public Rekor ;
- une attestation de provenance (SLSA) par image, enregistrée par GitHub.

La procédure de vérification est dans le [README](README.md#verifying-a-release). Une image dont la
somme ou la signature ne se vérifie pas ne doit pas être déployée.

## Dans l'appliance

- Certificat HTTPS auto-signé généré au premier démarrage, propre à chaque appliance ; empreinte
  affichée sur la console.
- Mot de passe vCenter mémorisé (si demandé) chiffré en AES-256-GCM avec une clé propre à
  l'appliance.
- Interface : CSP stricte, aucune ressource externe, sessions par cookie et jetons d'API révocables.
- Lecture seule côté infrastructure, en dehors des opérations listées dans le README.
