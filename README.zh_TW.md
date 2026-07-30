<div align="center">

![new-api](/web/public/logo.png)

# New API Audit Fork

**面向 Token 用量稽核的 New-API 最小二開版本**

<p align="center">
  <a href="./README.zh_CN.md">简体中文</a> |
  <a href="./README.en.md">English</a> |
  <strong>繁體中文</strong> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

</div>

## 專案定位

這個倉庫是基於 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的稽核專用 fork。

它不是重新實作的閘道，也不改變 New-API 的核心業務邏輯。這個 fork 只增加一組很小的稽核採集 hook，用來把 New-API 中已經解析到的請求資訊和結算後的 token 用量，非同步上報給獨立的 [`token-audit`](https://github.com/Bigduang/new-api-audit-for-company) 稽核服務。

目標是支援企業內部的 Token 稽核需求：

- 按時間範圍統計每個使用者、每個 token 的 token 消耗。
- 識別請求類型，例如編碼實作、除錯修復、架構設計、設定運維、文件撰寫、程式碼審查、資料分析、疑似非工作、其他。
- 對疑似非工作或不確定請求，能追溯到具體使用者、token、模型、時間、token 用量和 prompt 預覽。
- 保持 New-API 升級成本盡可能低，把分類、報表、複核、企業微信推送等邏輯放在獨立稽核服務中。

## 這個 fork 做了什麼

目前二開集中在 3 個檔案：

| 檔案 | 作用 |
| --- | --- |
| `audit/sender.go` | 新增稽核事件 sender，負責讀取環境變數、HMAC 簽名、非阻塞佇列、非同步 HTTP 上報 |
| `controller/relay.go` | 在請求解析後採集 request 事件，包括使用者、token、模型、路徑、格式、prompt hash、prompt preview 和完整 prompt |
| `model/log.go` | 在消費日誌記錄後採集 usage 事件，包括 prompt tokens、completion tokens、quota、channel、group、耗時、上游 request id |

New-API 會向稽核服務發送兩類事件：

```text
POST /internal/new-api/audit/request
POST /internal/new-api/audit/usage
```

請求帶有以下簽名頭：

```text
X-Audit-Timestamp
X-Audit-Signature
```

簽名演算法：

```text
hex(hmac_sha256(timestamp + "." + raw_body, AUDIT_SECRET))
```

## 為什麼要這樣做

New-API 原有日誌和資料庫適合做用量統計，但不足以完成「工作用途稽核」：

- `logs` 表能記錄使用者、token、模型、quota、token 數和 request_id，但不保存 prompt 內容。
- Docker logs 正則解析不穩定，遇到多節點、日誌輪轉、格式變更、串流請求時容易遺失或誤判。
- 直接修改 New-API 主業務表會增加升級風險，也會把稽核資料和閘道業務強耦合。
- 僅靠 token 數無法判斷請求是否真的用於工作，需要 prompt 證據、分類結果和人工複核鏈路。

所以這個 fork 採用「New-API 最小採集 + 獨立稽核服務處理」的設計：

- New-API 只負責在穩定位置上報 request / usage 事件。
- `request_id` 用於關聯 prompt 和最終用量。
- 上報使用非同步非阻塞佇列，稽核服務故障不阻斷正常 API 請求。
- prompt 原文不寫入 New-API 主庫，由獨立稽核服務加密保存。
- 稽核服務生產預設使用本機 SQLite 檔案庫，並按 30 天保留策略清理歷史資料，不需要額外部署 MySQL。
- 分類、報表、複核和推送全部放在 `token-audit` 服務側演進。

## 工作流程

```text
CPA / 客戶端
    |
    v
patched New-API
    | 1. 請求解析後非同步上報 request 事件
    | 2. 消費結算後非同步上報 usage 事件
    v
token-audit 服務
    |
    | request_id 關聯 prompt 與 token usage
    v
獨立稽核 SQLite 檔案庫
    |
    v
分類、報表、人工複核、企業微信推送
```

New-API 仍然按原來的方式完成鑑權、路由、轉發、計費和日誌記錄。稽核上報失敗時只記錄日誌，不影響使用者請求。

## 環境變數

這個 fork 新增以下 New-API 環境變數：

| 變數 | 預設值 | 說明 |
| --- | --- | --- |
| `AUDIT_ENABLED` | `false` | 是否開啟稽核上報 |
| `AUDIT_ENDPOINT` | 空 | 稽核服務地址，例如 `http://token-audit:8000` |
| `AUDIT_SECRET` | 空 | New-API 與稽核服務共享的 HMAC 密鑰 |
| `AUDIT_TIMEOUT_MS` | `800` | 單次上報請求逾時時間，單位毫秒 |
| `AUDIT_QUEUE_SIZE` | `1000` | 非同步上報佇列長度 |
| `AUDIT_MAX_EVENT_BYTES` | `1048576` | 單個稽核事件序列化後的最大位元組數；超大 request 事件會省略完整 prompt，僅發送 hash/preview/長度，其他超限事件會被丟棄 |
| `AUDIT_EXCLUDED_TOKEN_NAMES` | 空 | 逗號分隔的 token 名稱排除列表，用於排除稽核服務自己的 LLM 分類請求 |

推薦設定：

```env
AUDIT_ENABLED=true
AUDIT_ENDPOINT=http://token-audit:8000
AUDIT_SECRET=replace-with-long-random-secret
AUDIT_TIMEOUT_MS=800
AUDIT_QUEUE_SIZE=1000
AUDIT_MAX_EVENT_BYTES=1048576
AUDIT_EXCLUDED_TOKEN_NAMES=audit-classifier
```

## 部署建議

生產環境推薦按以下順序上線：

1. 先部署獨立 `token-audit` 服務，稽核庫使用本機 SQLite 檔案，例如 `/opt/token-audit/data/token_audit.db`。
2. 構建並部署這個 fork 的 New-API 映像，但先設定 `AUDIT_ENABLED=false`。
3. 確認 CPA、New-API、上游模型呼叫全部正常。
4. 將 `AUDIT_ENABLED=true`，進入 shadow 上報階段。
5. 對比 New-API `logs` 表與稽核庫中的請求數、token 數和 `request_id` 關聯率。
6. 對帳穩定後，再啟用分類任務、日報/週報和企業微信推送。

Docker Compose 中 New-API 側示例：

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

本地構建示例：

```bash
docker build -t new-api-audit:audit-hook .
```

## 驗證

本 fork 已在本地執行過以下驗證：

```bash
gofmt -w audit/sender.go controller/relay.go model/log.go
git diff --check
go test ./audit ./model ./controller -run '^$'
```

說明：

- `-run '^$'` 用於驗證受影響套件可編譯，但不執行倉庫既有測試用例。
- 當前上游 `controller` 的完整測試在本地 SQLite 初始化場景下存在既有失敗，不屬於稽核 hook 編譯錯誤。
- 上線前建議在 CI 或映像構建環境裡再次執行完整構建驗證。

## 隱私與安全

稽核資料可能包含敏感 prompt，因此請按內部合規要求部署：

- `AUDIT_ENDPOINT` 建議只暴露在 Docker 內網或內網服務網段，不開放公網。
- `AUDIT_SECRET` 必須使用高強度隨機字串，並與普通 New-API 設定隔離管理。
- 完整 prompt 不寫入 New-API 主庫；應由獨立稽核服務進行應用層加密存儲。
- 報表預設只展示 prompt preview，完整 prompt 僅允許內部管理員複核時解密查看。
- 分類服務如果透過 New-API 呼叫模型，必須使用專用 token，並加入 `AUDIT_EXCLUDED_TOKEN_NAMES`，避免稽核請求污染員工用量統計。

## 升級策略

這個 fork 的核心原則是讓二開範圍足夠小，方便持續跟隨上游 New-API：

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git fetch upstream --tags

git switch -c upgrade/upstream-vX.Y.Z
git merge --no-ff vX.Y.Z

gofmt -w audit/sender.go controller/relay.go model/log.go
go test ./audit ./model ./controller -run '^$'
docker build -t new-api-audit:audit-hook .
```

如果 rebase 衝突，優先檢查以下位置：

- `controller/relay.go` 中請求解析後、敏感詞檢查和 token 估算附近。
- `model/log.go` 中 `RecordConsumeLog` 記錄消費日誌的位置。
- `common.RequestIdKey` 和 `common.UpstreamRequestIdKey` 是否仍然存在。

## 友情連結

- [LinuxDO](https://linux.do/)：高品質技術社群。

## 與上游 New-API 的關係

本倉庫保留 New-API 的原始能力和許可證，僅增加企業內部稽核所需的最小 hook。

目前程式碼基線為上游 `v1.0.0-rc.22`。New-API 可透過 `LOG_SQL_DSN` 單獨使用 ClickHouse 保存閘道日誌；獨立 `token-audit` 服務仍使用自己的 SQLite 稽核資料庫，兩者職責不同。

原專案文件：

- [New-API 官方文件](https://docs.newapi.pro/zh/docs)
- [部署指南](https://docs.newapi.pro/zh/docs/installation)
- [環境變數](https://docs.newapi.pro/zh/docs/installation/config-maintenance/environment-variables)
- [API 文件](https://docs.newapi.pro/zh/docs/api)

> 使用本專案時仍需遵守上游模型服務條款、New-API 原專案許可證，以及所在司法轄區對生成式人工智慧服務、日誌留存、隱私保護和資料安全的要求。
