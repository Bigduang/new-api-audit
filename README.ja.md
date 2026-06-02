<div align="center">

![new-api](/web/default/public/logo.png)

# New API Audit Fork

**Token 利用監査向けの New-API 最小フォーク**

<p align="center">
  <a href="./README.md">简体中文</a> |
  <a href="./README.en.md">English</a> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <a href="./README.fr.md">Français</a> |
  <strong>日本語</strong>
</p>

</div>

## 目的

このリポジトリは [QuantumNous/new-api](https://github.com/QuantumNous/new-api) をベースにした監査専用 fork です。

新しいゲートウェイを作り直すものではなく、New-API の主要な業務ロジックも変更しません。この fork は小さな監査収集 hook だけを追加し、New-API が解析済みのリクエスト情報と精算後の token 使用量を、独立した `token-audit` サービスへ非同期で送信します。

目的は企業内の Token 監査要件を満たすことです：

- 期間を指定して、ユーザー別・token 別の token 消費を集計する。
- コーディング、デバッグ、アーキテクチャ設計、設定運用、文書作成、コードレビュー、データ分析、疑似非業務、その他などに分類する。
- 疑似非業務または不確実なリクエストを、ユーザー、token、モデル、時刻、token 使用量、prompt preview まで追跡できるようにする。
- 分類、レポート、レビュー、WeCom 通知を独立監査サービス側に置き、New-API のアップグレードコストを低く保つ。

## この fork の変更点

カスタム変更は 3 ファイルに限定されています：

| ファイル | 役割 |
| --- | --- |
| `audit/sender.go` | 監査イベント sender を追加。環境変数、HMAC 署名、非ブロッキングキュー、非同期 HTTP 送信を担当 |
| `controller/relay.go` | リクエスト解析後に request イベントを送信。ユーザー、token、モデル、パス、形式、prompt hash、prompt preview、完全 prompt を含む |
| `model/log.go` | 消費ログ記録後に usage イベントを送信。prompt tokens、completion tokens、quota、channel、group、処理時間、upstream request id を含む |

New-API は監査サービスへ 2 種類のイベントを送信します：

```text
POST /internal/new-api/audit/request
POST /internal/new-api/audit/usage
```

各リクエストには署名ヘッダーが付きます：

```text
X-Audit-Timestamp
X-Audit-Signature
```

署名アルゴリズム：

```text
hex(hmac_sha256(timestamp + "." + raw_body, AUDIT_SECRET))
```

## なぜこの設計か

New-API 既存のログとデータベースは利用量集計には十分ですが、「業務用途監査」には不足があります：

- `logs` テーブルにはユーザー、token、モデル、quota、token 数、`request_id` はありますが、prompt 本文は保存されません。
- Docker logs を正規表現で解析する方式は、複数ノード、ログローテーション、形式変更、ストリーミングリクエストで壊れやすいです。
- New-API の主要業務テーブルに監査データを書き込むと、アップグレードリスクが増え、監査とゲートウェイ業務が強く結合します。
- token 数だけではリクエストが本当に業務目的か判断できません。prompt の証跡、分類結果、手動レビューが必要です。

そのため、この fork は「New-API で最小限の収集 + 独立監査サービスで処理」という設計を採用します：

- New-API は安定した hook 位置で request / usage イベントだけを送信する。
- `request_id` で prompt と最終 token 使用量を関連付ける。
- 送信は非同期・非ブロッキングキューを使い、監査サービス障害時も通常 API リクエストを止めない。
- 完全 prompt は New-API 主データベースに書かず、独立監査サービスで暗号化保存する。
- 監査サービスは本番環境でもローカル SQLite ファイル DB をデフォルトで使用し、30 日保持ポリシーで履歴データを削除します。追加の MySQL サービスは不要です。
- 分類、レポート、レビュー、通知はすべて `token-audit` 側で進化させる。

## フロー

```text
CPA / Client
    |
    v
patched New-API
    | 1. 解析後に request イベントを非同期送信
    | 2. 精算後に usage イベントを非同期送信
    v
token-audit service
    |
    | request_id で prompt と token usage を関連付け
    v
独立監査 SQLite ファイル DB
    |
    v
分類、レポート、手動レビュー、WeCom 通知
```

New-API は従来どおり認証、ルーティング、転送、課金、ログ記録を行います。監査送信が失敗してもログに記録するだけで、ユーザーリクエストには影響しません。

## 環境変数

この fork は New-API に以下の環境変数を追加します：

| 変数 | デフォルト | 説明 |
| --- | --- | --- |
| `AUDIT_ENABLED` | `false` | 監査送信を有効化 |
| `AUDIT_ENDPOINT` | 空 | 監査サービス URL。例：`http://token-audit:8000` |
| `AUDIT_SECRET` | 空 | New-API と監査サービスで共有する HMAC secret |
| `AUDIT_TIMEOUT_MS` | `800` | 1 イベントあたりの送信 timeout、ミリ秒 |
| `AUDIT_QUEUE_SIZE` | `1000` | 非同期送信キューのサイズ |
| `AUDIT_MAX_EVENT_BYTES` | `1048576` | Maximum serialized audit event size in bytes; oversized request events omit full prompt text and send only hash/preview/length, while other oversized events are dropped |
| `AUDIT_EXCLUDED_TOKEN_NAMES` | 空 | 監査対象外 token 名のカンマ区切りリスト。分類器用 token の除外に使う |

推奨設定：

```env
AUDIT_ENABLED=true
AUDIT_ENDPOINT=http://token-audit:8000
AUDIT_SECRET=replace-with-long-random-secret
AUDIT_TIMEOUT_MS=800
AUDIT_QUEUE_SIZE=1000
AUDIT_MAX_EVENT_BYTES=1048576
AUDIT_EXCLUDED_TOKEN_NAMES=audit-classifier
```

## デプロイ

本番環境では次の順序を推奨します：

1. 先に独立した `token-audit` サービスをデプロイする。監査 DB はローカル SQLite ファイルで、例：`/opt/token-audit/data/token_audit.db`。
2. この fork の New-API イメージをビルドしてデプロイする。ただし最初は `AUDIT_ENABLED=false` にする。
3. CPA、New-API、上流モデル呼び出しがすべて正常であることを確認する。
4. `AUDIT_ENABLED=true` にして shadow reporting を開始する。
5. New-API `logs` と監査 DB のリクエスト数、token 数、`request_id` 関連付け率を比較する。
6. 照合が安定したら、分類ジョブ、日次/週次レポート、WeCom 通知を有効化する。

Docker Compose 例：

```yaml
services:
  new-api:
    image: your-registry/new-api-audit:audit-hook
    environment:
      AUDIT_ENABLED: "true"
      AUDIT_ENDPOINT: "http://token-audit:8000"
      AUDIT_SECRET: "${AUDIT_SECRET}"
      AUDIT_TIMEOUT_MS: "800"
      AUDIT_QUEUE_SIZE: "1000"
      AUDIT_MAX_EVENT_BYTES: "1048576"
      AUDIT_EXCLUDED_TOKEN_NAMES: "audit-classifier"
    depends_on:
      - token-audit
```

ローカルビルド：

```bash
docker build -t new-api-audit:audit-hook .
```

## 検証

この fork はローカルで以下を実行して検証済みです：

```bash
gofmt -w audit/sender.go controller/relay.go model/log.go
git diff --check
go test ./audit ./model ./controller -run '^$'
```

補足：

- `-run '^$'` は既存テストを実行せず、影響パッケージがコンパイルできることを確認します。
- 完全な `go test ./audit ./model ./controller` は、現在 `controller` の既存 SQLite 初期化失敗に当たります。これは監査 hook によるコンパイルエラーではありません。
- 本番前には CI またはイメージビルド環境で完全な検証を再実行してください。

## プライバシーとセキュリティ

監査データには機密 prompt が含まれる可能性があります。社内コンプライアンスに従ってデプロイしてください：

- `AUDIT_ENDPOINT` は Docker/internal network に限定し、公開しない。
- `AUDIT_SECRET` は高エントロピーのランダム文字列を使い、通常の New-API 設定とは分けて管理する。
- 完全 prompt は New-API 主 DB に保存せず、監査サービス側でアプリケーション層暗号化を行う。
- レポートではデフォルトで prompt preview のみ表示する。完全 prompt は内部管理者レビュー時だけ復号可能にする。
- 分類器が New-API 経由でモデルを呼ぶ場合は専用 token を使い、`AUDIT_EXCLUDED_TOKEN_NAMES` に追加する。

## アップグレード方針

この fork は変更範囲を小さく保ち、上流 New-API に追随しやすくしています：

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

rebase で衝突した場合は、優先して確認する場所：

- `controller/relay.go` のリクエスト解析後、センシティブワードチェック、token 推定付近。
- `model/log.go` の `RecordConsumeLog`。
- `common.RequestIdKey` と `common.UpstreamRequestIdKey` がまだ存在するか。

## 上流 New-API との関係

このリポジトリは New-API の元の機能とライセンスを保持し、社内監査に必要な最小 hook のみを追加しています。

元プロジェクトの参照：

- [New-API Documentation](https://docs.newapi.pro/en/docs)
- [Deployment Guide](https://docs.newapi.pro/en/docs/installation)
- [Environment Variables](https://docs.newapi.pro/en/docs/installation/config-maintenance/environment-variables)
- [API Documentation](https://docs.newapi.pro/en/docs/api)

> 本プロジェクトの利用者は、上流モデルサービスの規約、New-API 元プロジェクトのライセンス、生成 AI サービス、ログ保存、プライバシー、データセキュリティに関する適用要件を遵守する必要があります。
