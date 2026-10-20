import { request } from '@playwright/test';

import { ADMIN_PASSWORD, ADMIN_USERNAME, adminLogin, type ApiResponse } from './api';
import { clearTestUserRegistry, readTestUserRegistry } from './data';

/**
 * Playwright 全局回收钩子（B5：测试数据回收）
 *
 * 做的事：把本轮 clientLogin 登记的 C 端会员逐个停用（PUT /admin/users/:id/status {status:0}）。
 * 为什么不是删除：会员没有删除接口，后台提供的生命周期只有停用；停用后账号既不能再登录，
 * 也不会再被统计/列表用例命中，脏行留在库里等重建开发库（init.dev.sql）时统一清掉。
 *
 * - 函数名: globalTeardown（Playwright 约定导出）
 * - 参数: options — Playwright 传入的是 globalSetup 的返回值（本项目 globalSetup 无返回值，
 *          故为 undefined；健康检查失败时 globalSetup 直接抛错、本轮根本不会跑到这里）
 * - 返回: Promise<void>
 * - 异常: 不抛出。回收属尽力而为：服务已停或单个会员停用失败只打印告警，
 *         不能让「用例全绿但回收报错」变成红色误导调用方
 * - 使用示例: 由 playwright.config.ts 的 globalTeardown 字段自动调用
 */
export default async function globalTeardown(options?: { errors?: unknown[] }): Promise<void> {
  const users = readTestUserRegistry();
  if (users.length === 0) return;
  // 全局阶段（如健康检查）已报错说明服务不可用，登录必然失败，直接保留清单交给下轮
  if (options?.errors?.length) {
    console.warn(`[global-teardown] 本轮存在 ${options.errors.length} 条全局错误，跳过 ${users.length} 个会员的停用，登记表保留待下次回收`);
    return;
  }

  const ctx = await request.newContext({
    baseURL: process.env.TEST_BASE_URL ?? 'http://localhost:18080',
  });
  let disabled = 0;
  const skipped: string[] = [];
  const failed: string[] = [];
  try {
    const info = await adminLogin(ctx, ADMIN_USERNAME, ADMIN_PASSWORD);
    for (const rec of users) {
      if (!rec.community_id) {
        skipped.push(`会员 ${rec.user_id}（${rec.phone}）无社区语境，未停用`);
        continue;
      }
      const resp = await ctx.put(`/api/v1/admin/users/${rec.user_id}/status`, {
        data: { status: 0 },
        headers: { Authorization: `Bearer ${info.access_token}`, 'X-Community-Id': String(rec.community_id) },
      });
      const body = (await resp.json().catch(() => null)) as ApiResponse | null;
      if (body?.code === 0) disabled++;
      else failed.push(`会员 ${rec.user_id}（${rec.phone}）停用失败 HTTP ${resp.status()} code=${body?.code} message=${body?.message}`);
    }
  } catch (e) {
    failed.push(`管理端登录或停用中断（服务可能已停止）：${(e as Error).message}`);
  } finally {
    await ctx.dispose();
  }

  console.log(
    `[global-teardown] 本轮登记会员 ${users.length} 个：停用 ${disabled}，跳过 ${skipped.length}，失败 ${failed.length}`,
  );
  for (const line of [...skipped, ...failed]) console.warn(`[global-teardown] ${line}`);
  // 登记成功停用即清空；仍有未停用项时保留清单，供下一轮或人工核对
  if (skipped.length + failed.length === 0) clearTestUserRegistry();
}
