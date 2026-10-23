# XiaoWork Blog V1.0 API 接口详细设计说明书（API-DD）

> **版本：** V1.0-Draft 1（接口评审稿）  
> **编制日期：** 2026-10-22  
> **项目阶段：** 设计阶段，**未编码、未部署、未生成 OpenAPI 可执行契约**  
> **上游基线：** 《产品需求文档（PRD）》《系统架构设计说明书（SAD）》《数据库详细设计说明书（DD）》  
> **建议技术：** Nuxt 4 + Go / Chi v5 + PostgreSQL / pgx / sqlc  
> **评审对象：** 博客所有者、前端/后端开发 Agent、测试 Agent

---

## 1. 目标与边界

本文指定浏览器 / Nuxt 与 Go API 之间的稳定接口契约，包括：路径、访问权限、参数、返回结构、并发规则、安全与验收。本文不是代码，不替代数据库 DDL，也不增加 PRD 之外的产品能力。

**已经正式确定、不可擅改的需求：**

1. 左侧个人资料与少量外链图标，右侧博客文章；现代简约、响应式。
2. Markdown 原文编辑、实时预览；公开文章与编辑草稿**物理和业务隔离**，只有管理员手动发布/更新才影响线上内容。
3. 邮箱与密码注册、邮箱验证、邮件找回；用户需注册并验证邮箱后才能发表评论。
4. 评论通过合法性与风控检查后**立即公开**，管理员事后隐藏、删除、禁用；无人工审核队列。
5. 图片使用宿主机 Docker 持久化目录；文章草稿图片不允许匿名访问。
6. 搜索、分类、标签、归档、用户中心、SEO、基础管理后台、备份恢复属于 V1.0 范围。
7. 不做第三方登录、评论点赞、通知、完整历史版本、多管理员协作、Redis/消息队列、对象存储。

**约定状态：** 标有“建议默认”的具体数值是此轮接口设计建议，实施前可以统一微调；不代表此前已由用户单独确认。

## 2. 部署与路由边界

```text
浏览器 / Nuxt SSR
      │  HTTPS  https://blog.example.com
      ▼
Nginx Proxy Manager（唯一公网入口）
      ├── /api/v1/*       → blog-api:8080 (Go / Chi)
      ├── /media/*        → blog-api:8080 (Go 文件读取/鉴权)
      └── /*              → blog-web:3000 (Nuxt SSR)
                                   │
                         Docker 内网 + 宿主机挂载
                         PostgreSQL / media 目录
```

- 对用户表现为**同源**：浏览器调用 `/api/v1`；不使用浏览器可访问的第二个 API 子域名。
- Nuxt SSR 公共页面可以通过 Docker 内网读取 Go API，但不能在 SSR 过程中泄露管理员凭据。涉及用户个性化 SSR 的请求需**明确转发当前请求的必要 Cookie**，不得缓存到其他用户。禁止把私人令牌嵌入 Nuxt 公共静态 payload。
- Go 端只有信任的 NPM / Nuxt 代理可以设置代理链；拒绝任意客户端伪造的来源 IP 头。数据库端口不暴露公网。
- 正式页面地址由 Nuxt 路由拥有，例如 `/posts/{slug}`；API 返回文章数据，Go 不负责呈现 HTML 页面。
- `sitemap.xml`、`robots.txt`、`rss.xml` 由 Nuxt SEO 路由最终生成，通过公开 API 读取已发布数据；**它们不是本文的 `/api/v1` JSON API**。
- **前后端契约优先 API**：Nuxt 不直连 PostgreSQL、不绕过 Go 权限校验。

## 3. API 全局约定

### 3.1 路径、数据类型、时间和分页

| 规则 | 统一约定 |
|---|---|
| 基础路径 | `/api/v1`；`/media` 为专用文件读取路径 |
| 格式 | JSON 请求/响应为 `application/json; charset=utf-8`；上传使用 `multipart/form-data` |
| 命名 | JSON 字段 `snake_case`；路径用小写复数名词，少量动作子路径如 `/publish` |
| 主键 | UUID 字符串；文章公开 URL 由 Slug 路由，但内部联系均使用稳定 `article_id` |
| 时间 | RFC 3339，使用 UTC 带 `Z`；浏览器本地化显示，不混用本地时间字符串 |
| 计数 | 整数；基础阅读数为近似值，不能解释为独立访客量 |
| 可空字段 | 字段存在但无值时使用 `null`；写接口 `null` 表示清空，未传字段的语义按接口明确 |
| 删除 | 删除成功优先 `204 No Content`；回收站/墓碑是业务动作，不代表物理删除 |
| 分页 | 列表默认 `page=1&page_size=10`；后台/用户 `page_size=20`；上限 `100` |
| 排序 | 明确稳定次序；发布时间/创建时间相同使用 UUID 作为第二排序键 |
| Request ID | 客户端可传 `X-Request-Id`（仅在校验合法格式后采用）；响应统一含 `X-Request-Id` |
| 版本 | 重大不兼容变更升 `/api/v2`；V1 内尽量向后兼容增加可选字段 |

本阶段选择**普通页码分页**满足 V1.0；列表在并发变化下可能跨页重复，这是可接受的首版行为。评论回复单独分页，避免一次加载整棵深树。

### 3.2 成功响应

- 单体或列表：`{ "data": ..., "meta": ... }`。`meta` 在单体请求中可省略。
- 资源新建：HTTP `201`；异步/安全泛化的受理：`202`；普通读取/变更：`200`；无响应体删除：`204`。
- 列表 `meta`：`page`、`page_size`、`total`、`total_pages`（总量是本次查询时的近似/快照，不承诺多页全程一致）。
- `X-Request-Id` 为响应头；JSON 错误也包含 `request_id`，方便排障。

```json
{
  "data": [{"id":"c0000000-0000-4000-8000-000000000001","title":"Docker 部署笔记","slug":"docker-guide"}],
  "meta": {"page":1,"page_size":10,"total":18,"total_pages":2}
}
```

### 3.3 错误响应与 HTTP 语义

```json
{
  "error": {
    "code": "DRAFT_REVISION_CONFLICT",
    "message": "草稿已在其他位置更新，请重新加载后比较差异。",
    "details": {"current_revision": 8}
  },
  "request_id": "req_20261022_a1b2c3"
}
```

| HTTP | 含义 | 示例错误码 |
|---|---|---|
| 400 | 参数语法/格式问题 | `INVALID_REQUEST`, `INVALID_SLUG` |
| 401 | 未登录/登录过期 | `AUTH_REQUIRED`, `SESSION_EXPIRED` |
| 403 | 已登录但不允许 | `FORBIDDEN`, `EMAIL_NOT_VERIFIED`, `ACCOUNT_DISABLED`, `CSRF_INVALID` |
| 404 | 不存在**或对当前用户不可见** | `NOT_FOUND`；不向游客证明草稿存在 |
| 409 | 当前业务状态冲突 | `EMAIL_ALREADY_USED`, `DRAFT_REVISION_CONFLICT`, `SLUG_TAKEN`, `RESOURCE_IN_USE`, `ARTICLE_NOT_PUBLISHED` |
| 413 | 文件/请求体超限 | `PAYLOAD_TOO_LARGE` |
| 415 | 媒体类型不支持 | `UNSUPPORTED_MEDIA_TYPE` |
| 422 | 可解析但业务字段不满足验证 | `VALIDATION_FAILED`, `COMMENT_CLOSED`, `MEDIA_NOT_READY` |
| 429 | 限流 | `RATE_LIMITED`，带 `Retry-After` |
| 500 | 服务端未预期故障 | `INTERNAL_ERROR`；不得泄漏 SQL/磁盘路径 |
| 503 | 外部依赖暂时不可用 | `MAIL_UNAVAILABLE`, `SERVICE_UNAVAILABLE` |

- 表单字段错误在 `details.fields` 中给 `field → [error_code]`，不返回数据库错误原文。
- 资源已撤回、回收或未发布：**公开查询统一 404**；管理员读取仍能查看后台状态。
- **具体选择**：草稿采用 JSON `revision` CAS（比较并交换）冲突返回 **409**；不另混用 `If-Match` / 412，避免 Agent 开发出两套并发协议。若未来改成 ETag 条件请求，需要整体更新规范。

### 3.4 Cookie、Session、CSRF、缓存

1. 登录成功由 Go 返回 `Set-Cookie: __Host-blog_session=<random>; Path=/; Secure; HttpOnly; SameSite=Lax`；生产环境仅 HTTPS。Cookie 令牌是高熵随机值，数据库只存摘要；没有 `Domain` 属性。
2. **建议默认**：普通 Session 绝对过期 14 天；未提供“记住我”功能。后端在所有受保护请求检查未过期、未撤销以及账号状态。登录成功轮换会话凭据。
3. `GET /auth/csrf` 为同源浏览器提供一个与匿名/已登录安全上下文绑定的防 CSRF 令牌；变更型请求携带 `X-CSRF-Token`。可使用**签名的双提交 Cookie**方案，实现时必须绑定对应会话/匿名上下文并在登录后轮换。所有状态修改还需进行 `Origin` 校验；同源策略与 `SameSite` 仅为纵深防御。
4. Nuxt 可从 Go 获取 CSRF Token 并用于后续写请求；**不要**把 Session Cookie 值放入 JS、localStorage、日志或 SSR 公共 payload。
5. **无变更 GET**：邮箱链接先进入 Nuxt 确认页，由用户确认后发 POST 消费令牌，避免邮件安全网关预读取链接触发误操作。
6. 登录后的个人信息、草稿、后台和 CSRF 响应 `Cache-Control: private, no-store`；公开已发布文章可以采用短时缓存及失效策略，但**绝不共用含用户态的缓存**。
7. 不放开任意 CORS 源；响应使用合理 CSP、`X-Content-Type-Options: nosniff` 和上传安全策略。
8. 生产域名示例仅为占位，实际域名在部署阶段确定。

### 3.5 接口权限缩写

`P` 公开（游客可读）；`U` 有效已验证普通用户/管理员；`S` 仅有效登录用户本人；`A` 管理员；`T` 可匿名调用但需 CSRF、限流或一次性令牌。

注册未验证账号没有正常 Session 访问权限，仍可通过公开的验证/重发入口完成激活。普通登录认证必须检查邮箱已验证（未验证账号返回 `EMAIL_NOT_VERIFIED`，提供重新发送邮件入口）。

## 4. 接口总目录

下表为**V1.0 接口契约草案**，与上游数据库 24 表对应；不是已经可调用的服务。

### 4.1 公开站点、文章与发现（P）

| 方法 | 路径 | 作用 | 特别说明 |
|---|---|---|---|
| GET | `/site` | 网站标题、描述、个人栏、公开导航与外链 | 不含 SMTP 密钥/私人邮箱 |
| GET | `/pages/about` | 关于页公开内容 | 只返回发布内容 |
| GET | `/articles` | 公开文章分页列表 | `page,page_size,category,tag,sort` |
| GET | `/articles/{slug}` | 文章详情与公开 Markdown | 包含 `canonical_path`、上下篇摘要 |
| GET | `/search/articles` | 文章搜索 | `q,page,page_size`，仅检索公开快照 |
| GET | `/categories` | 分类清单 | 可返回公开文章数量 |
| GET | `/categories/{slug}/articles` | 分类文章列表 | 仅公开文章 |
| GET | `/tags` | 标签清单 | 可返回公开文章数量 |
| GET | `/tags/{slug}/articles` | 标签文章列表 | 仅公开文章 |
| GET | `/archives` | 年/月归档及文章摘要 | `year` 可选 |
| GET | `/articles/{article_id}/comments` | 顶层评论分页 | `sort=latest/oldest` |
| GET | `/comments/{root_id}/replies` | 单棵评论树下回复分页 | 限制每页数量 |
| POST | `/articles/{article_id}/views` | 浏览量近似计数事件 | `P`，限流与防爬，`204` |

### 4.2 注册、认证、个人中心（T / S）

| 方法 | 路径 | 作用 | 权限 |
|---|---|---|---|
| GET | `/auth/csrf` | 获取防 CSRF 令牌 | P |
| POST | `/auth/register` | 邮箱注册与初次验证邮件申请 | T |
| POST | `/auth/resend-verification` | 重新发送邮箱验证链接 | T |
| POST | `/auth/verify-email` | 消费邮箱验证令牌 | T |
| POST | `/auth/login` | 邮箱、密码登录 | T |
| POST | `/auth/logout` | 当前会话退出 | S |
| POST | `/auth/logout-all` | 撤销本账号所有会话 | S |
| GET | `/auth/me` | 当前登录用户与角色 | S |
| POST | `/auth/forgot-password` | 请求密码重置邮件 | T |
| POST | `/auth/reset-password` | 消费重置令牌并设置新密码 | T |
| PATCH | `/me/profile` | 修改本人昵称、简介 | S |
| POST | `/me/avatar` | 上传并切换本人头像 | S，multipart |
| POST | `/me/change-password` | 已登录时修改密码 | S |
| POST | `/me/change-email/request` | 向新邮箱发送更改确认链接 | S |
| POST | `/me/change-email/confirm` | 消费新邮箱确认令牌 | S，必须有有效 Session 与 CSRF |
| GET | `/me/comments` | 我的评论列表 | S |
| DELETE | `/comments/{comment_id}` | 删除自己的评论，保留墓碑 | S，所有权检查 |
| POST | `/articles/{article_id}/comments` | 新增顶层评论 | U |
| POST | `/comments/{comment_id}/replies` | 回复指定评论 | U |

### 4.3 文章与分类标签后台（A）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/admin/overview` | 后台统计摘要 |
| GET | `/admin/articles` | 后台文章分页/筛选 |
| POST | `/admin/articles` | 新建文章及草稿 |
| GET | `/admin/articles/{article_id}` | 管理详情：状态、草稿、公开版本摘要 |
| PUT | `/admin/articles/{article_id}/draft` | 保存**完整草稿快照**，Revision CAS |
| POST | `/admin/articles/{article_id}/publish` | 首次发布或手动发布更新 |
| POST | `/admin/articles/{article_id}/withdraw` | 撤回公开 |
| DELETE | `/admin/articles/{article_id}` | 软删除至回收站 |
| POST | `/admin/articles/{article_id}/restore` | 回收站恢复成非公开状态 |
| GET | `/admin/categories` | 管理分类列表 |
| POST | `/admin/categories` | 新增分类 |
| PATCH | `/admin/categories/{id}` | 修改分类 |
| DELETE | `/admin/categories/{id}` | 无关联后删除分类 |
| GET | `/admin/tags` | 管理标签列表 |
| POST | `/admin/tags` | 新增标签 |
| PATCH | `/admin/tags/{id}` | 修改标签 |
| DELETE | `/admin/tags/{id}` | 无关联后删除标签 |

### 4.4 评论、用户、媒体与站点管理（A）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/admin/comments` | 按文章/作者/状态分页筛选评论 |
| POST | `/admin/comments/{id}/hide` | 隐藏违规评论 |
| POST | `/admin/comments/{id}/unhide` | 恢复隐藏评论 |
| DELETE | `/admin/comments/{id}` | 管理员删除评论，保留关系墓碑 |
| GET | `/admin/users` | 分页检索账号 |
| GET | `/admin/users/{id}` | 账号管理详情与其评论摘要 |
| POST | `/admin/users/{id}/disable` | 禁用普通账号、撤销其 Session |
| POST | `/admin/users/{id}/enable` | 恢复账号 |
| GET | `/admin/media` | 管理媒体库列表 |
| POST | `/admin/media` | 上传 ARTICLE/SITE/PAGE 图片（multipart） |
| DELETE | `/admin/media/{id}` | 无引用且有权限时删除 |
| GET | `/admin/site` | 读取可编辑站点配置 |
| PATCH | `/admin/site` | 修改站点配置（不含部署密钥） |
| PUT | `/admin/site/social-links` | 全量排序及替换个人栏链接 |
| PUT | `/admin/site/navigation-links` | 全量排序及替换顶部导航 |
| GET | `/admin/pages/about` | 查看关于页编辑原文 |
| PUT | `/admin/pages/about` | 直接保存并公开关于页 |

**独立文件读取端点（P / S / A）**：`GET /media/{media_id}/file` 与 `HEAD /media/{media_id}/file`；无权访问时返回 404。列表中的 `url` 是此相对地址，而不是服务器绝对路径。

## 5. 关键 DTO（请求与响应模型）

### 5.1 公开 `ArticleSummary` 与 `ArticleDetail`

```json
{
  "id": "c0000000-0000-4000-8000-000000000001",
  "slug": "docker-guide",
  "title": "Docker 部署笔记",
  "summary": "一个可以复现的部署流程",
  "cover": {"id":"f0000000-0000-4000-8000-000000000001","url":"/media/f0000000-0000-4000-8000-000000000001/file"},
  "category": {"id":"d0000000-0000-4000-8000-000000000001","name":"开发笔记","slug":"dev"},
  "tags": [{"id":"e0000000-0000-4000-8000-000000000001","name":"Docker","slug":"docker"}],
  "first_published_at": "2026-10-22T09:00:00Z",
  "public_updated_at": "2026-10-22T09:00:00Z",
  "view_count": 123,
  "comments_enabled": true
}
```

- 列表不返回整篇 Markdown、管理员字段、草稿、未公开媒体。
- 详情额外返回 `body_markdown`、`seo_title`、`seo_description`、`canonical_path`、`previous_article`、`next_article`、`resolved_from_old_slug`。
- Markdown 的 HTML 与链接安全策略在渲染阶段实施：禁用任意原始 HTML 或严格净化，拒绝 `javascript:` URL。存储原文不意味着渲染安全。
- 旧 Slug 请求：Go API 可以返回 `200` 和 `canonical_path=/posts/{current_slug}`、`resolved_from_old_slug=true`；**Nuxt 的页面路由负责对人类访问的旧 `/posts/{old_slug}` 返回 HTTP 301**。撤回/删除均 404，不跳转。
- 分类或标签被改名可以立即改变公开显示名；编辑**某篇文章的标签关联**必须到发布后才改变。

### 5.2 管理 `ArticleDraft` 与 `PUT /admin/articles/{id}/draft`

新建文章 `POST /admin/articles` 不需要标题正文，产生 `articles(status=DRAFT)` 和空草稿 `revision=1`，返回 `201` 与 `article_id`、`draft.revision`。

保存草稿采用**完整覆盖 DTO**，避免表单局部更新与自动保存交错造成遗漏。缺失关键字段视为 `400`，可空项用 `null` 显式清除。

```json
{
  "expected_revision": 7,
  "title": "Docker 部署笔记",
  "summary": null,
  "body_markdown": "# Docker 部署笔记\n这里是正文……",
  "candidate_slug": "docker-guide",
  "category_id": null,
  "tag_ids": ["e0000000-0000-4000-8000-000000000001"],
  "cover_media_id": null,
  "seo_title": null,
  "seo_description": null,
  "comments_enabled": true
}
```

成功 `200`：

```json
{"data":{"article_id":"c0000000-0000-4000-8000-000000000001","revision":8,"saved_at":"2026-10-22T10:00:00Z","has_unpublished_changes":true}}
```

**草稿冲突**：如果数据库当前 `revision != expected_revision`，返回 `409 DRAFT_REVISION_CONFLICT`，`details.current_revision` 为服务端当前修订号；前端**必须保留本地未保存文字**，提示刷新或手动比较，不允许静默覆盖。即使旧请求晚到也不能覆盖新内容。

- 初次发布前可保存空标题/正文，但发布要求标题/正文非空。
- 当前公开状态下自动保存仅触及草稿及其媒体/标签关联，**不得写 `article_publications`、公开标签和公开媒体关联**。
- 允许草稿中的 Slug 候选与其他文章当前 Slug 暂时相同，直到发布时才最终做全站唯一性校验。界面可以提前给提示，但以发布事务为权威。
- 建议默认限值：标题最多 200 字符、Slug 最多 160 字符；后端验证 Unicode 长度并与数据库字段限制保持一致。Markdown 请求体大小建议不超过 2 MiB（可配置）。
- `has_unpublished_changes` 来源于 `draft.revision != articles.last_published_draft_revision` 或相关状态，而不是前端随意猜测。

**Markdown 文件导入与导出（PRD POST-11）：** Nuxt 编辑器可在浏览器侧读取本地 `.md` 文件并写入当前草稿表单，随后通过上述草稿 API 保存；导出时将 `body_markdown` 作为 `.md` 文件下载。首版不额外引入“服务端永久保存导入文件”的 API，也不把 Markdown 作为任意类型附件上传。

### 5.3 发布接口（最关键）

`POST /admin/articles/{article_id}/publish`：

```json
{"expected_draft_revision": 8}
```

成功 `200`：

```json
{
  "data": {
    "article_id": "c0000000-0000-4000-8000-000000000001",
    "status": "PUBLISHED",
    "publication_revision": 2,
    "source_draft_revision": 8,
    "slug": "docker-guide",
    "canonical_path": "/posts/docker-guide",
    "published_changed": true,
    "published_at": "2026-10-22T10:10:00Z"
  }
}
```

**Go Service 必须在一个数据库事务里：** 锁定文章行 → 核对 expected revision / 状态 → 校验标题、正文、Slug、分类标签、媒体引用有效性 → 写当前公开快照 → 切换公开标签和媒体引用 → 注册/转移 Slug → 更新生命周期/时间 → 写操作审计 → 提交。任一阶段失败全部回滚。

- 同一草稿修订已发布且文章**仍为 PUBLISHED**：重复点击返回 `200`，`published_changed=false`，不会再次增加 `publication_revision` 或改动公开时间（业务幂等）。
- 已撤回的文章再次显式发布：即使草稿修订没变，也应恢复公开状态并写新公开时间；这不属于误触重复发布。
- 服务端草稿版本发生变化：`409 DRAFT_REVISION_CONFLICT`；Slug 被其他文章占用：`409 SLUG_TAKEN`；未满足发布字段：`422 VALIDATION_FAILED`；草稿引用缺文件：`422 MEDIA_NOT_READY`。
- 为了与当前 DD 的 24 表模型保持一致，首版**不宣称支持跨任意请求的通用持久化 Idempotency-Key**；发布幂等由修订号和数据库事务保证。网络超时后前端先重新 GET 管理文章详情核实是否已发布，再决定重试。
- 修改已发布文章的 `comments_enabled`、标题、Slug、封面、SEO、分类和标签，也必须通过此发布接口才能对外生效。

### 5.4 撤回、删除、恢复

- `POST /admin/articles/{id}/withdraw`：PUBLISHED → WITHDRAWN，`200`；再次撤回可返回当前状态（无变化），并记录必要审计。
- `DELETE /admin/articles/{id}`：DRAFT/PUBLISHED/WITHDRAWN → TRASHED，`204`；不物理删除历史评论和图片。若幂等重复删除且有可追溯文章，可仍返回 `204`。
- `POST /admin/articles/{id}/restore`：TRASHED → WITHDRAWN（曾发布过）或 DRAFT（从未发布），`200`；**不会自动上线**。
- 所有公开文章列表、搜索、RSS、Sitemap、旧 Slug 查询与原评论必须检查 `articles.status=PUBLISHED`。

### 5.5 评论 DTO 与行为

**发布顶层评论：** `POST /articles/{article_id}/comments`，请求 `{ "body_text":"文章很有帮助，谢谢！" }`，成功 `201`，`data` 为新 `Comment`。

**回复指定评论：** `POST /comments/{comment_id}/replies`，请求 `{ "body_text":"@楼上 我也遇到这个问题" }`；Go 按父评论查明同一篇文章并计算 `root_id`；禁止跨文章、不存在/已删除/隐藏父项的回复（默认返回 404 或业务冲突）。

**公开 `Comment` 响应示例：**

```json
{
  "id":"a0000000-0000-4000-8000-000000000001",
  "article_id":"c0000000-0000-4000-8000-000000000001",
  "parent_id":null,
  "root_id":null,
  "body_text":"文章很有帮助，谢谢！",
  "status":"VISIBLE",
  "author":{"id":"b0000000-0000-4000-8000-000000000001","nickname":"访客","avatar_url":null,"is_blog_owner":false},
  "created_at":"2026-10-22T11:00:00Z",
  "reply_count":3,
  "can_delete":false
}
```

- **建议默认长度：** 1–2000 个 Unicode 字符（去除无意义空白），纯文本 + 换行 + Emoji + 安全链接。API 不接收任意评论 HTML/图片。
- **根评论分页：** `/articles/{article_id}/comments?page=1&page_size=20&sort=latest`，只对 `parent_id IS NULL` 分页。每条可提供最多 2 条回复预览及 `reply_count`，查看更多通过 `/comments/{root_id}/replies?page=...&page_size=20`。
- 评论删除后的占位：`status=DELETED_AUTHOR/DELETED_ADMIN`、`body_text=null`，前台显示“该评论已删除”；若管理员隐藏，则 `body_text=null`，不得把原文泄漏在公开响应的隐藏字段。
- 管理员隐藏父评论时，首版允许保留子回复结构，但**不显示隐藏父内容**；被隐藏评论不能继续回复。
- 公共回复和列表必须确认所属文章仍 PUBLISHED；文章关闭评论后仍可读取历史评论，任何新增/回复均 `422 COMMENT_CLOSED`。
- `can_delete` 仅是 UI 提示；真正删除必须由 Go 根据当前 Session 中的用户 ID 再次核对所有权。
- 用户删除/管理员删除是墓碑更新，不执行对子回复的 SQL 级联删除。
- 管理员隐藏/恢复操作返回 `200`，并记录审计；删除 `204`，不可恢复正文（与“隐藏可恢复”不同）。

### 5.6 注册、验证、登录与密码重置

**注册：** `POST /auth/register`

```json
{"email":"user@example.com","nickname":"测试用户","password":"example-not-a-real-password"}
```

成功 `201`：`{ "data": {"registration_status":"PENDING_EMAIL_VERIFICATION","email_delivery":"accepted"} }`；`accepted` 仅表示 SMTP 服务接受发送请求，不保证进入收件箱。如邮件服务暂不可用但**注册记录已经落库**，返回相同 `201`、`email_delivery="unavailable"`，前端明确提示稍后通过重发入口请求，避免客户端盲目再次注册。如果无法创建账号则按错误响应。已注册邮箱按当前产品规则 `409 EMAIL_ALREADY_USED`（公开注册存在一定邮箱可枚举风险，需限流与防刷）。邮箱密码不会进入日志。注册完成后**不自动登录**。

**重新发送验证：** `POST /auth/resend-verification` `{ "email":"user@example.com" }` → `202` 通用文案，存在与不存在邮箱尽量相同，限流、防枚举；请求成功不保证实际送达，不返回 Token。对已存在未验证邮箱，作废旧令牌再发新令牌。

**验证邮箱：** `POST /auth/verify-email` `{ "token":"..." }` → `200`，返回 `email_verified:true`；Token 仅一次有效。邮件链接落到 Nuxt `/auth/verify?token=...`，页面消费成功后移除地址栏敏感查询参数；响应设置 `Referrer-Policy: no-referrer`，避免将令牌外泄。

**登录：** `POST /auth/login` `{ "email":"...","password":"..." }` → `200`，`Set-Cookie` 安全 Session，响应 `{ "data": {"user": {"id":"...","nickname":"...","role":"USER","email_verified":true}}}`。邮箱不存在/密码错误统一 `401 INVALID_CREDENTIALS`；未验证账号返回 `403 EMAIL_NOT_VERIFIED`；禁用账号拒绝登录。实现常量时间级敏感处理与登录限流。

**查询自己：** `GET /auth/me`，包含本人的邮箱、昵称、头像、角色、验证和账号状态；**公开评论中的 `author` 永远不带邮箱**。

**退出登录：** `POST /auth/logout` 删除当前会话并清空 Cookie，`204`；`POST /auth/logout-all` 撤销本账号所有会话，`204`。密码成功重置/管理员禁用账户后已登录 Session 必须撤销。

**找回密码：** `POST /auth/forgot-password` `{ "email":"..." }` → `202` 统一“若邮箱可用将尝试发送”类文案，避免账号存在性枚举。后端邮件系统故障须记录明确可操作的运维事件，且页面不得谎称“必定已送达”；限流下允许重试。

**密码重置：** `POST /auth/reset-password` `{ "token":"...","new_password":"..." }` → `200`；一次性消费 Token、修改哈希、撤销旧 Session 与未消费的相关令牌在一个事务中完成。Token 失效错误码 `TOKEN_INVALID_OR_EXPIRED`，不披露账号信息。

**建议默认输入界限：** 昵称 1–40 字符；邮箱最多 254 字符；密码 12–128 字符（允许粘贴密码管理器生成的强密码，不要求重复字符种类）。邮件验证 TTL 24h、重置 TTL 30min（可配置）。本节密码字符串仅为**示例占位**，不得用于生产账号。

**修改本人信息：** `PATCH /me/profile` 支持 `nickname`、`bio`；`POST /me/change-password` 需 `current_password` + `new_password`，成功撤销其他会话并轮换当前会话或要求重新登录（二者实施前固定一种，推荐要求重新登录，前端明确提示）；`POST /me/change-email/request` 请求 `{ "new_email":"..." }`，`POST /me/change-email/confirm` 必须在有效 Session 和 CSRF 保护下消费与当前申请匹配的 Token；如果用户通过邮件链接在未登录浏览器打开确认页，先要求登录再确认，确保链接单独泄露不足以接管账户。

### 5.7 媒体读取与上传

`POST /admin/media`：管理员用 `multipart/form-data` 提交 `file` 与可选 `usage_kind` (`ARTICLE` / `SITE` / `PAGE`)。返回 `201`：

```json
{
  "data": {
    "id":"f0000000-0000-4000-8000-000000000001",
    "url":"/media/f0000000-0000-4000-8000-000000000001/file",
    "mime_type":"image/webp",
    "size_bytes":34051,
    "width":1280,
    "height":720,
    "visibility":"PRIVATE_UNTIL_REFERENCED"
  }
}
```

`POST /me/avatar`：普通用户上传 `file`（仅头像），服务器在校验和写盘完成后将自己 `user_profiles.avatar_media_id` 指向新图片，返回 `200` 新 `avatar_url`。用户不能通过此入口改变站点 Logo/文章媒体。

**建议默认文件限制：** JPEG/PNG/WebP/GIF；文章及站点图片 `≤10 MiB`、`≤20 MP`，头像 `≤2 MiB` 且尺寸/像素上限单独配置；拒绝 SVG、HTML 和脚本文件。使用内容嗅探、解码及尺寸检查，防止文件名欺骗、压缩炸弹；限制整体请求大小，校验存储剩余空间。

`GET|HEAD /media/{id}/file`：Go 从数据库识别 ID 和公开引用权限：

- 被**已发布**文章、公开站点/关于页或可公开头像引用的图片：允许匿名获取，设置短时 `Cache-Control: public` 与内容类型安全头。
- 仅出现在**草稿**的 ARTICLE 图片：只有管理员可读，`private, no-store`。
- 没有有效公开引用且普通用户无权查看的图片：返回 404，而非直接访问裸目录。
- 管理员列表 `/admin/media` 可查看未被引用的资产元信息；管理员预览仍须鉴权。
- 用 `state=READY` 只是文件准备完成，不等于“公开”。
- 原始上传文件不在 NPM 中配置为无需认证的 `/uploads` 静态目录。

`DELETE /admin/media/{id}`：存在任何草稿、公开版本、用户头像、站点头像/Logo、关于页等引用时返回 `409 RESOURCE_IN_USE` 并可展示**不包含私人内容**的引用数量；无引用才走安全删除状态机（PENDING_DELETE → 删除文件 → DELETED）。文件系统故障的可重试补偿由 Go 实施，不能误删正在被并发引用的资源。

**Markdown 引用规则：** 文章内使用 `/media/{uuid}/file` 地址，保存时识别并记录本站媒体引用；发布前校验引用都可用并归属合法。允许外部图片链接，但不能把外部 URL 当作本地媒体引用。Markdown 任意 HTML 中插入的 URL 不应绕过校验或图像权限规则。

### 5.8 管理数据、配置、分类、标签

- `/admin/overview` 只返回 `article_count`、`user_count`、`comment_count`、`hidden_comment_count`、`recent_events` 等摘要；**不出现“待审核评论数量”**。
- `/admin/users` 支持 `q,status,verified,page,page_size`；仅管理员可见邮箱等资料。`disable` 禁止直接禁用自身及最后一个 ADMIN，默认只操作普通用户。禁用后立刻撤销 Session，不自动删除历史评论。
- 分类/标签：`name`、`slug`、`sort_order`、`is_active`；重复 Slug `409`；存在草稿或公开版本关联时删除 `409 RESOURCE_IN_USE`，先迁移或停用。
- `/admin/site` 接受 `site_title,site_description,profile_name,profile_bio,profile_avatar_media_id,logo_media_id,favicon_media_id,contact_email,default_seo_title,default_seo_description`；不可写入 SMTP 密码、数据库 URL 或 Cookie 密钥。
- `PUT /admin/site/social-links`：数组全量覆盖 `[{id?,icon_key,label,url,sort_order,is_enabled}]`，`icon_key` 来自允许列表（如 github、mail、link）；外链只允许 `https:` 与受限 `mailto:`，禁止 `javascript:`、`data:`。
- `PUT /admin/site/navigation-links`：数组全量覆盖 `[{id?,label,link_type,url,sort_order,is_enabled}]`；内部路由走白名单 / 安全路径解析，外部 HTTPS 链接设置 `rel="noopener noreferrer"`。
- `PUT /admin/pages/about`：`{title,body_markdown}`，在数据库事务中更新 Markdown 和 `static_page_media` 引用；**显式保存即公开**，不适用文章的独立草稿/发布快照规则。
- 所有高风险变更必须服务端再次校验 ADMIN 并写 `admin_audit_logs`，审计 JSON 严禁保存密码、会话、SMTP 凭据、重置令牌等敏感材料。

## 6. 前端与接口的完整交互流程

### 6.1 访客 → 注册 → 验证 → 评论

```text
GET /api/v1/auth/csrf
POST /api/v1/auth/register         (昵称/邮箱/密码)
  → Nuxt 告知查看邮件
用户点击邮箱链接打开 Nuxt /auth/verify?token=...
用户确认 → POST /api/v1/auth/verify-email
POST /api/v1/auth/login            (设置 Session Cookie)
GET  /api/v1/articles/{slug}
POST /api/v1/articles/{article_id}/comments  (含 CSRF Header)
  → 201，评论直接出现在公开评论区；无待审核状态
```

### 6.2 管理员新建 → 自动保存 → 发布 → 继续编辑

```text
POST /api/v1/admin/articles       → {article_id, draft.revision=1}
GET  /api/v1/admin/articles/{id}  → draft/current_publication
PUT  /api/v1/admin/articles/{id}/draft  (expected_revision=1 + 所有草稿字段)
PUT  /api/v1/admin/articles/{id}/draft  (expected_revision=2 + 所有草稿字段)
POST /api/v1/admin/articles/{id}/publish  (expected_draft_revision=3)
GET  /api/v1/articles/{slug}       → 当前正式内容
PUT  /api/v1/admin/articles/{id}/draft   (revision=3，修改内容)
GET  /api/v1/articles/{slug}       → 仍返回旧正式内容
POST /api/v1/admin/articles/{id}/publish  (expected_draft_revision=4)
GET  /api/v1/articles/{slug}       → 新正式内容
```

### 6.3 隐藏违规评论 → 封禁账号

```text
GET  /api/v1/admin/comments?status=VISIBLE
POST /api/v1/admin/comments/{id}/hide
  → 原文不再公开，历史回复结构按规则保留
POST /api/v1/admin/users/{id}/disable
  → 撤销 Session + 以后不可登录和发表评论
```

### 6.4 上传草稿图片的可见性

```text
POST /api/v1/admin/media  → {id,url}
GET  /media/{id}/file     (游客) → 404
GET  /media/{id}/file     (管理员) → 200 private/no-store
PUT  /api/v1/admin/articles/{id}/draft   → 记录草稿图片引用
POST /api/v1/admin/articles/{id}/publish → 原子注册公开引用
GET  /media/{id}/file     (游客) → 200（文章仍 PUBLISHED）
POST /api/v1/admin/articles/{id}/withdraw
GET  /media/{id}/file     (游客) → 404（若无其他公开引用）
```

## 7. 限流、防滥用和安全基线

以下是**建议默认阈值，需要实施时按真实流量、NPM 代理链与 SMTP 额度校准**，不是已确认的生产 SLA。

| 场景 | 限制建议（组合 IP + 账号等维度） | 超出时 |
|---|---|---|
| 登录 | 单 IP 10 次/10 分钟；同邮箱 5 次/10 分钟失败 | 429；不反馈邮箱存在性 |
| 注册 | 单 IP 3 次/小时 | 429，可额外启用人机验证 |
| 发送验证邮件 | 同账号/邮箱 1 次/60 秒，5 次/天 | 429；泛化提示 |
| 找回密码邮件 | 同邮箱 1 次/60 秒，5 次/天 | 429；泛化提示 |
| 发表评论/回复 | 同用户 5 次/分钟，20 次/小时 | 429；可额外限 IP |
| 头像上传 | 同用户 10 次/小时 | 429 |
| 管理图片上传 | 单次 10 MiB，按服务容量设置总请求限速 | 413 / 429 |
| 搜索 | 单 IP 60 次/分钟，最多 100 字符关键词 | 429 / 422 |

- 后端统一规范化邮箱，但**不能**对任意邮箱模拟 Gmail 点号/加号特殊规则。
- 防 SQL 注入：参数化 SQL / sqlc，禁止拼接用户输入的查询片段。
- 防 XSS：Markdown 渲染白名单、评论文本安全转义、链接安全协议、媒体 MIME 验证。
- 防 CSRF：每一个产生状态变更的 Cookie 认证接口都要校验令牌和 Origin；特别是 `/auth/login`（避免登录 CSRF）。
- 管理端 Server 端强制权限判断；不能仅由 Nuxt 路由守卫判断。
- 日志不包含明文 Cookie、密码、邮件 Token、完整重置链接；使用 request_id 串联，失败时返回稳定业务错误码。
- 防爬与浏览量：`POST /articles/{id}/views` 不从 SSR 渲染自动触发；仅在客户端有效阅读后上报。Go 在短窗口内按匿名浏览器标识等做**近似**去重，避免把每次刷新或爬虫当成独立阅读；不以 IP 身份或指纹构建永久追踪库。
- 搜索初期用 PostgreSQL；中文与混合语言检索效果必须用实际文章验收，不能假设英文全文分词可直接满足中文搜索。短词使用受控降级/提示策略。

## 8. 需要补充的响应细则及状态边界

| 情况 | 推荐响应 | 前端行为 |
|---|---|---|
| 用户未登录执行评论 | 401 `AUTH_REQUIRED` | 显示登录入口 |
| 邮箱未验证（如 Session 仍存在） | 403 `EMAIL_NOT_VERIFIED` | 提示验证邮箱 |
| 会话失效 | 401 `SESSION_EXPIRED` | 清理客户端用户态，跳转登录 |
| 草稿编辑版本落后 | 409 `DRAFT_REVISION_CONFLICT` | 保留本地编辑内容，提供冲突比较 |
| 草稿标题/正文为空而点击发布 | 422 `VALIDATION_FAILED` | 定位未完成字段 |
| 发布时 Slug 已占用 | 409 `SLUG_TAKEN` | 修改候选 Slug 后重试 |
| 用户在文章关闭评论后提交 | 422 `COMMENT_CLOSED` | 保留输入文字并提示关闭 |
| 非本人删除评论 | 403 `FORBIDDEN` 或不披露型 404 | 不执行删除 |
| 隐藏/被删评论遭到回复 | 404 / 409 | 提示不能回复此评论 |
| 媒体被文章或头像使用 | 409 `RESOURCE_IN_USE` | 显示引用情况，不执行删除 |
| 游客访问草稿图片 | 404 `NOT_FOUND` | 不暴露存在性 |
| 邮件令牌已用/过期 | 422 `TOKEN_INVALID_OR_EXPIRED` | 提示重新申请 |
| 评论/注册过于频繁 | 429 `RATE_LIMITED` | 遵守 `Retry-After` |
| 已撤回文章被旧 Slug 访问 | 404 `NOT_FOUND` | 不跳转到撤回前内容 |

## 9. 接口契约测试清单（设计期，待实施）

| 编号 | 测试与预期 |
|---|---|
| API-01 | 公开文章列表不含草稿/撤回/回收站正文 |
| API-02 | 旧 Slug 对公开文章返回 canonical 路径，Nuxt 最终返回 301 |
| API-03 | 未发布文章和私有草稿图片对游客不可见且不可探测 |
| API-04 | 普通用户调用任意 `/admin/*` 返回 403 |
| API-05 | 注册只能得到 USER，重复规范化邮箱不能创建第二账号 |
| API-06 | 邮箱验证令牌过期/重复消费不能再次成功 |
| API-07 | 未验证账号不能登录后发表评论 |
| API-08 | 密码重置消耗一次性令牌并撤销旧 Session |
| API-09 | Cookie 安全属性、Session 过期、退出与禁用即时生效 |
| API-10 | 所有 Cookie 认证写请求缺 CSRF / 异源 Origin 时被拒绝 |
| API-11 | 两窗口保存草稿，旧 Revision 返回 409 且新数据保留 |
| API-12 | 已发布文章保存草稿后，公开正文/SEO/分类/标签/封面不变 |
| API-13 | 发布成功一次性切换全部公开内容；失败全回滚 |
| API-14 | 重复发布同修订不会重复提升发布版本（仍 PUBLISHED 时） |
| API-15 | 撤回后手动再发布可重新公开，不被错误判为无变化 |
| API-16 | 修改 Slug 保留原地址映射；撤回后旧链接不泄露文章 |
| API-17 | 登录验证用户评论立即可查；不存在审核队列 |
| API-18 | 跨文章回复和隐藏/删除父评论回复被拒绝 |
| API-19 | 删除带回复评论仍保留树与删除占位，正文不公开 |
| API-20 | 关闭评论后历史可见，但新增顶层和回复均被拒绝 |
| API-21 | 用户中心与公开评论不泄漏私人邮箱、Token 和用户 Session |
| API-22 | 上传图片严格校验真实格式/像素/大小；拒绝恶意文件 |
| API-23 | 草稿媒体只允许管理员查看，发布后在公开期间可访问 |
| API-24 | 删除被引用媒体返回 409，完整恢复备份后仍能正常读取 |
| API-25 | 用户头像上传与媒体管理权限隔离 |
| API-26 | `sitemap.xml`、RSS、搜索结果只包含当前已发布文章 |
| API-27 | 中文短关键词、混合检索及分页经实际样本验证 |
| API-28 | 同站点 URL、HTTPS、反代可信来源 IP/错误日志保护正常 |
| API-29 | API 超限返回明确 429 与 Retry-After |
| API-30 | 数据库迁移/邮件不可用/磁盘空间不足时，错误响应明确且不留下不一致数据 |

## 10. OpenAPI 产物与 Agent 开发要求

此轮交付为**人类可审核的 API 设计文件**，尚未编写 OpenAPI YAML 或 Go 处理器。进入实施准备时建议：

1. 将本文所有路由和 DTO 转换为一个**单一权威 OpenAPI 3.1** 文件，避免前后端各写一份。
2. 在 OpenAPI 中列出请求体字段（必填/可空）、响应 Schema、状态码、安全 Cookie + CSRF 描述、请求与响应样例。
3. Go 后端按 OpenAPI 实现，Nuxt 端基于它生成 TypeScript API 类型与客户端封装；统一测试契约而非复制 DTO。
4. Agent 不得在没有设计变更记录时增删字段、改变返回码、跳过鉴权、把草稿返回到公开接口。
5. 每个开发阶段必须提交交付记录：变更项、迁移、测试、剩余风险、当前版本、下一步计划；接入 GitHub 后每次正式版本修改都需提交并推送，密钥和用户数据禁止进入仓库。

## 11. 与数据库设计的映射校验

| API 域 | 数据源（源自 DD 24 表） | 关键约束 |
|---|---|---|
| `auth/*`、`me/*` | `users`、`user_profiles`、`auth_sessions`、`auth_tokens`、`auth_rate_windows` | 邮箱唯一、一次性令牌、Session 摘要 |
| 公开文章和搜索 | `articles`、`article_publications`、`article_slugs`、`article_publication_tags`、`article_metrics` | 只读 PUBLISHED 的公开快照 |
| 后台文章 | `article_drafts`、`article_draft_tags`、`article_draft_media` 与公开映射 | 修订号 CAS、发布事务 |
| 分类标签 | `categories`、`tags`、草稿及公开标签映射 | 分类标签删除引用保护 |
| 评论 | `comments` | article ID 稳定、父子同文章、墓碑 |
| 本地图片 | `media_assets`、`article_draft_media`、`article_publication_media`、`static_page_media` | 公开引用判断、删除保护 |
| 网站与后台 | `site_settings`、`site_social_links`、`site_navigation_links`、`static_pages`、`admin_audit_logs` | 公共字段最小化与审计 |

## 12. 当前待验证/待评审事项（非新增产品需求）

1. **API 默认阈值**（限流、上传体积、Session 14 天、评论长度）应在部署配置中统一，并在真实环境测试。
2. 邮件发送机制当前无需额外常驻队列；SMTP 故障、网络超时、一次性令牌与用户提示需在真实集成测试中验证。
3. 媒体公开可见性与缓存撤回的实际延迟必须进行浏览器和 NPM 端到端测试；受控服务端缓存不能泄漏曾仅在草稿使用的媒体。
4. 自动保存接口使用全量 DTO，Nuxt 需要节流/串行队列；409 冲突 UI 需在详细 UI 阶段指定操作方案。
5. PostgreSQL 对中文全文检索的能力需要样本测试；如质量不足，优先做适度 SQL/索引优化，而不是直接引入新服务。
6. Nuxt 端生成 Sitemap / RSS 的动态缓存刷新机制、旧 Slug 的真正 301、公共/私有响应隔离，需要端到端验收。
7. **密码修改后的当前登录状态**（重新登录或安全轮换）属于实现细节，默认建议强制重新登录，具体需在 OpenAPI 阶段冻结。
8. 管理员删除用户评论与物理删除媒体的风险需覆盖并发测试；关联业务优先防误删。

## 13. 阶段结论与里程碑

- 本文给出了可供 Go 与 Nuxt 团队共同评审的路径清单、权限、统一响应结构、主要 DTO、关键事务 API 与异常码。
- 与已有 24 表逻辑设计保持对齐，**未新增数据库表**；没有实现任何可执行 API。
- 下一个设计阶段是《前后台 UI / UX 详细设计》，之后编制《分阶段开发实施计划》；两项完成后可初始化仓库并正式编码。
- 真实安全性、性能和一致性只有运行起来经过测试才能确认；本文的审查不等同于测试通过。

## 附录：核对与参考

- 既有设计基线：`XiaoWork_Blog_V1.0_PRD.md`、`XiaoWork_Blog_V1.0_Architecture_Design.md`、`XiaoWork_Blog_V1.0_Database_Design.md`。
- OWASP CSRF Prevention Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html
- OWASP Session Management Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- MDN If-Match / 412 文档（作为替代方案参考；本轮实际选择 JSON revision + 409）：https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Status/412

