<div align="center">

![new-api](/web/default/public/logo.png)

# New API Audit Fork

**面向 Token 用量审计的 New-API 最小二开版本**

<p align="center">
  <strong>简体中文</strong> |
  <a href="./README.en.md">English</a> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

</div>

## 项目定位

这个仓库是基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的审计专用 fork。

它不是一个重新实现的网关，也不改变 New-API 的核心业务逻辑。这个 fork 只增加一组很小的审计采集 hook，用来把 New-API 中已经解析到的请求信息和结算后的 token 用量，异步上报给独立的 [`token-audit`](https://github.com/Bigduang/new-api-audit-for-company) 审计服务。

目标是支持企业内部的 Token 审计需求：

- 按时间范围统计每个用户、每个 token 的 token 消耗。
- 识别请求类型，例如编码实现、调试修复、架构设计、配置运维、文档编写、代码审查、数据分析、疑似非工作、其他。
- 对疑似非工作或不确定请求，能够追溯到具体用户、token、模型、时间、token 用量和 prompt 预览。
- 保持 New-API 升级成本尽可能低，把分类、报表、复核、企业微信推送等逻辑放在独立审计服务中。

## 这个 fork 做了什么

当前二开集中在 3 个文件：

| 文件 | 作用 |
| --- | --- |
| `audit/sender.go` | 新增审计事件 sender，负责读取环境变量、HMAC 签名、非阻塞队列、异步 HTTP 上报 |
| `controller/relay.go` | 在请求解析后采集 request 事件，包括用户、token、模型、路径、格式、prompt hash、prompt preview 和完整 prompt |
| `model/log.go` | 在消费日志记录后采集 usage 事件，包括 prompt tokens、completion tokens、quota、channel、group、耗时、上游 request id |

New-API 会向审计服务发送两类事件：

```text
POST /internal/new-api/audit/request
POST /internal/new-api/audit/usage
```

请求带有以下签名头：

```text
X-Audit-Timestamp
X-Audit-Signature
```

签名算法：

```text
hex(hmac_sha256(timestamp + "." + raw_body, AUDIT_SECRET))
```

## 为什么要这样做

New-API 原有日志和数据库适合做用量统计，但不足以完成“工作用途审计”：

- `logs` 表能记录用户、token、模型、quota、token 数和 request_id，但不保存 prompt 内容。
- Docker logs 正则解析不稳定，遇到多节点、日志轮转、格式变化、流式请求时容易丢失或误判。
- 直接修改 New-API 主业务表会增加升级风险，也会把审计数据和网关业务强耦合。
- 仅靠 token 数无法判断请求是否真的用于工作，需要 prompt 证据、分类结果和人工复核链路。

所以这个 fork 采用“New-API 最小采集 + 独立审计服务处理”的设计：

- New-API 只负责在稳定位置上报 request / usage 事件。
- `request_id` 用于关联 prompt 和最终用量。
- 上报使用异步非阻塞队列，审计服务故障不阻断正常 API 请求。
- prompt 原文不写入 New-API 主库，由独立审计服务加密保存。
- 审计服务生产默认使用本机 SQLite 文件库，并按 30 天保留策略清理历史数据，不需要额外部署 MySQL。
- 分类、报表、复核和推送全部放在 `token-audit` 服务侧演进。

## 工作流程

```text
CPA / 客户端
    |
    v
patched New-API
    | 1. 请求解析后异步上报 request 事件
    | 2. 消费结算后异步上报 usage 事件
    v
token-audit 服务
    |
    | request_id 关联 prompt 与 token usage
    v
独立审计 SQLite 文件库
    |
    v
分类、报表、人工复核、企业微信推送
```

New-API 仍然按原来的方式完成鉴权、路由、转发、计费和日志记录。审计上报失败时只记录日志，不影响用户请求。

## 环境变量

这个 fork 新增以下 New-API 环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AUDIT_ENABLED` | `false` | 是否开启审计上报 |
| `AUDIT_ENDPOINT` | 空 | 审计服务地址，例如 `http://token-audit:8000` |
| `AUDIT_SECRET` | 空 | New-API 与审计服务共享的 HMAC 密钥 |
| `AUDIT_TIMEOUT_MS` | `800` | 单次上报请求超时时间，单位毫秒 |
| `AUDIT_QUEUE_SIZE` | `1000` | 异步上报队列长度 |
| `AUDIT_MAX_EVENT_BYTES` | `1048576` | 单个审计事件序列化后的最大字节数；超大 request 事件会省略完整 prompt，仅发送 hash/preview/长度，其他超限事件会被丢弃 |
| `AUDIT_EXCLUDED_TOKEN_NAMES` | 空 | 逗号分隔的 token 名称排除列表，用于排除审计服务自己的 LLM 分类请求 |

推荐配置：

```env
AUDIT_ENABLED=true
AUDIT_ENDPOINT=http://token-audit:8000
AUDIT_SECRET=replace-with-long-random-secret
AUDIT_TIMEOUT_MS=800
AUDIT_QUEUE_SIZE=1000
AUDIT_MAX_EVENT_BYTES=1048576
AUDIT_EXCLUDED_TOKEN_NAMES=audit-classifier
```

## 部署建议

生产环境推荐按以下顺序上线：

1. 先部署独立 `token-audit` 服务，审计库使用本机 SQLite 文件，例如 `/opt/token-audit/data/token_audit.db`。
2. 构建并部署这个 fork 的 New-API 镜像，但先设置 `AUDIT_ENABLED=false`。
3. 确认 CPA、New-API、上游模型调用全部正常。
4. 将 `AUDIT_ENABLED=true`，进入 shadow 上报阶段。
5. 对比 New-API `logs` 表与审计库中的请求数、token 数和 `request_id` 关联率。
6. 对账稳定后，再启用分类任务、日报/周报和企业微信推送。

Docker Compose 中 New-API 侧示例：

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

本地构建示例：

```bash
docker build -t new-api-audit:audit-hook .
```

## 验证

本 fork 已在本地执行过以下验证：

```bash
gofmt -w audit/sender.go controller/relay.go model/log.go
git diff --check
go test ./audit ./model ./controller -run '^$'
```

说明：

- `-run '^$'` 用于验证受影响包可编译，但不执行仓库既有测试用例。
- 当前上游 `controller` 的完整测试在本地 SQLite 初始化场景下存在既有失败，不属于审计 hook 编译错误。
- 上线前建议在 CI 或镜像构建环境里再次执行完整构建验证。

## 隐私与安全

审计数据可能包含敏感 prompt，因此请按内部合规要求部署：

- `AUDIT_ENDPOINT` 建议只暴露在 Docker 内网或内网服务网段，不开放公网。
- `AUDIT_SECRET` 必须使用高强度随机字符串，并与普通 New-API 配置隔离管理。
- 完整 prompt 不写入 New-API 主库；应由独立审计服务进行应用层加密存储。
- 报表默认只展示 prompt preview，完整 prompt 仅允许内部管理员复核时解密查看。
- 分类服务如果通过 New-API 调用模型，必须使用专用 token，并加入 `AUDIT_EXCLUDED_TOKEN_NAMES`，避免审计请求污染员工用量统计。

## 升级策略

这个 fork 的核心原则是让二开范围足够小，方便持续跟随上游 New-API：

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git fetch upstream --tags

git switch -c upgrade/upstream-vX.Y.Z
git merge --no-ff vX.Y.Z

gofmt -w audit/sender.go controller/relay.go model/log.go
go test ./audit ./model ./controller -run '^$'
docker build -t new-api-audit:audit-hook .
```

如果 rebase 冲突，优先检查以下位置：

- `controller/relay.go` 中请求解析后、敏感词检查和 token 估算附近。
- `model/log.go` 中 `RecordConsumeLog` 记录消费日志的位置。
- `common.RequestIdKey` 和 `common.UpstreamRequestIdKey` 是否仍然存在。

## 友情链接

- [LinuxDO](https://linux.do/)：高质量技术社区。

## 与上游 New-API 的关系

本仓库保留 New-API 的原始能力和许可证，仅增加企业内部审计所需的最小 hook。

当前代码基线为上游 `v1.0.0-rc.20`。New-API 可通过 `LOG_SQL_DSN` 单独使用 ClickHouse 保存网关日志；独立 `token-audit` 服务仍使用自己的 SQLite 审计库，两者职责不同。

原项目文档：

- [New-API 官方文档](https://docs.newapi.pro/zh/docs)
- [部署指南](https://docs.newapi.pro/zh/docs/installation)
- [环境变量](https://docs.newapi.pro/zh/docs/installation/config-maintenance/environment-variables)
- [API 文档](https://docs.newapi.pro/zh/docs/api)

> 使用本项目时仍需遵守上游模型服务条款、New-API 原项目许可证，以及所在司法辖区对生成式人工智能服务、日志留存、隐私保护和数据安全的要求。
