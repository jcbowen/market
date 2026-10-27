import { readFileSync } from 'node:fs'
import path from 'node:path'
import { test, expect } from '@playwright/test'

/**
 * 权限点目录两端一致性校验（P-3c，纯文件比对，不依赖运行中的服务）
 *
 * 前端目录：src/admin/src/constants/permissions.ts（菜单/权限分配页渲染来源）
 * 后端目录：src/backend/gateway/middleware/permission_catalog.go（鉴权口径权威清单）
 *
 * 两端任一新增/删除权限点而另一端漏同步时，本用例失败提醒补齐；
 * 后端与实际路由使用的一致性由 Go 侧 permission_catalog_test.go 保证
 */

const repoRoot = path.resolve(__dirname, '../..')
const tsCatalogPath = path.join(repoRoot, 'src/admin/src/constants/permissions.ts')
const goCatalogPath = path.join(repoRoot, 'src/backend/gateway/middleware/permission_catalog.go')

/** 从 TS 目录提取权限码（permissionGroups 的 value 字段） */
function parseTsCatalog(): string[] {
  const src = readFileSync(tsCatalogPath, 'utf-8')
  const codes = [...src.matchAll(/value:\s*'([a-z]+:[a-z:_]+)'/g)].map((m) => m[1])
  return [...new Set(codes)].sort()
}

/** 从 Go 目录提取 KnownPermissionCodes 片段内的权限码 */
function parseGoCatalog(): string[] {
  const src = readFileSync(goCatalogPath, 'utf-8')
  const body = src.slice(src.indexOf('KnownPermissionCodes = []string{'))
  const end = body.indexOf('}')
  const codes = [...body.slice(0, end).matchAll(/"([a-z]+:[a-z:_]+)"/g)].map((m) => m[1])
  return [...new Set(codes)].sort()
}

test('权限点目录：前端 TS 清单与后端 Go 清单一致', () => {
  const ts = parseTsCatalog()
  const go = parseGoCatalog()

  expect(ts.length, '前端目录不应为空').toBeGreaterThan(0)
  expect(go.length, '后端目录不应为空').toBeGreaterThan(0)

  const onlyInTs = ts.filter((c) => !go.includes(c))
  const onlyInGo = go.filter((c) => !ts.includes(c))

  expect(onlyInTs, '前端目录多出的权限码（后端 Go 清单需同步登记）').toEqual([])
  expect(onlyInGo, '后端目录多出的权限码（前端 constants/permissions.ts 需同步登记）').toEqual([])
})
