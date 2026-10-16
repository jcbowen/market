import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, assertOk, type ApiResponse } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 26：杂项功能（图片上传 / geoip 定位 / 常用应用偏好）
 * 覆盖：C 端与管理端 multipart 图片上传（真实 PNG buffer）→ 响应含 attach_id/url
 *       → 上传文件可经静态路由访问 → 未登录上传被拒
 *       → geoip locate（本机回环地址查库无果，断言接口 200 且结构合法）
 *       → 常用应用偏好保存/回读/还原（覆盖式语义）
 */

interface UploadResult {
  attach_id: number | string; // proto int64 序列化可能为字符串
  url: string;
  remote_path: string;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let originalAppKeys: string[] | null = null;

/** 1x1 白色 PNG（合法图片，满足服务端魔数校验） */
const PNG_1PX = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
);

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  // 记录原偏好，测后还原
  try {
    const resp = await adminApi.get('/api/v1/admin/app-preferences');
    const body = (await resp.json()) as ApiResponse<{ app_keys: string[] }>;
    if (body.code === 0 && body.data) originalAppKeys = body.data.app_keys ?? [];
  } catch {
    originalAppKeys = null;
  }
});

test.afterAll(async () => {
  try {
    if (originalAppKeys) {
      await adminApi.put('/api/v1/admin/app-preferences', { data: { app_keys: originalAppKeys } });
    }
  } catch {
    /* 还原失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('图片上传', () => {
  test('1. 管理端 multipart 上传 PNG 成功', async () => {
    const resp = await adminApi.post('/api/v1/admin/upload/image', {
      multipart: {
        file: {
          name: 'auto-test.png',
          mimeType: 'image/png',
          buffer: PNG_1PX,
        },
      },
    });
    const body = (await resp.json()) as ApiResponse<UploadResult>;
    expect(body.code, `上传失败: ${body.message}`).toBe(0);
    expect(String(body.data.attach_id)).not.toBe('0');
    expect(body.data.url).toContain('/uploads/');
    expect(body.data.remote_path).toContain('.png');
  });

  test('2. 上传文件可经静态路由访问', async () => {
    const resp = await adminApi.post('/api/v1/admin/upload/image', {
      multipart: {
        file: { name: 'auto-test-2.png', mimeType: 'image/png', buffer: PNG_1PX },
      },
    });
    const body = (await resp.json()) as ApiResponse<UploadResult>;
    expect(body.code).toBe(0);
    // URL 是相对路径 /uploads/...，直接 GET 网关静态路由
    const fileResp = await adminApi.get(body.data.url);
    expect(fileResp.status(), `静态文件 ${body.data.url} 应可访问`).toBe(200);
    const buf = await fileResp.body();
    expect(buf.length).toBeGreaterThan(0);
  });

  test('3. C 端登录态上传图片成功', async ({ playwright }) => {
    const info = await clientLogin(playwright.request, uniquePhone());
    const buyerApi = await newAuthedRequest(playwright, info.access_token);
    try {
      const resp = await buyerApi.post('/api/v1/upload/image', {
        multipart: {
          file: { name: 'auto-test-c.png', mimeType: 'image/png', buffer: PNG_1PX },
        },
      });
      const body = (await resp.json()) as ApiResponse<UploadResult>;
      expect(body.code, `C 端上传失败: ${body.message}`).toBe(0);
      expect(body.data.url).toContain('/uploads/');
    } finally {
      await buyerApi.dispose();
    }
  });

  test('4. 未登录上传被拒', async ({ playwright }) => {
    const anon = await playwright.request.newContext();
    try {
      const resp = await anon.post('/api/v1/upload/image', {
        multipart: {
          file: { name: 'anon.png', mimeType: 'image/png', buffer: PNG_1PX },
        },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '未登录上传应被拒').not.toBe(0);
    } finally {
      await anon.dispose();
    }
  });

  test('5. 非图片文件被拒（txt 伪装）', async () => {
    const resp = await adminApi.post('/api/v1/admin/upload/image', {
      multipart: {
        file: { name: 'auto-test.txt', mimeType: 'text/plain', buffer: Buffer.from('not an image') },
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '非图片内容应被拒').not.toBe(0);
  });
});

test.describe.serial('geoip 与应用偏好', () => {
  test('6. geoip locate 接口 200 且结构合法', async () => {
    const resp = await adminApi.get('/api/v1/geoip/locate');
    expect(resp.status()).toBe(200);
    const body = (await resp.json()) as ApiResponse<{ longitude?: number; latitude?: number } | null>;
    expect(body.code).toBe(0);
    // 本机回环地址查库无果时 data 为 null，属合法返回；有结果时必含经纬度
    if (body.data) {
      expect(typeof body.data.longitude).toBe('number');
      expect(typeof body.data.latitude).toBe('number');
    }
  });

  test('7. 常用应用偏好保存后回读一致（覆盖式）', async () => {
    const keys = ['order_create', 'member_register', 'refund_handle'];
    const put = await adminApi.put('/api/v1/admin/app-preferences', { data: { app_keys: keys } });
    const putBody = (await put.json()) as ApiResponse;
    expect(putBody.code, `保存偏好失败: ${putBody.message}`).toBe(0);

    const resp = await adminApi.get('/api/v1/admin/app-preferences');
    const body = (await resp.json()) as ApiResponse<{ app_keys: string[] }>;
    expect(body.code).toBe(0);
    expect(body.data.app_keys).toEqual(keys);
  });

  test('8. 保存空数组覆盖为空', async () => {
    const put = await adminApi.put('/api/v1/admin/app-preferences', { data: { app_keys: [] } });
    const putBody = (await put.json()) as ApiResponse;
    expect(putBody.code).toBe(0);
    const resp = await adminApi.get('/api/v1/admin/app-preferences');
    const body = (await resp.json()) as ApiResponse<{ app_keys: string[] }>;
    // 空切片 JSON 序列化为 null，语义等同空列表
    expect(body.data.app_keys ?? []).toEqual([]);
  });
});
