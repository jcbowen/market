# 需求：C 端订单与首页体验修复

## 背景

用户反馈 C 端（uniapp H5）四类问题：

1. 申请退款时，退款金额未默认填上，需手动输入
2. 订单列表点击切换「待支付」tab 页面报错（浏览器实测复现：`TypeError: Cannot read properties of undefined (reading 'rect')`，列表被清空）
3. 部分页面样式超宽（Frame 模式 300px 视口实测：页面横向可滚动约 16px）
4. tabBar 页面切换后数据丢失：首页 → 分类 → 首页，首页数据变空

## 根因分析

### 问题 1：退款金额未默认填上
- RefundModal.vue 的 `amountInput` 初始为空字符串，仅 placeholder 提示「不超过 X」

### 问题 2：切换 tab 报错 + 列表空
- 根因 A：uview-plus 3.x 的 u-tabs `change` 回调参数是 `{...item, index}` 对象而非数字索引，
  `onTabChange(index: number)` 将对象赋给 `currentTab`：
  - u-tabs 内部 `setScrollLeft` 崩溃（`:current` 收到对象）
  - `tabs.value[currentTab.value]` 为 undefined 抛 TypeError，请求中断、列表清空
- 根因 B：`loadStatus === 'nomore'/'loading'` 门禁——切 tab / onShow 时未重置 `loadStatus`，
  `loadOrders()` 直接 return，列表空白
- coupon.vue 存在同款 u-tabs 用法问题

### 问题 3：样式超宽
- `.order-list`（scroll-view）默认 `box-sizing: content-box` + `width: 100%` + `padding: 20rpx`
  → 横向溢出（300px 视口下溢出 16px），整页可横向滚动

### 问题 4：tabBar 页面数据丢失
- 首页 onShow 先清空 `products` 再 `loadProducts()`，但首次加载不足一页后 `loadStatus='nomore'`，
  再次 onShow 时 loadProducts 直接 return → 数据永远空（订单列表 onShow 同病）

## 修复方案

1. **RefundModal**：加载完成后默认填入汇算金额；用户手动修改后不再覆盖；未手动修改时
   随勾选行/数量变化联动更新（watch refundable）
2. **u-tabs 参数**：`onTabChange` 改收 `{ index }` 对象取 `index`；coupon.vue 同步修复
3. **loadStatus 门禁**：切 tab / onShow 时重置 `loadStatus='loadmore'`；加载函数引入请求序号
   防竞态（快速切换 tab 时旧响应不覆盖新数据）
4. **超宽**：`.order-list` 补 `box-sizing: border-box`；检查 RefundModal 弹窗宽度
5. 排查 cart/user/category 等页面同类问题
6. vue-tsc 校验 + 构建部署 + 浏览器实测（退款弹窗默认金额、切 tab 无报错、首页数据不丢、无横向超宽）

## 任务清单

- [x] 1. RefundModal 退款金额默认填入汇算值（手动改过后不覆盖，未改过随勾选/数量联动）
- [x] 2. 修复订单列表 u-tabs change 参数错误，coupon.vue 同步修复
- [x] 3. 修复 loadStatus 门禁导致列表空白（订单列表切 tab/onShow、首页 onShow，请求序号防竞态）
- [x] 4. 修复 order-list padding 横向溢出，检查 RefundModal 弹窗宽度
- [x] 5. 排查 cart/user/category 等其他页面同类问题
- [x] 6. 构建部署 + 浏览器实测全场景

## 执行过程中的补充发现与修复

1. **u-input touched 标记失效**：uview-plus Vue3 版 u-input 值变化只 emit
   `update:modelValue`/`change`，不 emit `input`，`@input` 永远不触发；
   改绑 `@change`（仅在用户输入时触发，外部赋值不触发，正好作为手动修改信号）
2. **u-button 横排按钮占满整行**：uniapp H5 的 button 默认块级撑满父容器宽度，
   订单卡片操作按钮（去支付/取消订单/申请退款）与优惠券「领取」按钮需显式收缩
   （`width: auto + min-width + padding`，`:deep(.u-button)` 穿透）
3. **同类 scroll-view 溢出**：coupon/community-select/flash/full-reduction 四页
   scroll-view 均有 padding 溢出问题，统一补 `box-sizing: border-box`

## 验证记录（2026-10-12，Frame 模式浏览器实测）

| 场景 | 结果 |
| --- | --- |
| 退款弹窗默认金额 | ✅ 打开即填入汇算值 2.00 |
| 金额联动（未手动改） | ✅ 取消勾选 → 0.00，重新勾选 → 2.00 |
| 金额不覆盖（手动改过） | ✅ 输入 1.5 后反复勾选/取消，金额保持 1.5 |
| 退款提交闭环 | ✅ 提交成功，refund id=9 amount=1.50 入库正确，列表自动刷新为「退款中」 |
| 订单列表切「待支付」 | ✅ 无 console 报错（修复前 setScrollLeft TypeError），数据正确 |
| tab 往返切换 | ✅ 全部/待支付/已完成往返正常 |
| 首页 ↔ 分类 ↔ 首页 | ✅ 首页分类宫格、热门商品数据不丢 |
| 页面横向溢出 | ✅ 订单列表/优惠券页 clientWidth=scrollWidth=375（修复前溢出 16px） |
| 操作按钮宽度 | ✅ 收缩为 78/66/78px 横排小按钮 |
| 弹窗样式 | ✅ 弹窗无超宽，布局正常 |
