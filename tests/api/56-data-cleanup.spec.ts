import { test, expect } from '@playwright/test';
import { adminLogin, newAdminContext, newAuthedRequest, assertOk, type ApiResponse } from '../helpers/api';

/**
 * 模块 56：应用中心 · 数据清理
 * 覆盖（管理端 /admin/data-cleanup/*，product:create + 超管双闸）：
 *   - 预检：返回 9 个数据域（逆依赖顺序）且行数 ≥ 0，附件域 file_count = row_count
 *   - 非超管（社区管理员 shequ）访问预检被拒绝（业务码 11005）
 *   - 确认文案不匹配：执行被服务端拒绝（业务码 10001），数据不被清理
 * 安全口径：本模块为不可逆破坏性操作，用例一律不携带正确确认文案执行，
 * 避免清空开发/测试库业务数据
 */

const EXPECTED_SECTIONS = [
  'refunds',
  'orders',
  'payments',
  'inventory',
  'user_assets',
  'marketing',
  'catalog',
  'attachments',
  'users',
];

test.describe('数据清理', () => {
  test('1. 预检返回 9 个数据域（附件域文件数与记录数一致）', async ({ playwright }) => {
    const { api } = await newAdminContext(playwright);
    try {
      const resp = await api.get('/api/v1/admin/data-cleanup/preview');
      expect(resp.status()).toBe(200);
      const body = (await resp.json()) as ApiResponse<{ sections?: { key: string; row_count: number; file_count: number }[] }>;
      assertOk(body, '数据清理预检');
      const sections = body.data?.sections ?? [];
      expect(sections.map((s) => s.key)).toEqual(EXPECTED_SECTIONS);
      for (const sec of sections) {
        expect(sec.row_count, `${sec.key} 行数应 ≥ 0`).toBeGreaterThanOrEqual(0);
        if (sec.key === 'attachments') {
          expect(sec.file_count, '附件域文件数应等于记录数').toBe(sec.row_count);
        } else {
          expect(sec.file_count, `${sec.key} 非附件域无文件数`).toBe(0);
        }
      }
    } finally {
      await api.dispose();
    }
  });

  test('2. 非超管访问预检被拒绝（HTTP 恒 200，业务码 11005）', async ({ playwright }) => {
    const info = await adminLogin(playwright.request, 'shequ', 'admin123');
    const api = await newAuthedRequest(playwright, info.access_token);
    try {
      const resp = await api.get('/api/v1/admin/data-cleanup/preview');
      expect(resp.status()).toBe(200);
      const body = (await resp.json()) as ApiResponse<unknown>;
      expect(body.code, '无权限业务码').toBe(11005);
    } finally {
      await api.dispose();
    }
  });

  test('3. 确认文案不匹配时执行被拒绝（数据不被清理）', async ({ playwright }) => {
    const { api } = await newAdminContext(playwright);
    try {
      const resp = await api.post('/api/v1/admin/data-cleanup/run', {
        data: { confirm_text: '错误的确认文案' },
      });
      expect(resp.status()).toBe(200);
      const body = (await resp.json()) as ApiResponse<unknown>;
      expect(body.code, '参数错误业务码').toBe(10001);
      // 兜底防线：无论如何断言返回体不携带清理统计（即未执行清理）
      expect(body.data, '未执行清理').toBeUndefined();
    } finally {
      await api.dispose();
    }
  });
});
