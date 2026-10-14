<template>
  <n-card title="社区权限">
    <n-space vertical size="large">
      <n-alert type="info" :show-icon="false">
        为社区门店分配权限：勾选项即该社区的「权限上限」，社区管理员在本社区内可再分配的权限不会超出此范围。
        保存后立即生效（运行时按上限取交集），无需调整社区内已有角色。
      </n-alert>

      <n-space align="center">
        <span>目标社区</span>
        <n-select
          v-model:value="communityId"
          :options="communityOptions"
          style="width: 280px"
          placeholder="选择社区"
          filterable
          @update:value="loadDetail"
        />
        <n-button type="primary" :loading="saving" :disabled="!communityId" @click="save">保存</n-button>
      </n-space>

      <template v-if="communityId">
        <n-card embedded :bordered="false">
          <n-space vertical>
            <n-space align="center">
              <span style="font-weight: 600">权限点</span>
              <n-button size="tiny" @click="selectAll">全选</n-button>
              <n-button size="tiny" @click="selectOperationDefault">勾选运营默认集</n-button>
              <n-button size="tiny" @click="removeSystemPerms">清除系统级权限</n-button>
            </n-space>
            <n-alert v-if="affectedRoles.length > 0" type="warning" :show-icon="false">
              收紧后以下私有角色将被上限截断（运行时按上限取交集）：
              {{ affectedRoles.map((r) => `${r.name}（${r.exceeded.join('、')}）`).join('；') }}
            </n-alert>
            <div v-for="group in permissionGroups" :key="group.label" class="perm-group">
              <div class="perm-group-title">
                {{ group.label }}
                <n-button text size="tiny" type="primary" @click="toggleGroup(group)">全选/清空本组</n-button>
              </div>
              <n-checkbox-group v-model:value="form.permissions">
                <n-space item-style="display: flex;">
                  <n-checkbox v-for="opt in group.options" :key="opt.value" :value="opt.value" :label="opt.label" />
                </n-space>
              </n-checkbox-group>
            </div>
          </n-space>
        </n-card>

        <n-card embedded :bordered="false" title="本社区角色概览">
          <n-data-table :columns="roleColumns" :data="roles" :loading="loading" size="small" />
        </n-card>
      </template>
      <n-empty v-else description="请先选择社区" style="padding: 48px 0" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'CommunityPermission' })
import { ref, onMounted, computed, h } from 'vue'
import { useRoute } from 'vue-router'
import { NButton, NSpace, NTag } from 'naive-ui'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCommunityList } from '@/api/user'
import { getCommunityPermissions, setCommunityPermissions } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import { permissionGroups, allPermissionOptions } from '@/constants/permissions'
import type { Role } from '@/api/types'

/**
 * 社区权限分配页面（超管专用，菜单 superOnly + 后端接口强制超管）
 * - 权限上限载体：该社区内置社区管理员角色（code=community_admin）的权限集
 * - 保存后运行时权限解析与其取交集，立即对社区全员生效
 * - 内置角色缺失的存量社区在首次保存时自动创建
 */
const message = useMessage()
const route = useRoute()
const authStore = useAuthStore()

const communityId = ref<number | null>(null)
const communityOptions = ref<{ label: string; value: number }[]>([])
const roles = ref<Role[]>([])
const loading = ref(false)
const saving = ref(false)
/** 内置角色的权限（服务端已保存的上限），用于与当前勾选对比提示受影响角色 */
const savedPermissions = ref<string[]>([])
const form = ref<{ permissions: string[] }>({ permissions: [] })

/** 运营默认集（与后端 DefaultCommunityAdminPermissions 对应）：全部权限点减去系统级三组 */
const systemLevelValues = ['community:view', 'community:create', 'community:update', 'community:delete', 'region:view', 'region:update']
const operationDefault = allPermissionOptions.map((o) => o.value).filter((v) => !systemLevelValues.includes(v))

/** 权限点 → 中文名映射（受影响角色提示用） */
const permissionLabelMap = new Map(allPermissionOptions.map((o) => [o.value, o.label]))

/** 收紧上限后将受影响的私有角色：角色含已保存上限有、当前勾选没有的权限点 */
const affectedRoles = computed(() => {
  const saved = new Set(savedPermissions.value)
  const current = new Set(form.value.permissions)
  const removed = [...saved].filter((p) => !current.has(p))
  if (removed.length === 0) return []
  return roles.value
    .filter((r) => (r.community_id ?? 0) > 0 && r.code !== 'community_admin')
    .map((r) => ({
      name: r.name,
      exceeded: removed.filter((p) => (r.permissions ?? []).includes(p)).map((p) => permissionLabelMap.get(p) ?? p),
    }))
    .filter((r) => r.exceeded.length > 0)
})

const roleColumns: DataTableColumns<Role> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '角色名称', key: 'name', width: 140 },
  { title: '角色编码', key: 'code', width: 150 },
  {
    title: '类型',
    key: 'kind',
    width: 90,
    render: (row) =>
      h(
        NTag,
        { type: row.code === 'community_admin' ? 'warning' : 'default', size: 'small' },
        { default: () => (row.code === 'community_admin' ? '内置（上限）' : '私有') },
      ),
  },
  {
    title: '权限点',
    key: 'permissions',
    render: (row) => {
      const perms = row.permissions ?? []
      if (perms.length === 0) return '—'
      const shown = perms.slice(0, 8).map((p) => permissionLabelMap.get(p) ?? p)
      const more = perms.length > 8 ? ` 等 ${perms.length} 项` : ''
      return h('span', null, shown.join('、') + more)
    },
  },
]

async function loadCommunities() {
  const res = await getCommunityList({ page: 1, page_size: 200 })
  if (res.data.code === 0) {
    communityOptions.value = res.data.data.list.map((c) => ({ label: c.name, value: c.id }))
    // 支持从社区管理页「权限分配」跳转带入，缺省回退当前社区
    const fromQuery = Number(route.query.community_id)
    const initial =
      fromQuery > 0
        ? fromQuery
        : communityOptions.value.find((o) => o.value === authStore.currentCommunityId)?.value ?? communityOptions.value[0]?.value
    if (initial) {
      communityId.value = initial
      await loadDetail()
    }
  }
}

async function loadDetail() {
  if (!communityId.value) return
  loading.value = true
  try {
    const res = await getCommunityPermissions(communityId.value)
    if (res.data.code === 0) {
      roles.value = res.data.data.roles ?? []
      const perms = res.data.data.permissions ?? []
      savedPermissions.value = perms
      form.value.permissions = [...perms]
    }
  } finally {
    loading.value = false
  }
}

function toggleGroup(group: { options: { value: string }[] }) {
  const values = group.options.map((o) => o.value)
  const allIn = values.every((v) => form.value.permissions.includes(v))
  if (allIn) {
    form.value.permissions = form.value.permissions.filter((v) => !values.includes(v))
  } else {
    form.value.permissions = [...new Set([...form.value.permissions, ...values])]
  }
}

function selectAll() {
  form.value.permissions = allPermissionOptions.map((o) => o.value)
}

/** 勾选运营默认集：全量权限点减去系统级三组（社区/行政区划） */
function selectOperationDefault() {
  form.value.permissions = [...operationDefault]
}

/** 从当前勾选中移除系统级权限（社区 community:*、行政区划 region:*），保持系统管理为超管专属 */
function removeSystemPerms() {
  form.value.permissions = form.value.permissions.filter((v) => !systemLevelValues.includes(v))
}

async function save() {
  if (!communityId.value) return
  saving.value = true
  try {
    const res = await setCommunityPermissions(communityId.value, form.value.permissions)
    if (res.data.code === 0) {
      message.success('已保存，权限上限立即生效')
      await loadDetail()
    }
  } finally {
    saving.value = false
  }
}

onMounted(loadCommunities)
</script>

<style lang="scss" scoped>
.perm-group:not(:first-child) {
  margin-top: 12px;
}
.perm-group-title {
  font-weight: 600;
  margin-bottom: 6px;
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>
