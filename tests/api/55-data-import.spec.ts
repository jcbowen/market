import { test, expect } from '@playwright/test';
import { newAdminContext, adminLogin, newAuthedRequest, assertOk, type ApiResponse } from '../helpers/api';

/**
 * 模块 55：应用中心 · 数据导入
 * 覆盖（管理端 /admin/data-import/*，product:create + 超管双闸）：
 *   - 包列表：仅社区域且非仅初始化的段（catalog/banners/marketing/free_shipping/user_assets），
 *     全局段（communities/regions/accounts/users/site，环境初始化种子）一律不出现
 *   - 非超管（社区管理员 shequ）访问包列表被拒绝（403）
 *   - 预检：结构化条目 + 动作合法性；数据集口径为全部商品（2026-10-27 起两社区商品名
 *     全部唯一，预检应包含社区 1 + 社区 2 的全部分类/商品条目）
 *   - 指定导入会员：未指定会员时 user_assets 段全部 depend_missing（禁止导入）；
 *     指定演示用户（13800138000，开发库已初始化且已持有种子券/购物车）后全部 skip
 * 数据口径：演示种子来源为 product 服务内嵌 full-demo.json（tools/gen-data-package.mjs 生成，
 * 经 market-admin init 导入）；用例不写库，仅读列表与预检
 */

test.describe('数据导入', () => {
  test('1. 包列表仅含社区域段（全局段不出现）', async ({ playwright }) => {
    const { api } = await newAdminContext(playwright);
    try {
      const resp = await api.get('/api/v1/admin/data-import/packages');
      expect(resp.status()).toBe(200);
      const body = (await resp.json()) as ApiResponse<{ packages?: { key: string; sections?: { key: string }[] }[] }>;
      assertOk(body, '数据包列表');
      const list = body.data?.packages ?? [];
      const pkg = list.find((p) => p.key === 'full-demo');
      expect(pkg, 'full-demo 包已注册').toBeTruthy();
      const keys = (pkg?.sections ?? []).map((s) => s.key).sort();
      expect(keys).toEqual(['banners', 'catalog', 'free_shipping', 'marketing', 'user_assets']);
      // 全局段是环境初始化种子，应用中心不可见
      for (const hidden of ['communities', 'regions', 'accounts', 'users', 'site']) {
        expect(keys).not.toContain(hidden);
      }
    } finally {
      await api.dispose();
    }
  });

  test('2. 非超管访问包列表被拒绝（HTTP 恒 200，业务码 11005）', async ({ playwright }) => {
    const info = await adminLogin(playwright.request, 'shequ', 'admin123');
    const api = await newAuthedRequest(playwright, info.access_token);
    try {
      const resp = await api.get('/api/v1/admin/data-import/packages');
      expect(resp.status()).toBe(200);
      const body = (await resp.json()) as ApiResponse<unknown>;
      expect(body.code, '无权限业务码').toBe(11005);
    } finally {
      await api.dispose();
    }
  });

  test('3. 预检返回结构化条目（开发库口径下无未知动作）', async ({ playwright }) => {
    const { api, communityId } = await newAdminContext(playwright);
    try {
      const resp = await api.post('/api/v1/admin/data-import/preview', {
        data: { community_id: communityId, package_key: 'full-demo', selected_keys: [] },
      });
      expect(resp.status()).toBe(200);
      const body = (await resp.json()) as ApiResponse<{ items?: { action: string; section_key: string }[] }>;
      assertOk(body, '数据导入预检');
      const items = body.data?.items ?? [];
      expect(items.length, '预检应返回条目').toBeGreaterThan(0);
      // 预检是只读报告：动作仅限 create/skip/depend_missing，段键仅限社区域段
      const allowedActions = new Set(['create', 'skip', 'depend_missing']);
      const allowedSections = new Set(['catalog', 'banners', 'marketing', 'free_shipping', 'user_assets']);
      for (const it of items) {
        expect(allowedActions.has(it.action), `未知动作 ${it.action}`).toBeTruthy();
        expect(allowedSections.has(it.section_key), `全局段泄漏到预检 ${it.section_key}`).toBeTruthy();
      }
    } finally {
      await api.dispose();
    }
  });

  test('4. 数据集口径为全部商品（两社区条目都在预检中）', async ({ playwright }) => {
    const { api, communityId } = await newAdminContext(playwright);
    try {
      const resp = await api.post('/api/v1/admin/data-import/preview', {
        data: { community_id: communityId, package_key: 'full-demo', selected_keys: [] },
      });
      expect(resp.status()).toBe(200);
      const payload = (await resp.json()) as ApiResponse<{ items?: { section_key: string; name: string }[] }>;
      assertOk(payload, '数据导入预检');
      const items = payload.data?.items ?? [];
      const catalogItems = items.filter((it) => it.section_key === 'catalog');
      // 数据包口径：15 分类 + 66 商品（社区1 46 + 社区2 20），全部参与预检
      expect(catalogItems.length, '目录条目应为两社区合集').toBe(81);
      const names = catalogItems.map((it) => it.name);
      expect(names, '社区1 商品在列').toContain('魔小鲜 大面筋辣条');
      expect(names, '社区2 改名商品在列').toContain('晨曦牧场 高钙纯牛奶');
      expect(names, '社区2 新增商品在列').toContain('脆波波 香辣锅巴');
    } finally {
      await api.dispose();
    }
  });

  test('5. 未指定导入会员时用户侧数据禁止导入', async ({ playwright }) => {
    const { api, communityId } = await newAdminContext(playwright);
    try {
      const resp = await api.post('/api/v1/admin/data-import/preview', {
        data: { community_id: communityId, package_key: 'full-demo', selected_keys: [] },
      });
      const payload = (await resp.json()) as ApiResponse<{ items?: { action: string; section_key: string; detail: string }[] }>;
      assertOk(payload, '数据导入预检');
      const userItems = (payload.data?.items ?? []).filter((it) => it.section_key === 'user_assets');
      expect(userItems.length, '用户侧条目应存在').toBeGreaterThan(0);
      for (const it of userItems) {
        expect(it.action, '未指定会员应判依赖缺失').toBe('depend_missing');
        expect(it.detail).toContain('未指定导入会员');
      }
    } finally {
      await api.dispose();
    }
  });

  test('6. 指定演示会员后用户侧条目可解析（无会员缺失判定）', async ({ playwright }) => {
    const { api, communityId } = await newAdminContext(playwright);
    try {
      // 指定演示用户（13800138000）后会员解析生效：user_assets 条目不再报
      // 「未指定导入会员」；其余判定（create/skip）取决于当前操作社区的库存与持券状态，不在此强断
      const resp = await api.post('/api/v1/admin/data-import/preview', {
        data: {
          community_id: communityId,
          package_key: 'full-demo',
          selected_keys: [],
          target_user_phone: '13800138000',
        },
      });
      expect(resp.status()).toBe(200);
      const payload = (await resp.json()) as ApiResponse<{ items?: { action: string; section_key: string; detail: string }[] }>;
      assertOk(payload, '数据导入预检');
      const userItems = (payload.data?.items ?? []).filter((it) => it.section_key === 'user_assets');
      expect(userItems.length, '用户侧条目应存在').toBeGreaterThan(0);
      for (const it of userItems) {
        expect(
          it.action === 'create' || it.action === 'skip',
          `指定会员后应可导入：${it.name}（${it.action}：${it.detail}）`,
        ).toBeTruthy();
        expect(it.detail, `会员缺失判定应消除：${it.name}`).not.toContain('未指定导入会员');
      }
    } finally {
      await api.dispose();
    }
  });
});
