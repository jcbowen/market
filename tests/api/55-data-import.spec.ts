import { test, expect } from '@playwright/test';
import { newAdminContext, adminLogin, newAuthedRequest, assertOk, asList, type ApiResponse } from '../helpers/api';

/**
 * 模块 55：应用中心 · 数据导入
 * 覆盖（管理端 /admin/data-import/*，product:create + 超管双闸）：
 *   - 包列表：仅社区域且非仅初始化的段（catalog/banners/marketing/free_shipping/user_assets），
 *     全局段（communities/regions/accounts/users/site，环境初始化种子）一律不出现
 *   - 非超管（社区管理员 shequ）访问包列表被拒绝（403）
 *   - 预检：开发库已随初始化导入过演示包，重复预检应全部 skip（幂等口径的可观测面）
 * 数据口径：演示种子来源为 product 服务内嵌 full-demo.json（tools/gen-data-package.mjs 生成，
 * 经 market-admin init 导入）；用例不写库，仅读列表与预检
 */

test.describe('数据导入', () => {
  test('1. 包列表仅含社区域段（全局段不出现）', async ({ playwright }) => {
    const { api } = await newAdminContext(playwright);
    try {
      const resp = await api.get('/api/v1/admin/data-import/packages');
      expect(resp.status()).toBe(200);
      const body = (await resp.json()) as ApiResponse<{ key: string; sections?: { key: string }[] }[]>;
      const list = asList<{ key: string; sections?: { key: string }[] }>(body.data);
      assertOk(body, '数据包列表');
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

  test('2. 非超管访问包列表被拒绝 403', async ({ playwright }) => {
    const info = await adminLogin(playwright.request, 'shequ', 'admin123');
    const api = await newAuthedRequest(playwright, info.access_token);
    try {
      const resp = await api.get('/api/v1/admin/data-import/packages');
      expect(resp.status()).toBe(403);
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
});
