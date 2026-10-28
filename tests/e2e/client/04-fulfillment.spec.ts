import { test, expect } from '@playwright/test';
import type { Page, Playwright } from '@playwright/test';
import { uniquePhone } from '../../helpers/data';
import {
  clientLogin,
  newAuthedRequest,
  newAdminContext,
  pickSku,
  ensureStock,
  createFulfillerStaff,
  removeFulfillerStaff,
  assertOk,
  type ApiResponse,
} from '../../helpers/api';
import { clientLogin as uiLogin } from '../../helpers/e2e';

/**
 * C 端 H5 E2E 04：履约工作台 tab 与单据类型对应
 * 覆盖：履约身份真实生效（不以「页面能渲染」冒充通过）、送货 tab 只请求 order_type=2 并出「开始配送」、
 *       自提核销 tab 只请求 order_type=1 并出「核销取货码」
 * 背景：该页曾因「按展示顺序猜 order_type」把两个 tab 的数据整体对调（送货 tab 查出自提单），
 *       2026-10-28 纠正为送货=2、自提核销=1；原「履约工作台可达」只断言首个 uni-view 可见，
 *       非履约账号看到「您不是本社区履约人员」也会绿，属假绿，故本模块自带造数与请求级断言。
 *       2026-10-29 本模块首次跑真渲染即抓到一个更严重的问题：工作台接口直接回 proto OrderInfo，
 *       其中没有 items 与 status_text，卡片渲染取 order.items.slice() 抛错导致整页一张单都画不出来，
 *       网关改为按 C 端订单列表口径补明细后修复；因此卡片可见性是本模块的主断言，不再只看页面可达。
 */

const phone = uniquePhone();

let staffId = 0;
let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let pickupOrderNo = '';
let deliveryOrderNo = '';

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, phone);
  const communityId = info.user.community_id;
  const buyerApi = await newAuthedRequest(playwright, info.access_token);
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  // 造数失败一律抛错（不 skip）：造不出履约身份时，本模块的断言就没有意义，绿了才是骗人
  staffId = await createFulfillerStaff(adminApi, communityId, info.user.id);

  const sku = await pickSku(buyerApi, undefined, communityId);
  // 补库存走后台入库单接口，必须用管理端上下文（会员上下文会报 11005）
  await ensureStock(adminApi, sku.id, 4);
  const addr = assertOk(
    (await (
      await buyerApi.post('/api/v1/user/addresses', {
        data: {
          contact_name: 'AUTO-TEST 履约收货人',
          contact_phone: phone,
          province: '重庆市',
          city: '重庆市',
          district: '渝北区',
          detail: `AUTO-TEST 履约路 ${Date.now()} 号`,
          is_default: true,
        },
      })
    ).json()) as ApiResponse<{ id: number }>,
    '创建收货地址',
  );

  const createPaid = async (deliveryType: number, quantity: number): Promise<{ id: number; order_no: string }> => {
    const order = assertOk(
      (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: deliveryType,
            address_id: deliveryType === 2 ? addr.id : 0,
            items: [{ sku_id: sku.id, quantity }],
          },
        })
      ).json()) as ApiResponse<{ id: number; order_no: string }>,
      `下${deliveryType === 2 ? '配送' : '自提'}单`,
    );
    assertOk(
      (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } })
      ).json()) as ApiResponse,
      '现金支付',
    );
    return order;
  };
  const pickup = await createPaid(1, 1);
  pickupOrderNo = pickup.order_no;
  // 同用户连续下单有 5s 防重窗口，错开后再下第二单
  await new Promise((r) => setTimeout(r, 5500));
  const delivery = await createPaid(2, 2);
  deliveryOrderNo = delivery.order_no;
  // 送货 tab 只列「指派给本人」的配送单（服务端按 delivery_user_id 过滤），
  // 未指派的配送单出现在这里才是缺陷；自提单则本社区全部履约人员可见
  assertOk(
    (await (
      await adminApi.put(`/api/v1/admin/orders/${delivery.id}/assign-delivery`, {
        data: { delivery_user_id: info.user.id },
      })
    ).json()) as ApiResponse,
    '指派配送单给履约人员',
  );

  await buyerApi.dispose();
});

test.afterAll(async () => {
  if (staffId && adminApi) {
    const warns = await removeFulfillerStaff(adminApi, staffId);
    warns.forEach((w) => console.log(`[履约回收] ${w}`));
    await adminApi.dispose();
  }
});

/**
 * 等待履约列表请求并解析其 order_type 参数与响应体
 * - 函数名: waitForFulfillmentRequest
 * - 参数: page — 页面对象；action — 触发请求的动作（切 tab 等）
 * - 返回: Promise<{ orderType: string | null; url: string; list: Array<Record<string, unknown>> }>
 *         list 为响应里的订单数组，用于把「服务端返空」与「前端未渲染」区分开
 * - 异常: 不抛出，超时由调用方断言兜底
 * - 使用示例: const r = await waitForFulfillmentRequest(page, () => page.getByText('自提核销').click());
 */
async function waitForFulfillmentRequest(
  page: Page,
  action: () => Promise<void>,
): Promise<{ orderType: string | null; url: string; list: Array<Record<string, unknown>> }> {
  const [resp] = await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v1/fulfillment/orders'), { timeout: 20_000 }),
    action(),
  ]);
  const url = resp.url();
  const body = (await resp.json().catch(() => null)) as { data?: { list?: Array<Record<string, unknown>> } } | null;
  return {
    orderType: new URL(url).searchParams.get('order_type'),
    url,
    list: body?.data?.list || [],
  };
}

/**
 * 登录并打开履约工作台，返回默认 tab（送货）的首个列表响应
 * - 函数名: openWorkbench
 * - 参数: page — 页面对象
 * - 返回: Promise<{ orderType, url, list }> — 默认 tab 的查单结果
 * - 异常: 请求 20s 内未到达则由 Playwright 超时抛出
 * - 使用示例: const first = await openWorkbench(page);
 */
async function openWorkbench(page: Page) {
  await uiLogin(page, phone);
  return waitForFulfillmentRequest(page, () => page.goto('/s/#/pages/fulfillment/fulfillment'));
}

test.describe.serial('履约工作台单据类型对应', () => {
  test('送货 tab 请求 order_type=2 且卡片出「开始配送」', async ({ page }) => {
    const first = await openWorkbench(page);
    expect(first.orderType, '默认 tab 是「送货」，请求的 order_type 必须是 2').toBe('2');

    // 身份必须真的生效：出现权限空态说明造数或判定断了，此时后续断言全是假绿
    await expect(page.getByText('您不是本社区履约人员')).toHaveCount(0);
    await expect(page.getByText('送货').first()).toBeVisible();
    await expect(page.getByText('自提核销').first()).toBeVisible();

    // 先断服务端：列表里没这张单就是后端过滤/身份语境问题，再谈前端渲染
    expect(
      first.list.map((o) => o.order_no),
      `送货接口应返回配送单 ${deliveryOrderNo}（实际 ${JSON.stringify(first.list.map((o) => o.order_no))}）`,
    ).toContain(deliveryOrderNo);

    const card = page.locator('.order-card').filter({ hasText: deliveryOrderNo });
    await expect(card, `送货 tab 应能查到配送单 ${deliveryOrderNo}`).toBeVisible({ timeout: 20_000 });
    await expect(card.getByText('开始配送')).toBeVisible();
    await expect(card.getByText('核销取货码')).toHaveCount(0);
    // 配送单卡片带收货地址，自提单没有——顺带锁住「tab 数据没串」
    await expect(card.getByText('AUTO-TEST 履约路')).toBeVisible();
  });

  test('自提核销 tab 请求 order_type=1 且卡片出「核销取货码」', async ({ page }) => {
    // 每个用例都是全新页面上下文，须自己登录进页再切 tab，不能蹭上一个用例的页面状态
    await openWorkbench(page);
    const second = await waitForFulfillmentRequest(page, () => page.getByText('自提核销').first().click());
    expect(second.orderType, '切到「自提核销」后请求的 order_type 必须是 1').toBe('1');

    const card = page.locator('.order-card').filter({ hasText: pickupOrderNo });
    await expect(card, `自提 tab 应能查到自提单 ${pickupOrderNo}`).toBeVisible({ timeout: 20_000 });
    await expect(card.getByText('核销取货码')).toBeVisible();
    await expect(card.getByText('开始配送')).toHaveCount(0);
    // 配送单不得出现在自提 tab（两 tab 数据对调是本项要锁的原始缺陷）
    await expect(page.locator('.order-card').filter({ hasText: deliveryOrderNo })).toHaveCount(0);
  });
});
