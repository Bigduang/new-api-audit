<div align="center">

![new-api](/web/default/public/logo.png)

# New API Audit Fork

**A minimal New-API fork for token usage auditing**

<p align="center">
  <a href="./README.md">简体中文</a> |
  <strong>English</strong> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

</div>

## Purpose

This repository is an audit-focused fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api).

It is not a new gateway implementation and does not change New-API's core business logic. It only adds a small set of audit collection hooks that asynchronously report parsed request metadata and settled token usage to an independent `token-audit` service.

The goal is to support internal enterprise token auditing:

- Report token usage by user and token within a time range.
- Classify requests such as coding, debugging, architecture, operations, documentation, code review, data analysis, suspected non-work, and other.
- Trace every suspected non-work or uncertain request back to user, token, model, time, token usage, and prompt preview.
- Keep New-API upgrade cost low by leaving classification, reports, review workflow, and WeCom push in the independent audit service.

## What This Fork Changes

The custom changes are limited to three files:

| File | Purpose |
| --- | --- |
| `audit/sender.go` | Adds audit event sender: environment config, HMAC signing, non-blocking queue, async HTTP reporting |
| `controller/relay.go` | Sends request events after request parsing, including user, token, model, path, format, prompt hash, prompt preview, and full prompt |
| `model/log.go` | Sends usage events after consumption logging, including prompt tokens, completion tokens, quota, channel, group, duration, and upstream request id |

New-API reports two event types to the audit service:

```text
POST /internal/new-api/audit/request
POST /internal/new-api/audit/usage
```

Every request is signed with:

```text
X-Audit-Timestamp
X-Audit-Signature
```

Signature algorithm:

```text
hex(hmac_sha256(timestamp + "." + raw_body, AUDIT_SECRET))
```

## Why This Design

New-API's existing logs and database are good for usage accounting, but not enough for work-purpose auditing:

- The `logs` table records user, token, model, quota, token counts, and `request_id`, but not prompt content.
- Parsing Docker logs with regular expressions is fragile across multiple nodes, log rotation, format changes, and streaming requests.
- Writing audit data into New-API business tables increases upgrade risk and couples audit logic to gateway logic.
- Token counts alone cannot prove whether a request was used for work. Prompt evidence, classification results, and manual review are required.

This fork therefore uses a "minimal New-API collection + independent audit processing" approach:

- New-API only reports request and usage events at stable hook points.
- `request_id` links prompt data with final usage.
- Reporting uses an async non-blocking queue, so audit service failures do not block API requests.
- Full prompts are not written to the New-API main database; they are encrypted and stored by the audit service.
- Classification, reports, manual review, and push notifications evolve independently in `token-audit`.

## Flow

```text
CPA / Client
    |
    v
patched New-API
    | 1. report request event after parsing
    | 2. report usage event after settlement
    v
token-audit service
    |
    | request_id links prompt and token usage
    v
independent audit database
    |
    v
classification, reports, review, WeCom push
```

New-API still handles authentication, routing, forwarding, billing, and normal logging exactly as before. Audit reporting failures are logged but do not affect user requests.

## Environment Variables

This fork adds the following New-API environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `AUDIT_ENABLED` | `false` | Enable audit reporting |
| `AUDIT_ENDPOINT` | empty | Audit service base URL, for example `http://token-audit:8000` |
| `AUDIT_SECRET` | empty | Shared HMAC secret between New-API and audit service |
| `AUDIT_TIMEOUT_MS` | `800` | Per-event reporting timeout in milliseconds |
| `AUDIT_QUEUE_SIZE` | `10000` | Async reporting queue size |
| `AUDIT_EXCLUDED_TOKEN_NAMES` | empty | Comma-separated token names excluded from audit, used for the audit classifier token |

Recommended configuration:

```env
AUDIT_ENABLED=true
AUDIT_ENDPOINT=http://token-audit:8000
AUDIT_SECRET=replace-with-long-random-secret
AUDIT_TIMEOUT_MS=800
AUDIT_QUEUE_SIZE=10000
AUDIT_EXCLUDED_TOKEN_NAMES=audit-classifier
```

## Deployment

Recommended production rollout:

1. Deploy the independent `token-audit` service and audit database first.
2. Build and deploy this fork's New-API image with `AUDIT_ENABLED=false`.
3. Confirm CPA, New-API, and upstream model calls still work normally.
4. Set `AUDIT_ENABLED=true` and enter shadow reporting mode.
5. Compare New-API `logs` with the audit database by request count, token count, and `request_id` link rate.
6. After reconciliation is stable, enable classification jobs, daily/weekly reports, and WeCom push.

Docker Compose example:

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

Local build:

```bash
docker build -t new-api-audit:audit-hook .
```

## Verification

This fork was locally verified with:

```bash
gofmt -w audit/sender.go controller/relay.go model/log.go
git diff --check
go test ./audit ./model ./controller -run '^$'
```

Notes:

- `-run '^$'` verifies that affected packages compile without running existing test cases.
- A full local `go test ./audit ./model ./controller` currently hits an existing upstream SQLite initialization failure in `controller`; it is not caused by the audit hook.
- Run a full CI or image-build verification before production rollout.

## Privacy and Security

Audit data can contain sensitive prompts. Deploy it according to internal compliance requirements:

- Keep `AUDIT_ENDPOINT` on Docker/internal networks. Do not expose it publicly.
- Use a high-entropy `AUDIT_SECRET` and manage it separately from ordinary New-API config.
- Do not store full prompts in the New-API main database; let the audit service encrypt them at application level.
- Reports should show prompt previews by default. Full prompt text should only be decryptable for internal admin review.
- If the classifier calls models through New-API, use a dedicated token and include it in `AUDIT_EXCLUDED_TOKEN_NAMES`.

## Upgrade Strategy

The fork keeps the custom change surface small so it can follow upstream New-API:

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

If a rebase conflict occurs, check:

- The request parsing area in `controller/relay.go`, near sensitive-word checks and token estimation.
- `RecordConsumeLog` in `model/log.go`.
- Whether `common.RequestIdKey` and `common.UpstreamRequestIdKey` still exist.

## Upstream Relationship

This repository keeps New-API's original capabilities and license, and only adds the minimal hooks required for internal auditing.

Upstream project references:

- [New-API Documentation](https://docs.newapi.pro/en/docs)
- [Deployment Guide](https://docs.newapi.pro/en/docs/installation)
- [Environment Variables](https://docs.newapi.pro/en/docs/installation/config-maintenance/environment-variables)
- [API Documentation](https://docs.newapi.pro/en/docs/api)

> Users must still comply with upstream model terms, the original New-API license, and applicable requirements for generative AI services, log retention, privacy, and data security.
