# Langfuse 精通路线：在 go-eino Demo 中逐步接入

Nov 6, 2026 · @Tim

## 0. 先看清现状：你要学的是 Langfuse v4

结论：直接学 v4，Go 侧用 eino-ext 的 `callbacks/langfuse/v2` 做追踪，用自己封装的 REST 客户端做 Prompt、数据集、评分。下面三件事决定了整条路线怎么走。

**1）v4 是一次数据模型大改。** v4.0.0 于 2026-07-29 发布，Langfuse Cloud 在 2026-11-16 变为只支持 v4，自部署 v3 的安全补丁到 2027 年 1 月为止（[迁移实测](https://jacar.es/?p=7396)，[v4 公告](https://langfuse.com/changelog/2026-08-17-langfuse-v4.md)）。v4 的主表从 trace 变成了 observation：每次 LLM 调用、工具调用、Agent 步骤都是一行（[来源](https://langfuse.com/changelog/2026-03-10-simplify-for-scale)）。网上大量教程还是 v2/v3 时代的，看到 `/api/public/ingestion`、“trace input/output” 的写法要自动打个问号。

**2）先确认你 Docker 里跑的是哪个版本。** 打开 Langfuse 网页左下角或看 `docker compose images`。

- 如果是 v3：这是学习环境、没有实际数据要保留，最省事的是拉最新的 [docker-compose](https://langfuse.com/self-hosting) 重新部一套干净的 v4。不要在旧卷上直接 `docker compose up --pull always`，v4 需要先把 ClickHouse 升到 26.4，硬升会出问题。
- 如果是 v4：直接开始。
- 企业里以后一定会碰到“生产环境从 v3 升 v4”，这个放到阶段 10 专门练。

**3）Go 没有官方 SDK，但不影响你全功能使用。** 官方 SDK 只有 Python 和 JS/TS；Langfuse 本身是一个 OpenTelemetry 后端，任何有 OTel SDK 的语言都能把 trace 发到 `/api/public/otel`（[来源](https://langfuse.com/resources/engineering/opentelemetry-languages.md)）。注意只支持 OTLP over HTTP（protobuf 或 JSON），不支持 gRPC。所以你的 Go 工具箱是三层：

| 层 | 用什么 | 覆盖的功能 |
| --- | --- | --- |
| 自动追踪 | [eino-ext callbacks/langfuse/v2](https://pkg.go.dev/github.com/cloudwego/eino-ext/callbacks/langfuse/v2)（v2.0.2，2026-09 发布） | ChatModel→generation、Agent/Tool/Retriever/Embedding 类型、token、流式、user/session/tags/release 传播、脱敏、采样 |
| 手动补充 | 原生 OpenTelemetry Go + `langfuse.*` 属性 | 自定义 span、Prompt 关联、可过滤 metadata、实验属性 |
| 管理功能 | 自己封装的 Go REST 客户端（可用 [OpenAPI 规范](https://cloud.langfuse.com/generated/api/openapi.yml) 生成） | Prompt、数据集、实验、评分、标注队列、指标查询 |

eino 的这个回调包在 README 里明确说了：Prompt、数据集、评分等管理 API 不在它的范围内。这正好是你的练手点：自己写一个 `lfclient` 包，以后直接搬进 Workflow Base。

另外：网上不少文章写的是 v1 回调（`NewLangfuseHandler` / `SetTrace`），它走的是旧的 ingestion 接口，不要用。

## 1. 全功能地图：要掌握的 4 大块、约 30 个功能点

Langfuse 的功能可以归成一个闭环：追踪 → 监控 → 建数据集 → 实验 → 评估，再回到追踪（[官方概念页](https://langfuse.com/docs/evaluation/core-concepts.md)）。下表是你的“通关清单”，最后一列对应第 3 节的阶段编号。

| 模块 | 功能点 | Go 里怎么用 | 阶段 |
| --- | --- | --- | --- |
| 可观测 | Trace / Observation / Session 数据模型 | eino 回调自动生成 | 1 |
| 可观测 | Observation 类型：span、generation、event、agent、tool、chain、retriever、embedding、guardrail、evaluator | eino 自动映射 + `langfuse.observation.type` | 2 |
| 可观测 | User、Session、Tags、Metadata、Release、Version、Environment | `StartTrace` 的 With\* 选项 | 2 |
| 可观测 | 日志级别与错误状态、中断/取消 | 回调自动处理 + 手动 `langfuse.observation.level` | 2 |
| 可观测 | Token 用量与成本、自定义模型价格 | 回调采集 usage；在 UI 里给通义/DeepSeek 等配价格 | 3 |
| 可观测 | 流式输出、首 token 时间 | 回调自动采集 completion start time | 3 |
| 可观测 | 脱敏、采样、大小限制、批量与优雅关闭 | `Config.MaskFunc` / `SampleRate` / `Shutdown` | 9 |
| 可观测 | 跨服务分布式追踪（Java + Go） | W3C `traceparent` 传播 | 9 |
| 监控分析 | Observations 表、过滤、全文搜索、Saved Views、Pulse | UI，依赖你前面打的属性是否规范 | 3 |
| 监控分析 | 自定义 Dashboard、Metrics API v2 | UI + Go 调用 Metrics API | 8 |
| 监控分析 | Monitors 告警（成本/质量/延迟阈值 → Webhook） | UI 配置 + Go 写一个 Webhook 接收端 | 8 |
| Prompt 管理 | 版本、Label（production / staging）、text 与 chat 两种类型 | REST `GET/POST /api/public/v2/prompts` | 4 |
| Prompt 管理 | 变量、Prompt 引用、Message Placeholder、config 字段 | Go 里自己实现 compile | 4 |
| Prompt 管理 | 缓存与兜底、把 Prompt 关联到 generation | 本地 TTL 缓存 + `langfuse.observation.prompt.name/version` | 4 |
| Prompt 管理 | Playground、LLM Connections | UI | 4 |
| 评估 | Scores（数值/分类/布尔）、Score Config | REST 写分，读用 Scores API v3 | 5 |
| 评估 | 用户反馈（点赞/点踩） | demo 的 `/feedback` 接口 → 写 Score | 5 |
| 评估 | 标注队列（人工审核） | UI + REST 自动入队 | 5 |
| 评估 | LLM-as-a-Judge（在线规则 + 采样） | UI 配置，不写代码 | 6 |
| 评估 | Code Evaluators（确定性检查） | 评估器本身用 Python 或 TS 写，在 Langfuse 里跑 | 6 |
| 评估 | 数据集、版本、从生产 trace 入库、CSV 导入 | REST + UI | 7 |
| 评估 | 实验（UI 实验、代码实验、对比视图） | Go 用 OTel 实验属性自己写 runner | 7 |
| 评估 | CI 门禁（回归不达标就拦住合并） | Go runner 读分数，不达标 exit 1 | 7 |
| 平台 | 组织/项目/角色、API Key、数据保留、导出 | UI + 自部署配置 | 10 |
| 平台 | 自部署架构（Web、Worker、Postgres、ClickHouse、Redis、S3）、升级 | Docker/Helm | 10 |
| 平台 | CLI、MCP Server、Agent Skill | 开发辅助，当工具用 | 随时 |

两点提前说清楚：

- **你不会 Python 没关系。** 只有 Code Evaluator 的脚本本身必须是 Python 或 TS，而且通常只有十几行。其余所有功能都能用 Go + REST + UI 完成。
- **自部署版是完整的。** LLM-as-a-Judge、Playground、Prompt 实验、标注队列都在免费自部署版里（[来源](https://langfuse.com/resources/engineering/clarifications.md)）；少数功能标了 EE，需要 license key，碰到时看文档标记即可。Langfuse Assistant 这类云端功能在自部署里可能没有，以你自己部署的实际界面为准。

## 2. Demo 设计：一个跟着你一起长大的 go-eino 项目

Demo 叫 `lf-lab`，从一次模型调用开始，最终长成“意图路由 + RAG + 工具调用”的多节点工作流。这样学完 Langfuse，你的 Workflow 和 RAG 也已经有了骨架。不建议对着一个只会“你好”的 demo 学 Langfuse：没有分支、没有检索、没有工具，很多功能你根本看不出价值。

演进路线（每一版都是下一阶段 Langfuse 功能的“靶子”）：

1. **v1 单次调用**：命令行调用一次 ChatModel（通义千问或任何 OpenAI 兼容接口）。
2. **v2 HTTP 服务 + 多轮对话**：`POST /chat` 带 `user_id`、`session_id`，内存里存历史消息。
3. **v3 流式输出**：SSE 返回，用来观察首 token 时间和流式 trace。
4. **v4 Agent + 工具**：用 eino ADK 的 ChatModelAgent，挂 2 个假工具（查询办事进度、查询活动），其中一个故意会报错。
5. **v5 RAG**：Embedding + Retriever（先内存向量，再换 Milvus），20 篇小文档的知识库。
6. **v6 多节点工作流**：eino Graph，意图识别节点 → 分支到“闲聊 / 知识问答 / 业务查询”。结构就是你智慧民生工作流的缩小版。

建议的目录结构（第一天就按这个建，后面只往里填）：

```text
lf-lab/
  cmd/server/main.go        // 启动、注册全局回调、优雅关闭
  cmd/experiment/main.go    // 阶段 7：离线实验 runner
  internal/observability/   // Langfuse handler 初始化、手动 span 工具函数
  internal/lfclient/        // 自己封装的 Langfuse REST 客户端
  internal/prompt/          // Prompt 拉取、缓存、编译、兜底
  internal/flow/            // eino Graph / Agent
  internal/rag/             // 切分、向量化、检索
  internal/api/             // HTTP handler：/chat、/feedback
  testdata/                 // 知识库文档、数据集 CSV
```

`internal/observability` 和 `internal/lfclient` 这两个包要当成“未来要搬进 Workflow Base 的公共组件”来写：接口干净、配置外置、有单元测试。

## 3. 分阶段路线：10 个阶段，约 6 周

每个阶段都是“学一个功能 → 在 demo 里用起来 → 到 UI 里验证”。验收标准没过就不进下一阶段。用时是按每天 2–3 小时估的。

| 阶段 | 主题 | Demo 版本 | 建议用时 |
| --- | --- | --- | --- |
| 1 | 跑通第一条 trace | v1 | 1 天 |
| 2 | Trace 属性与层级结构 | v2 | 2 天 |
| 3 | 成本、流式、UI 分析 | v3 | 2 天 |
| 4 | Prompt 管理 | v4 | 4 天 |
| 5 | Scores、用户反馈、标注队列 | v4 | 3 天 |
| 6 | 在线自动评估 | v5 | 3 天 |
| 7 | 数据集、实验、CI 门禁 | v6 | 5 天 |
| 8 | Dashboard、Metrics API、告警 | v6 | 2 天 |
| 9 | 生产级接入 | v6 | 4 天 |
| 10 | 自部署运维与平台管理 | — | 3 天 |

### 阶段 1：跑通第一条 trace

- **学什么**：Trace / Observation / Session 的关系；API Key 和 Basic Auth；OTLP 接口与 `x-langfuse-ingestion-version: 4` 头。
- **Demo 任务**：在 Langfuse 里建项目、拿密钥，让 v1 的一次模型调用上报：

```go
handler, err := langfuse.NewHandler(ctx, &langfuse.Config{
    Host:        os.Getenv("LANGFUSE_HOST"), // http://localhost:3000
    PublicKey:   os.Getenv("LANGFUSE_PUBLIC_KEY"),
    SecretKey:   os.Getenv("LANGFUSE_SECRET_KEY"),
    ServiceName: "lf-lab",
    Environment: "dev",
})
if err != nil { panic(err) }
defer handler.Shutdown(ctx)              // 不调用会丢数据
callbacks.AppendGlobalHandlers(handler)  // 所有 eino 组件自动上报

traceCtx := handler.StartTrace(ctx,
    langfuse.WithName("hello"),
    langfuse.WithInput(question),
)
resp, err := chatModel.Generate(traceCtx, msgs) // 必须传 traceCtx
handler.EndTrace(traceCtx, resp.Content)
```

- **故意弄坏它**：写错 SecretKey、去掉 `Shutdown`、用 `ctx` 而不是 `traceCtx` 调模型，分别看会发生什么。这三个坑你在公司项目里一定会遇到。
- **验收**：UI 里看到一条 trace，下面有一个 generation，带模型名、输入输出、token 数；能口头讲清楚 trace、observation、generation 的区别。

### 阶段 2：Trace 属性与层级结构

- **学什么**：User、Session、Tags、Metadata、Release、Version、Environment；10 种 observation 类型；为什么 v4 里这些属性要传播到**每个** span（v4 的过滤和聚合是按 observation 做的）。
- **Demo 任务**：
  - 升级到 v2（HTTP + 多轮），每次请求 `StartTrace` 时带上 `WithUserID`、`WithSessionID`、`WithTags`、`WithRelease`（用 git commit）。
  - 自己创建一个 OTel `TracerProvider` 传给 `Config.TracerProvider`，让你手写的 span 和 eino 的 span 在同一棵树上。用它给“加载历史消息”这一步包一个手动 span。
  - 对比两种 metadata：普通 OTel 属性会被放进 `metadata.attributes`，不能过滤；只有 `langfuse.observation.metadata.*` 前缀的才是一级、可过滤的（[属性映射表](https://langfuse.com/integrations/native/opentelemetry.md)）。
- **验收**：在 Sessions 页看到一个多轮会话完整按顺序串起来；按 user、tag、release、自定义 metadata 都能过滤出目标 observation。

### 阶段 3：成本、流式、UI 分析

- **学什么**：token 用量与成本怎么算、模型价格定义；首 token 时间；Observations 表、过滤栏、全文搜索、Saved Views、Pulse。
- **Demo 任务**：
  - 升级到 v3（SSE 流式），确认流式调用也有完整输出和 token。
  - 如果你用的是通义、DeepSeek 等国产模型，成本很可能显示为 0。在项目设置里给它们配价格，让成本计算生效。
  - 建 3 个 Saved View：“慢请求 > 5s”、“所有报错”、“今天最贵的 generation”。
- **验收**：能在 1 分钟内回答“昨天哪个用户花钱最多、哪类请求最慢”。

### 阶段 4：Prompt 管理

- **学什么**：版本与 Label（production / staging / latest）；text 和 chat 两种 prompt；变量、Prompt 引用、Message Placeholder、config 字段；缓存与兜底；Playground；把 prompt 版本关联到 generation。
- **Demo 任务**：
  - 升级到 v4（Agent + 工具），把系统提示词和工具描述全部搬到 Langfuse，代码里不再有硬编码 prompt。
  - 写 `internal/lfclient` 和 `internal/prompt`：用 `GET /api/public/v2/prompts/{name}?label=production` 拉取；本地 TTL 缓存（如 60 秒）；Langfuse 挂了时用编译进二进制的默认版本兜底；自己实现 `{{var}}` 替换和 placeholder 展开。
  - 关联 prompt：给模型层的 span 打上 `langfuse.observation.prompt.name` 和 `langfuse.observation.prompt.version`。eino 回调不帮你做这件事，可以写一个自定义 OTel SpanProcessor，在 `OnStart` 里从 ctx 读出当前 prompt 信息写进去。这是整条路线里最需要你亲手验证的一处，以 UI 里 prompt 详情页能看到关联的 generation 为准。
  - 在 Playground 里配一个 LLM Connection（通义走 OpenAI 兼容接口），对同一个 prompt 的两个版本做手工对比。
- **验收**：在 UI 里把 production label 从 v2 改到 v3，不重启服务，60 秒内新请求就用上 v3；在 prompt 页能看到每个版本的调用量、延迟、成本。

### 阶段 5：Scores、用户反馈、标注队列

- **学什么**：Score 的三种类型（数值/分类/布尔）和 Score Config；打在 trace、observation、session 上的区别；标注队列。读分数用 Scores API v3，v2 的读接口已废弃（[来源](https://langfuse.com/docs/api-and-data-platform/features/query-via-sdk)）。
- **Demo 任务**：
  - `/chat` 响应里返回 `trace_id`（从 `traceCtx` 的 SpanContext 取）。
  - 新增 `POST /feedback`：点赞/点踩写成一个布尔 score，1–5 星写成数值 score，都先在 UI 里建好 Score Config。
  - 建一个标注队列“差评复核”，点踩时自动把这条 trace 入队。你自己当标注员标 20 条。
- **验收**：标注完的人工分和用户反馈分能在同一条 trace 上并列看到。

### 阶段 6：在线自动评估

- **学什么**：评估器（Evaluator）和规则（Rule）的分工：Evaluator 定义怎么打分，Rule 定义给哪些 observation 打分；过滤与采样、变量映射、Tool Calls 变量；LLM-as-a-Judge 与 Code Evaluator 的适用场景；评估器成本告警。
- **Demo 任务**：
  - 升级到 v5（RAG）。配 3 个 LLM-as-a-Judge：答案是否基于检索内容（groundedness）、检索是否相关、工具选择是否合理。只对特定名称的 observation 生效，采样 20%。
  - 写一个 Code Evaluator（TS 十几行）：检查意图识别节点的输出是不是合法 JSON、意图在枚举里。
  - 拿阶段 5 的 20 条人工标注去校准裁判：人和模型意见不一致的地方，改裁判 prompt。
- **验收**：发 50 个请求，约 10 个自动带上裁判分。如果你通过 OTel 上报的数据没触发评估器，先查 ingestion 版本头和 Langfuse 版本。

### 阶段 7：数据集、实验、CI 门禁

- **学什么**：Dataset / Item / 版本；从生产 trace 加入数据集；UI 实验与代码实验；实验对比视图；用分数卡 CI。
- **Demo 任务**：
  - 升级到 v6（多节点工作流）。建两个数据集：`intent-routing`（30 条，输入问句 → 期望意图）和 `rag-qa`（20 条，问题 → 参考答案）。一部分用 CSV 导入，一部分从阶段 5 的差评 trace 一键加入。
  - 写 `cmd/experiment`：Go 没有现成的实验 runner，按 [OTel 实验规范](https://langfuse.com/integrations/native/opentelemetry/experiments.md)自己实现：实验级属性（`langfuse.experiment.id`、`name`、`dataset.id`）放 Baggage；每个 item 一个独立的根 span，写入 item id、期望输出，以及等于自身 spanId 的 `root_observation_id`；子 span 通过 Baggage 继承 item 信息。Go 里用 contrib 的 baggagecopy 处理器把 Baggage 拷贝到 span。
  - 跑完用精确匹配算意图准确率，把分数写到每个 item 的根 observation 上。
  - 用两个 prompt 版本各跑一次，在对比视图里看哪些 item 变好、变坏；再在 UI 里不写代码跑一次 prompt 实验。
  - CI：GitHub Actions 里跑 `go run ./cmd/experiment`，准确率低于阈值就 `exit 1`。
- **验收**：改一个 prompt 导致意图准确率下降，CI 能拦住；在 UI 里能指出是哪几条样本变坏了。

### 阶段 8：Dashboard、Metrics API、告警

- **学什么**：自定义 Dashboard；Metrics API v2（聚合和时间序列查询）；Monitors（成本、质量、延迟阈值 → Slack / Webhook / GitHub Actions）。
- **Demo 任务**：
  - 建一个“工作流健康度” Dashboard：按意图分的请求量、P95 延迟、每日成本、差评率、裁判平均分。
  - 在 Go 里调 Metrics API，给以后 Workflow Base 的管理后台提供“今日成本 / 调用量”接口。
  - 配一个 Monitor → Webhook 到你 Go 写的接收端，再转发到企业微信或飞书机器人。
- **验收**：故意让工具连续报错，几分钟内手机上收到告警。

### 阶段 9：生产级接入

- **学什么**：脱敏、采样、属性大小限制、队列与批量参数、重试、优雅关闭；Environment 隔离；`CollapseAgentInternalSpans` 精简 Agent 内部 span；跨服务分布式追踪。
- **Demo 任务**：
  - `MaskFunc` 用正则脱敏手机号、身份证号；设 `SampleRate`、`MaxAttributeValueLength`；监听 SIGTERM 先 `Shutdown` 再退出。
  - **停掉 Langfuse 容器压测你的服务**：业务延迟和成功率不能受影响，只允许打出丢弃警告。这是企业评审一定会问的问题。
  - 写一个最小的 Java 服务（你的主场），用 OTel Java agent（协议设为 http/protobuf）调用 Go demo，通过 `traceparent` 串成一条 trace。
- **验收**：Java → Go → LLM 的完整链路在一条 trace 里；日志和 trace 里看不到任何明文手机号。

### 阶段 10：自部署运维与平台管理

- **学什么**：组件分工（Web、Worker、Postgres、ClickHouse、Redis、S3/MinIO）、组织/项目/角色与 API Key 管理、数据保留与导出、备份、升级策略；生产用 Kubernetes (Helm) 而不是 Docker Compose（[官方建议](https://langfuse.com/self-hosting.md)）。
- **任务**：
  - 画出一条 span 从 Go 发出到出现在 UI 的完整路径（谁负责接收、排队、落库）。
  - 按 dev / staging / prod 建项目和独立 Key，配数据保留期。
  - 用一台虚拟机先装 v3、写入数据，再按[官方升级指南](https://langfuse.com/self-hosting/upgrade.md)完整演练一遍 v3→v4（先升 ClickHouse、备份、双写、回填、切换）。
- **验收**：能写出一份「公司内部 Langfuse 部署方案」，包含资源规格、备份、升级、权限四部分。

## 4. 企业级落地清单与常见坑

学完 10 个阶段后，把下面这份清单直接用到 Workflow Base 和 RAG Base 里。每一项都对应前面某个阶段已经练过的东西。

- [ ] 观测代码收在一个包里，业务代码只调 `StartTrace` / `EndTrace`
- [ ] 每个请求都带 user、session、release、environment，并传播到所有 span
- [ ] 所有 prompt 都在 Langfuse 里，代码里只有兜底版本；上线靠切 label 而不是发版
- [ ] 每个 generation 都关联了 prompt 名称和版本
- [ ] 用户反馈接口 + 差评自动进标注队列
- [ ] 关键节点有在线裁判，并用人工标注校准过
- [ ] 每个工作流有自己的回归数据集，改 prompt 或换模型前必须跑实验，CI 有分数门禁
- [ ] 成本、延迟、质量有 Monitor 告警
- [ ] 脱敏、采样、大小限制已配置；Langfuse 宕机不影响业务
- [ ] dev / staging / prod 分项目、分 Key，有备份和升级方案

常见坑（大部分你会在阶段 1–3 就踩到）：

| 现象 | 原因 | 解法 |
| --- | --- | --- |
| trace 断成好几条、层级乱 | 调用 eino 组件时传的是原始 `ctx` | 全程传 `StartTrace` 返回的 ctx |
| 短进程（CLI、实验 runner）数据丢失 | 批量导出还没发出进程就退了 | 退出前 `Flush` + `Shutdown` |
| 自己配的 OTel 导出器报错 | 用了 gRPC | 只用 OTLP HTTP |
| 数据延迟很久才出现 | 手动导出器没加 `x-langfuse-ingestion-version: 4`，可能延迟最多 10 分钟 | 补上这个头（eino v2 回调已自带） |
| 按 user 或 tag 过滤不到子节点 | 属性只打在根 span 上 | 传播到所有 span（Baggage 或 eino v2 自动传播） |
| metadata 不能过滤 | 没加 `langfuse.*.metadata.` 前缀 | 加前缀 |
| 公司已有 OTel（SkyWalking、Jaeger）打架 | 共用了全局 TracerProvider | Langfuse 用独立 Provider，或在 Collector 层分流（[官方 FAQ](https://langfuse.com/integrations/native/opentelemetry.md)） |
| 成本显示 0 | 国产模型没有内置价格 | 在项目里自定义模型价格 |
| RAG trace 巨大、导出失败 | 把整篇文档都塞进了输入输出 | `MaxAttributeValueLength` / `MaxSpanAttributeBytes` |
| Langfuse 一挂，业务也挂 | prompt 拉取没有缓存和兜底 | 本地缓存 + 内置默认版本 |
| 敏感信息传到了第三方 | Baggage 里放了用户信息，会随请求头传到下游 | Baggage 只放无敏感的 ID |

做 ToB 交付时还要提前想一个问题：多个客户用同一套平台，是一个客户一个 Langfuse 项目（隔离好、Key 多），还是共用项目用 metadata 区分（管理简单、权限难做）。阶段 10 结束时你应该能给出自己的选择和理由。

## 5. 学习方法与节奏

每个阶段固定走 4 步：读对应的官方文档 → 在 demo 里实现 → 故意弄坏再修好 → 写一篇笔记。

- **查文档的方法**：Langfuse 的每个文档页在 URL 后加 `.md` 就是纯 Markdown；也可以直接用他们的语义搜索接口 `https://langfuse.com/api/search-docs?query=...` 问具体问题。接口字段以 [API Reference](https://api.reference.langfuse.com) 为准，不要从别人的代码里推断。
- **笔记就是公众号素材**：“Go 接入 Langfuse v4 的 10 个坑”“用 Go 自己写 Langfuse 实验 runner”这类内容，中文社区几乎没有人写过。
- **关于“先完全精通再做 Workflow / RAG”**：建议别把两件事完全分开。Langfuse 的价值只有在真实的工作流里才看得出来，所以这份路线已经把 RAG 和多节点工作流嵌进了 demo。阶段 7 结束时，可以开始搭 Workflow Base，阶段 8–10 就在 Base 项目里直接做，这样更接近企业里的真实情况。
- **和我的配合方式**：每次告诉我“开始阶段 N”，我给你这一阶段的完整代码和操作步骤；你把报错、UI 截图或者“为什么是这样”的问题发给我。过了验收再进下一阶段。

## 参考资料

- [eino-ext callbacks/langfuse/v2（pkg.go.dev）](https://pkg.go.dev/github.com/cloudwego/eino-ext/callbacks/langfuse/v2)
- [Langfuse OpenTelemetry 接入与属性映射](https://langfuse.com/integrations/native/opentelemetry.md)
- [用 Go / Java 等语言通过 OTel 接入](https://langfuse.com/resources/engineering/opentelemetry-languages.md)
- [通过 OTel 上报实验](https://langfuse.com/integrations/native/opentelemetry/experiments.md)
- [评估核心概念](https://langfuse.com/docs/evaluation/core-concepts.md)
- [Langfuse v4 公告](https://langfuse.com/changelog/2026-08-17-langfuse-v4.md)
- [自部署概览](https://langfuse.com/self-hosting.md) · [升级指南](https://langfuse.com/self-hosting/upgrade.md)
- [自部署 v3→v4 迁移实测（第三方）](https://jacar.es/?p=7396)
- [通过 API 查询数据（含 Scores v3）](https://langfuse.com/docs/api-and-data-platform/features/query-via-sdk)
