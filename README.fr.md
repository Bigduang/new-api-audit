<div align="center">

![new-api](/web/default/public/logo.png)

# New API Audit Fork

**Fork minimal de New-API pour l'audit d'utilisation des tokens**

<p align="center">
  <a href="./README.md">简体中文</a> |
  <a href="./README.en.md">English</a> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <strong>Français</strong> |
  <a href="./README.ja.md">日本語</a>
</p>

</div>

## Objectif

Ce dépôt est un fork orienté audit de [QuantumNous/new-api](https://github.com/QuantumNous/new-api).

Il ne réimplémente pas la passerelle et ne modifie pas la logique métier principale de New-API. Il ajoute uniquement quelques hooks de collecte pour envoyer, de façon asynchrone, les métadonnées de requête déjà analysées par New-API et l'utilisation de tokens après règlement vers un service indépendant `token-audit`.

L'objectif est de répondre aux besoins d'audit internes en entreprise :

- Statistiques de consommation par utilisateur et par token sur une période donnée.
- Classification des requêtes : développement, débogage, architecture, opérations, documentation, revue de code, analyse de données, usage potentiellement non professionnel, autre.
- Traçabilité des requêtes suspectes ou incertaines vers l'utilisateur, le token, le modèle, l'heure, les tokens consommés et un aperçu du prompt.
- Coût de mise à niveau New-API limité, en gardant classification, rapports, revue manuelle et notifications WeCom dans le service d'audit séparé.

## Ce que ce fork modifie

Les changements personnalisés sont limités à trois fichiers :

| Fichier | Rôle |
| --- | --- |
| `audit/sender.go` | Ajoute l'émetteur d'événements d'audit : configuration, signature HMAC, file non bloquante, envoi HTTP asynchrone |
| `controller/relay.go` | Envoie les événements request après l'analyse de la requête : utilisateur, token, modèle, chemin, format, hash du prompt, aperçu et prompt complet |
| `model/log.go` | Envoie les événements usage après le journal de consommation : prompt tokens, completion tokens, quota, channel, group, durée, upstream request id |

New-API envoie deux types d'événements au service d'audit :

```text
POST /internal/new-api/audit/request
POST /internal/new-api/audit/usage
```

Chaque requête contient les en-têtes de signature :

```text
X-Audit-Timestamp
X-Audit-Signature
```

Algorithme de signature :

```text
hex(hmac_sha256(timestamp + "." + raw_body, AUDIT_SECRET))
```

## Pourquoi cette approche

Les journaux et la base de données existants de New-API sont adaptés à la comptabilité d'usage, mais insuffisants pour auditer l'objectif professionnel des requêtes :

- La table `logs` contient l'utilisateur, le token, le modèle, le quota, les compteurs de tokens et `request_id`, mais pas le contenu du prompt.
- L'analyse de Docker logs par expressions régulières est fragile avec plusieurs noeuds, la rotation des logs, les changements de format et les requêtes en streaming.
- Écrire les données d'audit dans les tables métier de New-API augmente le risque de mise à niveau et couple l'audit à la passerelle.
- Les compteurs de tokens ne prouvent pas qu'une requête est liée au travail. Il faut des preuves de prompt, des résultats de classification et une revue manuelle.

Ce fork utilise donc le modèle "collecte minimale dans New-API + traitement indépendant dans token-audit" :

- New-API ne fait qu'envoyer les événements request et usage à des points stables.
- `request_id` relie le prompt à l'utilisation finale.
- L'envoi est asynchrone et non bloquant, afin qu'une panne du service d'audit ne bloque pas les requêtes API.
- Les prompts complets ne sont pas écrits dans la base principale New-API ; ils sont chiffrés par le service d'audit.
- Classification, rapports, revue et notifications évoluent dans `token-audit`.

## Flux

```text
CPA / Client
    |
    v
patched New-API
    | 1. envoi request après analyse
    | 2. envoi usage après règlement
    v
service token-audit
    |
    | request_id relie prompt et token usage
    v
base d'audit indépendante
    |
    v
classification, rapports, revue, push WeCom
```

New-API continue de gérer l'authentification, le routage, le forwarding, la facturation et les journaux habituels. Les échecs d'envoi d'audit sont seulement journalisés.

## Variables d'environnement

Ce fork ajoute les variables suivantes côté New-API :

| Variable | Défaut | Description |
| --- | --- | --- |
| `AUDIT_ENABLED` | `false` | Active l'envoi d'audit |
| `AUDIT_ENDPOINT` | vide | URL du service d'audit, par exemple `http://token-audit:8000` |
| `AUDIT_SECRET` | vide | Secret HMAC partagé entre New-API et le service d'audit |
| `AUDIT_TIMEOUT_MS` | `800` | Timeout par événement en millisecondes |
| `AUDIT_QUEUE_SIZE` | `10000` | Taille de la file asynchrone |
| `AUDIT_EXCLUDED_TOKEN_NAMES` | vide | Liste de noms de tokens exclus, séparés par des virgules, utilisée pour le token du classificateur |

Configuration recommandée :

```env
AUDIT_ENABLED=true
AUDIT_ENDPOINT=http://token-audit:8000
AUDIT_SECRET=replace-with-long-random-secret
AUDIT_TIMEOUT_MS=800
AUDIT_QUEUE_SIZE=10000
AUDIT_EXCLUDED_TOKEN_NAMES=audit-classifier
```

## Déploiement

Déploiement recommandé en production :

1. Déployer d'abord le service `token-audit` et la base d'audit.
2. Construire et déployer l'image New-API de ce fork avec `AUDIT_ENABLED=false`.
3. Vérifier que CPA, New-API et les appels aux modèles upstream fonctionnent normalement.
4. Passer `AUDIT_ENABLED=true` pour entrer en mode shadow reporting.
5. Comparer les `logs` New-API avec la base d'audit : nombre de requêtes, tokens et taux de liaison `request_id`.
6. Après réconciliation stable, activer classification, rapports quotidiens/hebdomadaires et push WeCom.

Exemple Docker Compose :

```yaml
services:
  new-api:
    image: your-registry/new-api-audit:audit-hook
    environment:
      AUDIT_ENABLED: "true"
      AUDIT_ENDPOINT: "http://token-audit:8000"
      AUDIT_SECRET: "${AUDIT_SECRET}"
      AUDIT_TIMEOUT_MS: "800"
      AUDIT_QUEUE_SIZE: "10000"
      AUDIT_EXCLUDED_TOKEN_NAMES: "audit-classifier"
    depends_on:
      - token-audit
```

Build local :

```bash
docker build -t new-api-audit:audit-hook .
```

## Vérification

Ce fork a été vérifié localement avec :

```bash
gofmt -w audit/sender.go controller/relay.go model/log.go
git diff --check
go test ./audit ./model ./controller -run '^$'
```

Notes :

- `-run '^$'` vérifie la compilation des paquets touchés sans exécuter les tests existants.
- Un `go test ./audit ./model ./controller` complet rencontre actuellement un échec existant d'initialisation SQLite dans `controller`, sans lien avec le hook d'audit.
- Avant la production, exécuter une vérification complète en CI ou dans l'environnement de build d'image.

## Confidentialité et sécurité

Les données d'audit peuvent contenir des prompts sensibles. Déployez-les selon vos règles internes :

- Garder `AUDIT_ENDPOINT` sur le réseau Docker/interne. Ne pas l'exposer publiquement.
- Utiliser un `AUDIT_SECRET` fort et le gérer séparément de la configuration ordinaire New-API.
- Ne pas stocker les prompts complets dans la base principale New-API ; les chiffrer côté service d'audit.
- Les rapports doivent afficher seulement les aperçus de prompts par défaut. Le texte complet doit être réservé à une revue administrateur interne.
- Si le classificateur appelle les modèles via New-API, utiliser un token dédié et l'ajouter à `AUDIT_EXCLUDED_TOKEN_NAMES`.

## Stratégie de mise à niveau

Le fork garde une surface de modification minimale pour suivre New-API upstream :

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git fetch upstream

git switch main
git merge upstream/main

git switch audit-hook
git rebase main

gofmt -w audit/sender.go controller/relay.go model/log.go
go test ./audit ./model ./controller -run '^$'
docker build -t new-api-audit:audit-hook .
```

En cas de conflit de rebase, vérifier :

- La zone d'analyse de requête dans `controller/relay.go`, près des contrôles de mots sensibles et de l'estimation des tokens.
- `RecordConsumeLog` dans `model/log.go`.
- L'existence de `common.RequestIdKey` et `common.UpstreamRequestIdKey`.

## Relation avec New-API upstream

Ce dépôt conserve les fonctionnalités et la licence originales de New-API, et ajoute seulement les hooks minimaux requis pour l'audit interne.

Références du projet original :

- [Documentation New-API](https://docs.newapi.pro/en/docs)
- [Guide de déploiement](https://docs.newapi.pro/en/docs/installation)
- [Variables d'environnement](https://docs.newapi.pro/en/docs/installation/config-maintenance/environment-variables)
- [Documentation API](https://docs.newapi.pro/en/docs/api)

> Les utilisateurs doivent toujours respecter les conditions des fournisseurs de modèles, la licence New-API originale et les exigences applicables en matière de services d'IA générative, conservation des journaux, confidentialité et sécurité des données.
