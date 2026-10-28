# 代码审查处置 todolist（2026-10-28）

依据：`代码审查报告-20261028.md`。本轮先做二次取证（6 路子代理 + 本机基线复跑），再按清单处置。
口径已确认：① 统计排除 status=8、保留 7；② `users.phone` 与班次唯一索引两项都走 AutoMigrate 表结构演进；③ 范围 = P1 + P2 全量一次做完。
约束：不做 git 提交与推送；改动全程带函数级注释；文档与台账随代码同步。

## 取证结论摘要（报告真实性判定）

- 14 条 P1：事实全部成立，行号误差 ≤3。
- §3 证伪 6 条：判定全部正确，无「真问题被误判为误报」；报告遗留的 2 条疑似项已静态判定——`writeContentNodes` 无深度上限=成立（需超大嵌套体，风险低）、`safeContentURL` 放行 `//evil.com`=不成立（已拒所有 `//` 前缀）。
- §4 台账重叠 12 项：编号与口径全部对得上。
- P2 抽样 20 条：全部成立，5 处表述需修正（见 I 组勘误）。
- 基线复跑：`go vet` 零告警✓、admin `vue-tsc` 零错误✓、Go 文件全 LF✓、文件数（249/146/67）✓。
- 基线两处不实：`go test ./...` 有 1 条红（且是报告未发现的真实缺陷）；admin `vitest` 整轮 143/144（1 条超时 flaky，单跑通过）。
- 报告范围描述不实：「11 个未提交的文档修订」——工作树干净，docs 最后提交为 `ff42642`（10-27 23:15）。

---

## A. 恢复绿色基线（报告漏判的真缺陷）

- [x] A1. 修 `gateway/main.go:687-707` exports 私有段判定：`filepath.Clean` 在 Windows 返回 `\exports\...`，前缀匹配失败，`/uploads/images/../exports/task.csv` 实测 200 泄露导出内容（含用户数据）。改为 `path.Clean` 归一 + 大小写不敏感判定，归一后的路径回写 `c.Request.URL.Path` 再分派 seed/attachment；补 `./exports`、`Exports`（大写）、`images/../exports`、`seed/../` 用例。完成后 `TestRegisterUploadsBlocksExports` 转绿。
- [x] A2. 查清 `src/admin/src/api/__tests__/request.spec.ts`「业务码 11001 触发收口」用例（单跑 1743ms，整轮 worker 抢占即超时）的慢因并修稳（fake timers 或显式 timeout + 拆掉真实等待），保证整轮 vitest 可重复全绿。

## B. 资金与统计口径

- [x] B1. 统计排除已退款：`services/order/service/order.go:2020 / 2066 / 2224` 三处 `status NOT IN (1,6,8)`；日均为派生值随之收敛；同步 `src/admin/src/views/statistics/index.vue:100` 注释与 extra 文案、`views/system/statistics.vue` 口径说明、《04-业务需求》统计口径章节。
- [x] B2. 退款汇算事务句柄统一：`services/order/service/refund.go` 把 `tx *gorm.DB` 作参数贯穿 `calcRefundOrder / refundedStat / refundedMoneyStat / resolveFreight / getFreightPolicy / getCompletedRefundHours`，事务闭包内禁止再取 `s.db.GetDb()`；`ApplyRefund`（:165 起）与 `AdminCreateRefund`（:296/:323）两条同型路径一并改；补并发退款回归用例。
- [x] B3. 班次原子化：`services/order/model/shift.go` 新增 `ActiveFlag *int8`（开班=1、关班=NULL）+ 复合唯一索引 `uniqueIndex:uq_shift_active`（community_id, operator_id, active_flag）；`shift.go:61-84` 改为直接 Create 并捕获 duplicate 转「已有进行中班次」；`EndShift` 关班时置 NULL；配套一次性清理脚本（存量双开班次只保留最新一条），脚本执行前单独确认。
- [x] B4. 支付冲突分支补渠道关单：`services/payment/service/payment.go:232-240` 事务提交后对被关旧单发起渠道关单/撤销，失败仅记 `NotifyKindClose` 流水不阻塞下单；同时把该路径与 `ReconcileEnabled` 闸门的耦合口径写清（关渠道动作不受对账开关抑制）。
- [x] B5. 迟到付款回溯：`services/payment/service/scheduler.go:372` 拆分「在途单最大时长」与「已关单回溯窗口」两个配置，已关单按关闭时间分档低频回溯 + `reconcile_count` 上限控成本；注意取证补充——`withinReconcileWindow`（:1194）按 `created_at` 判定，pay_type 2/5/6 实际窗口约 30 分钟（min(120min, OrderTimeout=1800s)），需一并纳入配置。
- [x] B6. 异常流水清理加 `handled = 1`（或仅清 `result=success`）：`scheduler.go:812-821`；未销账流水不过期，清理前对 `handled=0` 存量再发一次升级告警。
- [x] B7. `codepay` 金额实收回填：`services/payment/service/codepay.go` 的 `codePayResult`（:42）补 `Amount`，`resolveCodePayByQuery`（:341 已握有渠道 `res.Amount`）回填真实值，恢复 `payment.go:1235` 金额二次比对的判定能力；顺带补 `writeCallbackLog` 的 NotifyKind，使付款码成功有流水（当前 :341 调用未传 kind，:1333 直接 return 导致零流水）。

## C. 越权与凭据

- [x] C1. 员工列表不再下发口令哈希：`services/user/service/admin.go:906` 的 `ToAdminInfo` 去掉 `Password` 赋值；`gateway/handler/admin.go:475` 的 `ListAdmins` 改走 `toAdminVO`（:5534）白名单；补用例断言列表响应无 `password` 字段（登录校验走模型层，不经 proto，需确认无消费方依赖）。
  - 取证纠正：清单原文假设「登录校验走模型层、不经 proto」，实测 `gateway/handler/admin.go:139` 正是拿 proto 的 `AdminInfo.password` 做 bcrypt 比对——单纯删赋值会让后台登录全废。故按「缺陷改在归属层」处理：新增 `user.VerifyAdminCredential` RPC（比对在 user 服务内完成，只回 `password_valid` 布尔 + 不含摘要的账号信息，NotFound 仍表示账号不存在以保留锁定计数区分），`AdminInfo.password` 标 `[deprecated]`，网关 `Login` 改道并移除 `bcrypt` 依赖。
  - `toAdminVO` 顺带补 `roles` 白名单子集（id/name/code/community_id，不透出 permissions）：列表原本直接透传 proto 才带着它，改走白名单后不补会让前端「本社区角色」列与编辑回显 `role_ids` 失效。
  - 用例：`gateway/handler/admin_credential_test.go` 三条离线回归（桩故意回填摘要，断言登录与列表响应全文无 `$2a$`；口令错与账号不存在同码；白名单不漏前端依赖字段且角色行不带权限明细）。
- [x] C2. C 端公开详情收口：`gateway/handler/product.go:101` 的 `community_id` 改取登录/渠道语境而非 query（缺省 0 即绕过 `scope.go:38` 的归属校验）；`services/product/service/product.go:346` 默认 `status <> 2`，管理端显式放开；详情路径传 `RequireSellable: true`；补网关匿名用例（跨社区、已删除、下架商品均不得返回价格与富文本 content）。台账 82（`scope<=0` 只告警）登记状态同步更新。
  - 取证纠正 1（社区语境来源）：清单假设「改取登录/渠道语境」在本路由上不成立——`/api/v1/products/:id` 只注册在公开组（`gateway/main.go`），不经 auth 中间件，请求头令牌不会被解析成社区语境。实改为**必显式携带 `?community_id>0`，否则网关直接 `10001` 且不触达服务**；C 端四处调用点配套补齐当前浏览社区（新增 `src/client/src/utils/community.ts` 的 `ensureCommunity`，`detail.vue` onLoad 先兜底、`sku-selector.vue` 新增 `communityId` prop、`cart.vue`/`category.vue`/`confirm.vue` 传参）。
  - 取证纠正 2（不传 `RequireSellable`）：详情是浏览入口，下架商品必须能打开页面（显示「已下架」）而不是报错，硬可售会打断正常浏览。改为 `public_view` 脱敏口径：非上架态抹价格/划线价/库存/富文本正文，`cost_price`/`safety_stock` 任何状态都抹。
  - 取证补充（`sku_code`）：初版把 `sku_code` 一起抹了，grep 到收银台 `OrderTab.vue:251/766` 用 `spec_name || sku_code` 兜底规格名，抹掉会让 POS 显示空白 → 从脱敏字段中移除，注释记录该取舍。
  - 层次归位：脱敏策略先误写在 `services/product/server.go`（package main）+ 薄包装，已迁到归属层 `services/product/service/scope.go`（`RedactPublicProductDetail`/`RedactPublicSku` 导出函数），出口只声明意图。
  - 交付：proto 新增 `include_deleted`/`public_view`（GetProductReq/ListSkusReq/GetSkuByIdReq，已 `buf generate`）；服务层 `GetProduct(ctx,id,includeDeleted)` + `ProductStatusDeleted` 常量；管理端详情 `include_deleted=true`/`public_view=false`；扫码出口 `LookupBarcode` 无条件只抹经营字段（收银台与 C 端共用同一 handler，价格与库存必须保留）。
  - 用例：`services/product/service/public_view_test.go` 4 例、`gateway/handler/product_public_view_test.go` 4 例（含管理端反向口径），`go test ./...` 全绿。
  - 文档同步：`09-gRPC接口设计/03-product.proto.md`、`06-接口设计/02-C端接口.md`（顺带修正同表商品列表 `status` 透传的旧描述——早先 P1-3 已改为服务端强制 1）、`06-接口设计/03-后台管理接口.md`、`04-业务需求/05-权限与数据隔离.md`、`05-数据库设计/03-商品服务表结构.md`、`11-开发计划.md` 台账 82（本项范围收窄为交易入口）。
- [x] C3. `services/user/model/user.go:15` 的 `Phone` 由 `string ... unique` 改 `*string`（NULL 不参与唯一冲突，与同表 `AlipayUserID`/`Unionid` 一致）；仅在真正绑定时写非空值；核查所有 `u.Phone` 读写分支与查询条件不被 nil 破坏；AutoMigrate 演进后实测。
  - 实测取证（测试库 `market.users`，事务内插入后 `ROLLBACK`，无残留）：列本身已是 `varchar(20) YES / UNI / DEFAULT NULL`，所以**本项不需要任何表结构演进**（J3 里的「C3 改可空」实际不存在 ALTER）；缺陷纯粹在写入侧。两条 `phone=''` 插入即 `ERROR 1062 Duplicate entry '' for key 'users.uni_users_phone'`，两条 `phone IS NULL` 插入成功——与报告结论一致。
  - 交付：`Phone *string`（tag 保持 `unique`，符合单列唯一用 `unique` 的约定）+ 模型侧两个唯一入口 `model.NewPhone(phone)`（空串→NULL）与 `(*User).PhoneValue()`（NULL→空串，nil 接收者安全）；读写点全部改走入口：`service/user.go` 登录 upsert（`orPtrValue` 保留「req 不带手机号不冲掉存量绑定」的原语义）、`CreateMember` 注册、履约人员列表、`ToUserInfo` 出口、`user/server.go` 员工绑定会员回显、`product/service/section_bootstrap.go` 种子导入。
  - 顺带堵住空串查询旁路：`DataImportService.userIDByPhone` 与 users 种子段的幂等查询在 phone 为空时直接拒绝/标记失败——空串在查询里是具体值，不带手机号就查会把历史遗留空串行当成命中用户。
  - 用例：`services/user/model/user_phone_test.go` 2 例（离线）。
  - 文档同步：`05-数据库设计/02-用户服务表结构.md` users.phone 行（NULL 语义与读写入口）。
- [x] C4. 口令下限 6→10：`gateway/handler/admin.go:821` 及创建/改密入口、admin 前端表单校验、《06-接口设计》相关字段说明同步。
  - 取证纠正（关键）：报告把下限写成「在 `decryptPwdField` 里判」，但该函数**同时被登录与 5 个写入口共用**（`admin.go:115` 登录也调它）。若在传输层按新下限拒绝，存量 6-9 位口令账号会直接登不进去——把「提强度」做成了「锁号事故」。实际分层落地：传输层只判上限（190 字节，RSA-2048/OAEP-SHA256 单块明文上限，防设出加密侧解不出的口令）；下限 10 由策略层 `validateNewPassword` 在 5 个写入口解密后逐处判定；归属层 `hashPassword` 再兜一次（防 RPC 直连绕网关）。存量短口令不强制重置，下次改密时被要求满足新下限。
  - 交付：新增单一口径包 `common/pwdpolicy`（`MinLength=10`/`MaxLength=190`/`TooShort`/`TooLong`/`Message()`），网关 binding 标签（`min=6`→`min=10`，5 处）、`maxRSAPasswordLen = pwdpolicy.MaxLength`、`services/user/service/admin.go` 兜底（删除两处重复 `len<6`）、`cmd/market-admin` 的 `MARKET_ADMIN_PASSWORD` 下限（原 8）全部引用同一包；前端新增 `src/admin/src/constants/security.ts`，`accounts.vue`/`community-form.vue`/`settings/profile.vue` 的占位与校验改走常量。
  - 边界说明：190 恰为 RSA-2048 + OAEP-SHA256 单块最大明文（256−2×32−2），加密通道无法构造越界口令，越界只在明文兼容通道可达——用例分别覆盖两条通道。
  - 用例：`gateway/handler/password_crypto_test.go` 的 `TestDecryptPwdFieldPlaintextLengthGuard`（重写为分层口径）+ 新增 `TestValidateNewPasswordPolicy`；`go build ./...`、`go vet ./...`、`gofmt -l`、`go test ./gateway/... ./services/user/... ./cmd/...` 全绿，`vue-tsc --noEmit` 无错。
  - 连带修改：e2e 造数口令低于 10 的 5 处上调（`08-admin-auth-rbac` Low@123456、`24-rbac-advanced` STAFF_PASS/CM_PASS、`73-payment-reliability` + `tests/helpers/api.ts` Ff12345678、`75-order-export-search` Exp1234567）；登录用例用的 `admin123`（演示种子，bcrypt 直写、不经策略）保持不动。
  - 文档同步：`06-接口设计/03-后台管理接口.md` 新增「口令长度下限」整段（分层原因、共用函数陷阱、存量不强制重置、`MARKET_ADMIN_PASSWORD` 与演示种子的例外）。
- [x] C5. 登出真正吊销 access：`common/jwt/jwt.go:88` access 签发写入 jti，`gateway/middleware/auth.go` 校验黑名单（登出双吊销，含 refresh 路径 `gateway/handler/auth.go:343`、`handler/admin.go:202`）；`jwt.go:205` `newJTI` 随机源失败不再返回空串（改为拒绝签发）。
  - 取证补充：`JWT_ACCESS_TOKEN_TTL` 默认 7200 秒（2 小时），报告说的「登出只是前端表演」成立——原先 access 完全不带 jti（`common/jwt/jwt_test.go` 里甚至有一条断言「access 不应携带 jti」锁死这个旧口径），鉴权链也没有任何吊销查验。
  - 交付：① 签发侧 `generateToken` 内部统一取 jti、`newJTI` 返回 `(string, error)`，随机源失败直接拒绝签发（不再退化成「签出一枚永远无法单独吊销的令牌」）；② 存储侧把 `refreshGuard` 泛化为 `tokenGuard`（文件 `refresh_guard*.go` → `token_guard*.go`，Redis 键 `refreshjti:` → `tokenjti:`，access/refresh 共用一个命名空间），并新增 `NewTokenGuard`/`RevocationCheck` 供启动装配；③ 鉴权侧 `gwMiddleware.SetTokenRevocationChecker(...)`（与 `SetPermissionResolver` 同注入模式，`WSAuthMiddleware` 复用 `AuthMiddleware` 因而自动覆盖 WS 握手）；④ 登出侧 `revokePresentedTokens` 一次吊销 access + refresh，access 从 `Authorization` 头取、兜底读 `market_admin_access` httpOnly cookie（只清 cookie 不读它就漏掉这一份），类型/用途不符跳过；⑤ `NewAuthHandler`/`NewAdminHandler` 改为接收同一份 guard，网关只构造一个实例。
  - 热路径取舍（性能）：每个鉴权请求多一次 Redis `EXISTS`；命令上限 1s → **500ms**（Redis 故障时最坏只加 500ms 而非 1s），查验失败告警 **30s CAS 节流**（逐请求刷屏会掩盖真实故障线索），fail-open 契约不变。
  - 明确边界（写进文档，非缺陷）：刷新不吊销上一枚 access（在途并发请求会莫名 401）；已建立的 WS 长连接要到下次握手才被拒；停用/删除管理员无「管理员 → 全部 jti」索引，仍靠实时权限解析与刷新时账号校验兜住。
  - 用例：`common/jwt/jwt_test.go`（`TestAccessTokenHasNoJTI` 反转为 `TestAccessTokenCarriesJTI`，含 access/refresh 不同键断言）、`gateway/middleware/auth_revocation_test.go` 4 例（命中拒绝 / 未命中放行且只查一次 / 未注入跳过 / 用途校验排在吊销之前）、`gateway/handler/token_guard_test.go` 新增 `TestPresentedAccessToken`（头/小写 bearer/cookie 兜底/优先级）与集成 `TestLogoutRevokesAccessAndRefresh`（需 `TEST_REDIS_PASSWORD`）。`go build ./...`、`go vet ./...`、`gofmt -l` 全清，`go test ./...` 21 个包全绿。
  - 文档同步：`07-基础设施/01-端口与认证.md`（小节改名 + 六条新口径 + 边界）、`07-基础设施/03-缓存存储与日志.md`（键名与文件改名）、`06-接口设计/02-C端接口.md` 与 `03-后台管理接口.md`（登出端点行为）。
  - 待办：docker 里跑的是旧二进制，e2e「登出后旧 access 立即 401」的实测需先交叉编译 + 重启容器（归入 K 组整体验收）。

## D. C 端 / 后台功能正确性

- [x] D1. 履约 tab 映射纠正：`src/client/src/pages/fulfillment/fulfillment.vue:155-158` 改 `{送货:2, 自提核销:1}`，修正 :154 注释与 :50/:57/:65 按钮分支（按后端 `delivery_type` 语义：1 自提、2 配送）；补 `tests/api` 或 e2e 断言 tab→`order_type`→`delivery_type` 映射，防再次漂移。
  - 完成：模板三处 `currentTab` 残留一并改为 `activeTabIndex`/`isDeliveryTab`；取证时额外发现同源缺陷「把 tabs 的 value 当索引传给 `u-tabs :current`」，已一并纠正。
  - 契约防漂移：`tests/api/16-fulfillment-delivery.spec.ts` 新增「`order_type` 与 `delivery_type` 严格同值」用例。
  - 顺带查出并修复真实产品缺陷（归属层）：C 端切换社区只写 `users.community_id`、不补 `user_communities`，导致「商品选得到、后台派不动配送员（用户不存在）」——`services/user/service/user.go` 的 `UpdateUserCommunity` 改为调用幂等写入器 `MarkUserCommunity(source=user_select)`；模型/文档/proto 的 source 取值口径同步补齐（12/12 用例转绿）。
- [x] D2. 确认页重入守卫：`src/client/src/pages/order/confirm.vue:794` 首行加 `if (submitting.value) return`（不依赖 u-button 的 loading 拦截）；`createOrder` 成功而 `payOrder` 失败时 `redirectTo` 订单详情，与既有 `outcome === 'redirected'`（:848）分支对齐。
  - 完成：新增 `createdOrderId`，下单成功后的任何异常（含支付发起失败）统一改跳订单详情，消除「已下单 + 购物车已清」中间态停留在确认页诱发重复下单。
- [x] D3. keep-alive 激活刷新补齐：以下 15 个页面加 `onActivated(loadData)`——`views/user/index.vue`、`views/statistics/index.vue`、`views/system/statistics.vue`、`views/product/categories.vue`、`views/inventory/check.vue`、`views/inventory/reports.vue`（当前 setup 体内直拉）、`views/marketing/coupon-records.vue`、`views/system/admins.vue`、`views/system/accounts.vue`、`views/store/settings.vue`、`views/settings/{site,payment,channel,refund,attachment}.vue`；整份 PUT 的设置类页面（如 `api/admin.ts:415` updateSiteSetting、`api/user.ts:92` updateCommunityProfile）保存前比对 `updated_at`，冲突则拒绝提交。
  - 激活刷新：新建 `src/admin/src/utils/activateRefresh.ts`（`refreshOnActivated`，跳过 mounted 后紧跟的首次 activated，避免双请求），15 页全部接入；`npm run build` + 144 单测全绿。
  - 乐观并发：新增业务码 `10007 DataConflict`（`common/errcode`）并把 gRPC `codes.Aborted` 映射到它（`common/response/grpc_error.go`）。三处整份覆盖保存全部有守卫——站点设置在网关比对 `site_configs.version`（`gateway/handler/site.go`），支付设置沿用 payment 服务 `updated_at` 比对（改为 `Aborted`），附件配置补上 `updated_at` 比对并把 `SaveAttachmentConfig` 改走 `bizerr.ToStatus`（`services/product/service/attachment.go`、`services/product/server.go`）。
  - 附件版本号比对必须做时间归一：同一行 `updated_at` 有两种字符串形态（本服务保存写入的空格格式 vs 驱动按 DATETIME 回读 string 的 RFC3339），直接字符串相等会把同一版本判成冲突，故用 `sameConfigVersion` 复用同包 `parseTime` 比较（该双格式问题在 `product/service/marketing.go` 已有先例注释）。
  - 冲突恢复：`views/settings/{site,attachment}.vue` 在 10007 时重拉配置刷新版本号（支付页此前已「成功失败都重拉」），否则用户按同一份旧表单重试会一直撞同一个冲突；`api/request.ts` 导出 `CODE_DATA_CONFLICT`。
  - 不做项与依据：`updateCommunityProfile`（门店设置）是**白名单局部更新**（`Partial<Community>`，服务端忽略非白名单字段），不是整份覆盖，覆盖风险面与三张单行配置表不同量级；要加版本守卫需给 `user.CommunityInfo` 补 `updated_at` 字段并改 proto/服务/网关/前端四层，收益不匹配，且该页接入激活刷新后「旧表单」这一主要触发源已消除——记录结论，不改库。
  - 测试：新增 `tests/api/81-settings-conflict.spec.ts`（4 条：三处过期版本被拒且库中配置逐项不改写、文案透传，空版本号保持原行为）；`47` 第 8 条由「非 0」收紧为断言 10007；`60`/`71` 改为携带最新版本号提交（否则被新守卫拒绝）。
  - 顺带修掉一处测试环境态污染：`71` 用例把站点域名设为测试域名时，站点保存联动会把空的附件「访问域名」写成测试域名并留在库里，下一轮 `71` 的 logo/favicon 回读变成绝对地址而自相矛盾——`71` 的 afterAll 补恢复附件快照，回读期望前缀按当前访问域名现算（连跑 3 轮 30/30 稳定）。
- [x] D4. 秒杀展示口径：`flashSalePriceEnabled = false`（`order.go:1864`）前提下，C 端不再展示与收款不符的 `flash_price`（或改由后端下发「活动未开启」口径）；清理 `src/client/src/api/marketing.ts:114` 硬编码 `/static/logo.png` 与 :118 恒 0 销量。
  - 取证纠正审查报告的前提：`order.go:1862` 原注释称「库存扣减与限购校验尚未实现」**不成立**——秒杀池扣减已完整实现（`product/service/stock.go:523 DeductFlashSaleStock`：幂等日志 + `stock >= qty` 条件更新防池超卖，`BatchDeductFlashSaleStock` 批量口 + 回补侧 `BatchReleaseFlashSaleStock` 均在），唯一真实阻塞是**每人限购 `limit_per_user` 无校验**（单用户可超额抢活动价），注释与 `tests/api/19` 用例说明已按此改写。开关本身不改（改的是收款金额口径，属产品决策）。
  - 归属层修数据：`FlashSaleItemInfo` 补 `product_image = 11` / `sold = 12`（`buf generate --path product/product.proto`），`server.go:GetFlashSale` 用现成的 `productByID` 封面图经 `ToMediaURLs` 整批转换（与商品查询同批，只读 1 次附件配置）+ `item.SoldCount` 回填，网关 `handler/product.go:ListFlashSales` 透出两字段；C 端 `marketing.ts` 改映射真实值（不再编造 `/static/logo.png` 与恒 0 销量，占位图退到模板层），`flash.vue` 展示价取 `original_price`（=下单实付）并加「限时活动价暂未生效，以下单实付为准」，库存行改「已售 n · 剩余 m」（`stock` 语义是余量，不臆造总量做进度条）。
  - 浏览器实测（`/s/app.html#/pages/flash/flash`，真重载）发现**存量缺陷**：`onShow` 里清空列表后未复位 `loadStatus`，上一轮已置 `nomore` 时被加载函数的守卫直接 return，页面永远只剩「没有更多了」——`flash.vue`、`full-reduction.vue`、`community/select.vue` 三页补 `loadStatus.value = 'loadmore'`（`user/user.vue:149` 早已注明同一坑并修过，本次补齐其余页面；`coupon.vue`/`order/list.vue`/`fulfillment.vue` 走 reset 函数已含复位）。修后实测 2 张卡片、图片 naturalWidth=600、价格 ¥3.50/¥2.00、已售 3/12 与库值一致。
  - 测试：`tests/api/19` 第 5 条补断言（展示价=SKU 价、`product_image`/`sold` 为服务端下发字段）；`tests/e2e/client/01-browse` 的「秒杀页可达」升级为「秒杀页按实付价渲染活动卡」（先取接口期望值，再断言卡片价、提示文案、已售/剩余文案，活动价不等于实付价时断言其不出现在价格区），替换原来只判 `uni-view` 可见的空断言。
  - 环境态：`attachment_config.visit_domain` 残留上一轮 `71` 用例写入的测试域名，导致图片出库为不可达绝对地址，已清空（下次由 `AttachmentDomainSync` 按请求域名惰性补写）；附件配置进程内缓存 TTL 60s，改库后需等一轮才生效。
  - 验证：`go build ./...` + `go test ./services/product/... ./services/order/... ./gateway/...` 全绿；`vue-tsc --noEmit` + `build:h5` 通过；`api/19`(9 passed)、`api/01/17/36`(29 passed)、`e2e/client/01`(5 passed)。

## E. P2 全量 · 后端 gateway

- [x] E1. `gateway/middleware/ws.go:70` 的 `?access_token=` 兜底与 `middleware/logger.go:30` 全量记录 RawQuery 组合导致令牌落日志——改为 header/subprotocol 优先 + logger 对 `access_token` 脱敏（兜底保留兼容，需注明窗口）。
  - 完成：`logger.go` 新增 `sensitiveQueryKeys`（access_token/refresh_token/token/password/code/sign 等 9 项）+ `maskSensitiveQuery`，按参数名把值替换为 `***`；不走 `url.ParseQuery+Encode`（会重排参数、改写未编码字符，降低日志可读性）。`ws.go` 的兜底保留（旧前端缓存与 POS 仍按查询参数握手），并在注释写清移除窗口与判据：观察期内用日志 path 复核无客户端依赖后即可删；cookie 优先顺序原本已正确，未改动。
  - 用例：`gateway/middleware/logger_test.go` 覆盖脱敏命中/大小写/空值/非敏感参数原样保留。
- [x] E2. `gateway/handler/fulfillment.go:135` 改走 `parsePageParams`（对齐 `pos_shift.go:153/172` 约定）。
  - 完成：`ListFulfillmentOrdersReq` 去掉 `Page/PageSize` 表单字段，统一 `parsePageParams(c)`；额外发现服务端 `fulfillment.go` 把 >50 的页长回退成 10，网关若透传 `parsePageParams` 的封顶 100 会出现「请求 100 条只回 10 条」的静默短页，故加 `fulfillmentMaxPageSize = 50` 常量在网关侧先收敛（注释记该口径来源）。
- [x] E3. `gateway/client/client.go:70` 为 gRPC 调用加缺省 deadline（注意 NewClient 是懒连接，实质风险在 RPC 无截止）；`handler/order.go:206→226` 清理协程 `context.Background()` 加超时。
  - 完成：新增 `gateway/client/deadline.go`——一元拦截器 `defaultDeadlineUnaryInterceptor`（缺省 15s，已带 deadline 的调用原样透传不重复包裹），按 `*_FullMethodName` 常量给预览/导入/附件扫描等长任务覆写（2~10 分钟）；流式调用不套超时（事件订阅生命周期由 WS 连接自管）。拦截器在 `initClients` 的 `WithUnaryInterceptor` 挂载。`cleanupCartAfterOrder` 的后台清理改用 `context.WithTimeout(context.Background(), cartCleanupTimeout=30s)`，整批共用预算。
  - 用例：`gateway/client/deadline_test.go`（无 deadline 时补齐 / 已带 deadline 透传 / 长任务方法名单生效）。
- [x] E4. `gateway/middleware/security.go:22/25` CSP 收紧 `unsafe-inline unsafe-eval`——先实测 admin/client 是否依赖内联脚本与 eval，能收则收，不能收则记录取舍与例外范围。
  - 实测证据（对已构建产物 `bin/static/`）：admin 入口 `index.html` 只有一个外链 `<script type="module" crossorigin src="/b/assets/index-*.js">`，无内联脚本；H5 `app.html`/`index.html` 各有 2~4 个内联 `<script>`（uni-app 视口探测与 Frame 宽屏跳转，内容随构建变化无法固定 hash）；两端 `assets/` grep `eval(` 与 `new Function(` 均为零命中，`unsafe-eval` 可去。
  - 交付：CSP 拆成 `cspShared`（default/img/font/connect/style）+ `cspClient`（H5 保留 `script-src 'self' 'unsafe-inline'`、去 eval）+ `cspConsole`（后台 `script-src 'self'`，内联与 eval 全关），按 `isClientEntry`（`/s` 与其子路径，精确匹配避免 `/static` 误判）分派；`style-src 'unsafe-inline'` 保留并在注释写明原因（naive-ui 与 uni-app 运行时注入 `<style>`，去掉会把两端样式整片打回无样式）。`X-Frame-Options` 三态口径不变（app.html SAMEORIGIN、其余 DENY）。
  - 待办：浏览器实测新 CSP 下后台与 H5 样式/交互无回归，归入 K4。
- [x] E5. `gateway/handler/update.go:71` 把外网 HTTP 移出全局互斥锁（锁内只读缓存，取版本走单飞）；`handler/about.go:56→70→97` 的 `/proc` 探测结果加进程级缓存。
  - 完成：`checkLatestVersion` 拆成 `cachedUpdateResult`（`updateCacheMu` 锁内只做 TTL 比较，不含任何外网调用）+ 单飞（`updateFlightMu` + `updateFetch{done,result}`：过期后只放一个请求打双源，其余请求 `<-done` 等同一份结果，成功后写缓存并清空在途句柄）；旧实现是「全局锁串行 + 每个请求都可能打外网」，慢源会把读缓存的请求一起排队。
  - `detectDeployMode` 改用 `sync.OnceValue` 包装成 `deployMode`，关于页取 `deployMode()`——`/.dockerenv` 与 `/proc` 进程遍历在一次进程生命周期内不变，旧实现每请求全量扫 `/proc`（容器内进程多时成本随请求线性放大）。
- [x] E6. `gateway/handler/admin_dataimport.go:32/72` 补齐函数级注释（参数/返回值/异常/示例）。
  - 完成：三个入口按函数级注释规范补齐（用途、社区语境、异常码 10001/11005、`GET /api/v1/admin/data-import/packages`、`POST .../preview`、`POST .../import` 与请求体示例）；`full-demo` 这一 package_key 示例取自 `services/product/service/seeddata/full-demo.json` 顶层 `key` 字段实测值，非拟造。
- [x] E7. `gateway/ws/hub.go:395` `kick` 返回值与注释口径统一（意图踢线数 vs 实际断开数）。
  - 完成：`Unregister` 改返回 `bool`（本次是否真的从在线集合摘除，幂等语义不变），`kick` 按返回值累加，注释写明「快照取出后客户端自己断开时不计入」；三个 `Kick*` 出口与 `subscriber.go` 的日志文案「已断开 N 条」因此与实际一致。
- [x] E8. `gateway/handler/admin.go:2709` 判空、:2809 无条件解引用——统一为同一口径（保留防御则全程防御）。
  - 完成：`GetAdminOrder` 在 RPC 成功后单点收口 `resp.GetOrder() == nil → 10003 订单不存在`，随后下游一律按非空主体处理（`userName`/`payType`/`address` 三处条件判空一并去掉，`payType` 改直接取 `adminOrderPayInfo` 返回值），消除「既判空又无条件解引用 `o.Id`」这种两边都没兜住的混用口径。

## F. P2 全量 · 后端 user / common / product

- [x] F1. `services/cart/server.go:450-489` upsert 原子化：行锁或 `ON CONFLICT`（模型 `model/cart.go:12/15` 有 `uk_user_sku`），并发不再抛原始 1062。
  - 完成：`upsertCartItem` 改单条 `clause.OnConflict{user_id,sku_id}` 的 `INSERT ... ON DUPLICATE KEY UPDATE`，`quantity` 由库侧 `quantity + ?` 累加、同时刷新 `selected`/`updated_at` 与三项快照；旧「先查后写」在并发加购同一 SKU 时两条都读到「不存在」，第二条把原始 1062 抛给前端，且读-改-写会丢更新。
  - 用例：`tests/api/04-cart.spec.ts` 新增「并发加购同 SKU 不报冲突且数量精确累加」（`Promise.all` 5 次 POST，断言只 1 行、数量 = 基线 +5）——纯内存单测覆盖不到唯一索引，只能走接口层。
- [x] F2. `services/user/service/role.go:290` 只在真正的唯一键冲突上返回「角色编码已存在」，其余 DB 错误原样上抛（精确错误号判定）。
  - 交付：新建归属层公共件 `common/dberr/duplicate.go`——`IsDuplicate(err, indexNames...)` 用 `errors.As(*mysql.MySQLError)` 精确判 1062，可选按索引名收窄；`role.go` 冲突分支只认 `uk_role_community_code`，其余错误原样上抛（不再伪装成业务重名）；`order/service/shift.go` 的同类字符串匹配一并改用该 helper（顺带清掉失效的 `strings` 导入）。
  - 用例：`common/dberr/duplicate_test.go` 覆盖错误号判定、文本含「1062.00」但错误号是 1406 的反例、索引名收窄命中与不命中。
- [x] F3. `services/user/service/community.go:127-129` 拼 `ORDER BY` 前拒绝 NaN/Inf（ParseFloat 结果先 `math.IsNaN/IsInf` 校验）；`:272` `DeleteCommunity` 加引用守卫（有订单/用户/商品关联时禁止硬删或改软删）。
  - 服务层：`nearbyOrderSQL(lon,lat)` 非有限值直接退化为 `id asc`（不再把 NaN 拼进 SQL）；`DeleteCommunity` 事务内先按硬关联计数（会员社区归属/收货地址/管理员任职/社区角色）命中即 `FailedPrecondition` 阻塞，浏览态引用 `users.community_id` 归零，`RowsAffected==0` 判 NotFound。
  - 网关层：坐标入参 `parseCoordinates` 统一校验（err/NaN/Inf/|lon|>180/|lat|>90 归零），`DeleteCommunity` 增加跨服务的商品/订单引用检查 `communityBusinessRefs`（借现有列表 RPC 的 `pagination.total` 计数；RPC 查询失败时选择保守阻塞，不把「查不到」当「没有引用」）。
  - 用例：`services/user/service/community_nearby_test.go`、`gateway/handler/user_coordinate_test.go`。
- [x] F4. `services/user/service/region.go:262` 检查-创建改原子（唯一约束或事务内 `FOR UPDATE`），后台任务 ctx 不脱离请求生命周期语义（显式 WithTimeout）。
  - 表结构（走 AutoMigrate）：`region_update_tasks` 新增 `active_flag *int8` + 单列 `unique` tag（NULL 可重复、进行中恒唯一，库中约束名实测为 `uni_region_update_tasks_active_flag`）；模型注释写明「为什么先查后插不够」。
  - 服务层：`CreateRegionUpdateTask` 直接 `Create` 并把 1062 转成幂等返回在途任务；`runUpdateTask` 自带 `regionTaskTimeout=10min` 的 `WithTimeout`（不再挂请求 ctx，也不裸奔无截止）；终态统一走 `finishRegionTask`（置 `active_flag=NULL` + `finished_at`），失败补记用 `context.Background()` 保证写得进；`diffAndStore` 全链路 `WithContext`。
  - 新机制引入的风险已兜住：进程被杀会让占位永不释放、功能整体不可用，故启动时 `ReleaseStaleRegionTasks` 回收遗留占位（不阻塞启动，有回收则打日志），接入 `services/user/main.go`。
- [x] F5. `ListAdmins`（`services/user/service/admin.go`）与 `buildCartItem` 的 N+1 RPC 改批量接口。
  - 复核结论（报告这一条只有一半成立）：`ListAdmins` 链路已无 N+1——服务层 `admin.go:177` 用 `admin_id IN ?` 一次取任职行，`user/server.go:430/442` 的角色与绑定会员各一次批量，网关 `toAdminVO` 是纯映射不发 RPC；不改代码，仅在此登记取证结论。真正的 N+1 在购物车展示侧。
  - 购物车：`buildCartItems` 改为整批预取 `loadCartCatalog`（1 次 `BatchGetSkus(tolerate_missing)` + 按规格归属社区分组的 `BatchGetProducts`，C 端单社区语境即共 2 次 RPC），`buildCartItem` 退化为纯装配函数、失效判定抽成纯函数 `cartItemInvalidReason`（顺序与文案保持与旧逐行版一致：SKU 缺失→商品已失效→规格已失效→商品已下架→已售罄）。旧实现每行 2 次 RPC，且每次 `GetProduct` 在服务侧还要跑 5 条 SQL（主表/SKU/规格/图集/分类名）。
  - 口径改进：批量出口用 `tolerate_missing` 逐项判定，单行商品被删除或商品与规格社区归属漂移只让该行失效，不再像旧版那样把归属漂移的 `PermissionDenied` 包成 Internal、整个购物车打不开；SKU/商品取不到时展示字段回退购物车快照的行为不变。
  - 归属层配套：`ProductInfo` 新增 `sku_total`（字段号 24，proto 已 `buf generate`），由 `SkuService.CountActiveSkusByProductIDs` 一次 `GROUP BY` 回填、仅 `BatchGetProducts` 填充——否则批量出口拿不到「启用 SKU 数」，购物车的规格切换入口（`sku_total > 1`）会失去依据；口径与 `ListSkusByProductID`/`syncProductAggregates` 一致（只统计 status=1）。
  - 网关同类项：跨社区订单列表原先按「本页不同社区数」逐个 `GetCommunity`，改为 `communityNamesByIDs`（整批分页列表，200/页、上限 10 页，凑齐即停；失败仍按空名退化，不阻断列表）。
  - 用例：`services/cart/server_test.go` 重写失效判定用例为纯函数表驱动，新增 `TestBuildCartItemsBatchPrefetch`（断言 SKU 批量只 1 次、商品按社区分组各 1 次、逐行 `GetProduct` 零调用、跨社区行字段正确）与 `TestBuildCartItemsSnapshotFallback`（缺失项回退快照 + 标失效 + 不整体报错）。
- [x] F6. `services/user/main.go:34/47` 的 `Migrator().DropColumn` 属 AutoMigrate 能力外的手写 DDL——登记为受控例外并写明限期移除条件（或迁到一次性脚本）。
  - 交付：`docs/需求文档/05-数据库设计/01-设计规范.md` 新增「AutoMigrate 能力缺口」条目 + `#### 5.1.1 手写 DDL 受控例外台账`，逐条登记两处 `DropColumn`（`user_communities.is_fulfiller` 废弃列清理、`site_configs.copyright_text/icp_number` 合一迁移+清理），移除条件写清为「information_schema 逐列计数为 0 的各环境确认」，并规定**未登记的 DropColumn/CREATE/ALTER 视为违规**；`services/user/main.go` 两段注释同步改为「受控例外（已登记 5.1.1 台账）……移除条件：各环境确认后删除本段」。
- [x] F7. `services/product/service/product.go:562/624` `UpdateProduct` 改字段级更新或加 `version` 乐观锁，避免整行 Save 覆盖并发编辑。
  - 取证：丢更新的真实来源不是「两个管理员同时编辑」，而是 `sales_count` 与各 SKU 汇总列由别的写入方（下单/扣减/回补）维护，`UpdateProduct` 事务外读到的快照在提交时整行 `Save` 会把读取时点的旧值写回，覆盖这期间已完成的销量/库存汇总。
  - 交付：新增字段白名单 `productEditableColumns`（14 列，与表单可编辑项一一对应），`tx.Save(p)` 改 `tx.Model(&model.Product{}).Where("id = ?", p.ID).Select(productEditableColumns).Updates(p)`——`Select` 后零值也会写入，因此「清空描述/清空富文本」仍能生效，白名单外的列不进入 UPDATE 语句。选字段白名单而非 `version` 乐观锁：无需改表，且不牺牲后台「后保存者覆盖」这一既有交互语义。
- [x] F8. `services/product/service/spec.go:318 saveSkus`（注释起 :303）事务内读旧 SKU（:434）加 `clause.Locking{Strength:"UPDATE}`，防止把旧 stock 覆盖回去。
  - 交付：旧 SKU 读取加 `Clauses(clause.Locking{Strength: "UPDATE"})`，与并发的库存扣减/回补在同一行上串行；`syncSkuStockBatches` 依赖「当前 `stock` 恒等于 Σ 在库批次」这一不变式，不加锁时读到旧值会把批次增减量算错。
  - 顺带纠正一处错误吞掉：原实现把 `Take` 的任何错误都判成「SKU 不存在或不属于该商品」，锁等待/连接错误会被伪装成 NotFound；改为只对 `errors.Is(err, gorm.ErrRecordNotFound)` 返回业务提示，其余错误原样上抛。新增 `gorm.io/gorm/clause` 导入。
- [x] F9. `services/product/service/product.go:607` `OnSaleAt` 空串恒清除、`:588` `Sort>0` 使 sort 无法置 0——改用可选语义（proto optional / 指针字段），前端表单同步。
  - 交付：proto `UpdateProductReq` 的 `sort`（8）/`on_sale_at`（17）改 `optional`（已 `buf generate`，生成 `*int32`/`*string`）；服务层 `if req.Sort != nil { p.Sort = *req.Sort }`，定时上架改三态——不传=维持原预约、显式非空=设预约（并置 status=0、校验时间格式）、显式空串=取消预约；网关 HTTP `UpdateProductReq.Sort/OnSaleAt` 同步改指针并原样透传，注释从 struct 内联提到文档注释（避免 gofmt 因注释断行把整个结构体拆成多段对齐）。
  - `status` 保持不变（仍是「0=不改动」）：上下架有专门 RPC 且会同时清预约，扩大到这里会把两条路径的语义搅在一起，不属本条目范围。
  - 前端不需要同改：唯一 HTTP 调用方 `src/admin/src/views/product/form.vue` 每次全量提交 `sort`/`on_sale_at`（实测 :595/:599），改 optional 只修接口契约、不改后台行为。
  - 用例：`tests/api/82-product-edit-tristate.spec.ts`（5 条 serial：sort=5 基线 / 传 0 置 0 / 不传维持 / 设预约后普通编辑保留预约 / 显式空串取消预约且 status=1；编辑接口要求全量 `sku_items`，故每次 PUT 都回传详情现值）。
- [x] F10. `CreateProduct`/`UpdateProduct` 补 Name 长度与 Content 大小校验，与 `UpdateProductName` 的 100 字口径一致；admin `product/form.vue` 补 `maxlength`。
  - 交付：抽出口径单一来源——常量 `productNameMaxRunes = 100`、`productContentMaxBytes = 1 << 20`，函数 `normalizeProductName(name, required)`（TrimSpace + 必填判定 + 超长按 `rune` 计数报「商品名称不能超过 100 字」）与 `validateProductContent(content)`；`CreateProduct` 用必填态、`UpdateProduct` 用「空=不改动」态、`UpdateProductName` 重构为复用同一函数（原先三处各写一份、长度口径不一致）。
  - 1MB 上限理由（写进注释）：不设限会撞 gRPC 默认 4MB 消息上限，用户看到的是「系统错误」而不是「内容过大」；富文本含内联样式与 base64 时体积增长快，1MB 对应数万字正文，够业务用。
  - 前端：admin `product/form.vue` 名称输入补 `:maxlength="100"`，与后端口径一致。
- [ ] F11. `services/product/service/attachment.go:884` `checkImageMime` 改服务端 magic-number 探测（`http.DetectContentType`），`:914` `decodeImageSize` 失败即拒而非记 0。
- [ ] F12. 服务层分页封顶统一化：`services/product/service/stock.go:864` 等十余处列表接口接入统一 pageSize clamp helper（网关 `maxPageSize=100` 只约束 HTTP，gRPC 侧当前裸奔）；order/payment 同类项并入本条（`services/order/service/order.go:673`）。
- [ ] F13. `services/product/service/attachment_migrate.go:38` 扫描附件根时排除 `exports/`，避免导出文件名/大小进迁移清单。
- [ ] F14. 商品 keyword 查询补 `escapeLike`（`services/product/service/product.go:161` 等同类位置统一）。
- [ ] F15. `services/product/server.go` 不再把内部 DB 错误统一转 `InvalidArgument` 并透出原文（转内部错误码 + 日志留原文）。

## G. P2 全量 · 后端 order / payment

- [ ] G1. `services/order/service/free_shipping.go:195` 补与 `order.go:238/247` 对称的 nil 防御。
- [ ] G2. `services/order/service/order.go:1393` 流转表补 `5→7`，并把 `refund.go` 直接 Save 的旁路收敛到状态机单轨。
- [ ] G3. `order.go:1989` 抢占态 100/101/102/103 归桶，保证「桶和 = 总数」，与注释一致。
- [ ] G4. `order.go:2170` Top1-SKU 原生 SQL 补 `user_deleted=0`、社区条件与 B1 的状态口径。
- [ ] G5. `order.go:336` 金额汇入加 `round2`（对齐 `refund.go:1741`）。
- [ ] G6. `order.go:1051` 取消后关渠道单改用 `runPostCommitEffect` 加固模式（不受请求 ctx 取消影响、失败有流水）。
- [ ] G7. `services/order/service/export_task.go:544` `DeleteExportTask` 服务端补权限校验（不依赖网关超管闸）。
- [ ] G8. `services/payment/service/payment.go:1443` `isDuplicateEntryErr` 改按 MySQL 错误号 1062 精确判定（`errors.As` + `*mysql.MySQLError`），不再用 `"1062"` 裸子串（避免 `Truncated incorrect DOUBLE value: '1062.00'` 之类误判成冲突）。
- [ ] G9. `payment.go:106` 现金/付款码互斥的进程内分段锁——改为库层串行点（唯一约束/条件更新）或明确单实例部署约束并写入文档。
- [ ] G10. `payment.go:367` 不再把 DB 错误吞成 NotFound。
- [ ] G11. `services/payment/service/wechat.go:238` 验签改用带超时的请求 ctx；`:251` 解密后核对 `mch_id/appid/currency`（不一致即拒并记异常流水）。
- [ ] G12. 支付宝 SDK HTTP 超时：SDK 用 `http.DefaultClient`（`alipay-sdk-go-all/generic_execute_api.go:54`），有 `SetHttpClient` 注入点未用。**跨仓库改动，须打新 tag 并上调 require**（见 J 组风险）。
- [ ] G13. 支付宝回调失败取证原文脱敏（`sign`、`buyer_logon_id`），力度对齐微信侧 `maskWechatNotifyHeaders`；长期留存策略一并明确。

## H. P2 全量 · 前端 admin / client

- [ ] H1. `src/admin/src/views/system/statistics.vue:283` 加请求序号守卫（复用同仓 `views/statistics/index.vue:273 loadSeq` 范式）并按社区并发上限化（当前 `page_size:200` → 最多 200 个并发统计请求）。
- [ ] H2. `src/admin/src/router/index.ts:249/341` 的 `meta.superOnly` 在 `beforeEach`（:469 起）落地判定，或移除该语义（后端已有 `IsSuperAdmin` 双闸，属闸门语义脱节）。
- [ ] H3. 商品/订单/库存列表操作按钮补按钮级权限判定，与会员列表 `canManage` 口径统一。
- [ ] H4. 主列表页统一竞态守卫（对齐 `UserPicker`/`SkuPicker` 已有范式），与 D3 的 `onActivated` 改造同批做完。
- [ ] H5. `DownloadCenter.vue:443`、`views/user/index.vue:283` 等 `dialog onPositiveClick` 的 async 补 try/catch，失败可见且不误关弹窗。
- [ ] H6. `src/admin/src/views/order/detail.vue:49` 金额拆解补「手动优惠」行，使 `total − discount + fee = pay` 成立。
- [ ] H7. 券可用门槛口径与后端一致（后端 `order.go:1825` 按适用范围，前端当前按全车金额）。
- [ ] H8. `src/client/src/pages/category/category.vue:858` 购物车弹层写操作 await + catch，再重拉汇总。
- [ ] H9. `src/client/src/pages/user/user.vue:145` `fetchUserInfo` 补异常捕获。
- [ ] H10. 分类页搜索固定 20 条改为可翻页/加载更多。
- [ ] H11. `src/client/src/pages/order/detail.vue:228` 自提 statusTip 补状态 3（`showPickupCode`:202 已含 3）；`onLoad`(:259) 与 `onShow`(:275) 去重，首进只拉一次。
- [ ] H12. `src/client/src/utils/pay.ts` 货到付款提示由「支付成功」改为「下单成功，请当面付款」。
- [ ] H13. `src/client/src/api/request.ts:321/358` 全局 loading 加引用计数（报告误写为 `utils/request.ts`），避免并发请求先完成者掐掉进行中 loading。

## I. P2 全量 · 构建部署与文档

- [ ] I1. `docker-compose.yml:282` 的 `[ -w ] || rm -f /app/runtime/config.yaml` 静默重建改为显式失败/告警（忘 `chown 10001` 时不再丢运行态配置）。
- [ ] I2. `docker-compose.prod.yml` 补 `./data/ssl` 挂载（dev :273 已挂），避免每次重建换 RSA 登录私钥、多实例互斥。
- [ ] I3. 13 处业务服务改用独立业务账号，不再直连 `MYSQL_ROOT_PASSWORD`；两套 compose 与配置模板同步。
- [ ] I4. all-in-one `entrypoint.sh:252-264` 不再 sed 回写明文口令到宿主 runtime 目录、`:320` 不用命令行 `-p`，统一改 `MYSQL_PWD`（对齐 `start-all.mjs:205` 口径）。
- [ ] I5. `.dockerignore` 补 `bin/uploads/`、`bin/config.local.yaml`、`bin/release/`，并修正 :64 仍写的 `bin/attachment/`（构建上下文实测 2.0G）。
- [ ] I6. `docker-compose.prod.yml` 去掉镜像 `:latest` 回落（与同文件 :14 注释一致），缺省即报错要求显式 tag。
- [ ] I7. `start-all.mjs:314` 健康等待改用 `loadGatewayPort`（:189 已有回退逻辑却未用），改端口后不再假超时。
- [ ] I8. `deploy.mjs:51` 的 `timeout /t 1 /nobreak` + `stdio:'ignore'` 改为可靠 sleep（120 秒预算实际失效）；:5/:122 注释「Linux arm64 交叉编译」按现状改为全平台编译。
- [ ] I9. `build.mjs:199` 并发阶段保留并上报首个真实错误文本（当前被吞）。
- [ ] I10. 镜像新鲜度检测扩到前端：`build-image.mjs:230` 的 `newestSourceMtime` 纳入 `src/admin/src`、`src/client/src` 与 `bin/static/*` mtime 比对，过期即 fail 并提示先跑对应 build（当前只有 `fs.existsSync`，改前端未重跑 build 会静默打旧包）。
- [ ] I11. `docker-compose.prod.yml:217/267` 的 `APP_ENV: ${APP_ENV:-}` 改为 `${APP_ENV:?生产部署必须显式设置}`，compose 头部人工步骤（:13-19）补 APP_ENV 必配；`cmd/market-admin/seed.go:31` 在非 production 导入演示种子时打显著告警并要求二次确认变量（弱口令实据：`full-demo.json:285/294` `admin123`、:310 `13800138000`）。
- [ ] I12. 文档漂移：《03-目录结构.md:55》e2e admin「4 个」改「10 个」；`README.md:86` 与《10-部署方案/03-生产环境部署.md:233/241》的 `mkdir data/{...,attachment,...}` 统一为 `uploads`（同文档 154/220/252 与两套 compose 已是 uploads）。
- [ ] I13. 报告勘误留档：在 `代码审查报告-20261028.md` 追加「二次取证勘误（2026-10-28）」小节——基线两处不实（go test 1 红且牵出 A1 真缺陷、vitest flaky 1 条）、「11 个未提交文档修订」表述失实、P1#6 危害被 `FOR UPDATE` 与订单行条件更新部分兜底（定级偏重）、P1#11 u-button 已绑 `:loading`（真实窗口在 payOrder 抛错后）、P1#4 无触发路径属潜伏、P2 中 `utils/request.ts` 路径错 / NaN 无注入面 / `rm -f` 有守卫 / `grpc.NewClient` 懒连接 / `payment/server.go:301` 实为 AppSecret（私钥 :305，`toPayConfig` 全段 272-318）；两条疑似项判定结果并入 §3。

## J. 台账与跨仓库风险（需你点头才动）

- [ ] J1. `docs/需求文档/11-开发计划.md` 追加第 94 项，登记本轮处置与遗留；同步修正第 90 项的行号口径（272-318，私钥在 :305）。
- [ ] J2. 支付宝 SDK 超时（G12）在兄弟仓库：改动须打新 tag 并上调 require，是否本轮做由你定；不做则登记为受控例外。
- [ ] J3. 涉库变更（B3 新列+复合唯一索引、I3 独立业务账号）需在真实库上跑 AutoMigrate；存量双开班次清理脚本执行前单独确认，不擅自删数据。C3 的 `users.phone` 已从库侧确认列本就 `NULL` 可空（`uni_users_phone` 唯一索引），改 `*string` 不产生任何 ALTER，只改写入值（`''`→NULL），因此不在本项范围内。

## K. 验证（每批完成后跑，最后整体验收）

- [ ] K1. `gofmt -l` 空、`go build ./...`、`go vet ./...`、`go test ./...` 全绿（A1 必须转绿）。
- [ ] K2. admin `vue-tsc --noEmit` + `npx vitest run` 整轮全绿且可重复（A2）；client `npm run type-check`。
- [ ] K3. `tests`：`npm run test:api:strict`（含 `--max 0` 完整性闸门）；docker 起栈跑相关 spec，重点覆盖统计口径、履约 tab 映射、匿名商品详情越权、员工列表无 password、班次并发、退款并发。
- [ ] K4. 浏览器实测（真重载，非同一 URL no-op）：admin 统计页与设置页激活刷新/并发请求上限、C 端确认页重入、履约页按钮与单据类型对应关系。
- [ ] K5. 逐条在本文件标注完成，未完成项写明原因与后续批次。
