import { checkServices } from './env';

/**
 * Playwright 全局初始化钩子
 * - 函数名: globalSetup（Playwright 约定导出）
 * - 参数: 无
 * - 返回: Promise<void>
 * - 异常: 服务不可用时抛出错误，Playwright 直接终止并展示提示
 * - 使用示例: 由 playwright.config.ts 的 globalSetup 字段自动调用
 */
export default async function globalSetup(): Promise<void> {
  const err = await checkServices();
  if (err) throw new Error(err);
  console.log('[global-setup] 服务健康检查通过，开始执行测试');
}
