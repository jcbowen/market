/**
 * 环境检测工具
 */

/** 服务健康检查端点返回的 JSON 结构 */
interface HealthResp {
  code: number;
  message: string;
}

/**
 * 探测本地服务是否可用
 * - 函数名: checkServices
 * - 参数: 无（内部使用 TEST_BASE_URL 环境变量，默认 http://localhost:18080）
 * - 返回: Promise<string | null>，可用返回 null；不可用返回错误提示文案
 * - 异常: 网络错误不会抛出，统一转为返回提示文案
 * - 使用示例: const err = await checkServices(); if (err) throw new Error(err);
 */
export async function checkServices(): Promise<string | null> {
  const base = process.env.TEST_BASE_URL ?? 'http://localhost:18080';
  let resp: Response;
  try {
    resp = await fetch(`${base}/health`, { signal: AbortSignal.timeout(5000) });
  } catch {
    return `服务未启动：${base}/health 无法连接。请先在项目根目录执行 node start-all.mjs 启动环境。`;
  }
  if (!resp.ok) {
    return `健康检查异常：/health 返回 HTTP ${resp.status}，请确认服务状态。`;
  }
  const body = (await resp.json()) as HealthResp;
  if (body.code !== 0 && body.code !== undefined) {
    return `健康检查异常：/health 业务码 ${body.code}（${body.message}）。`;
  }
  return null;
}
