<template>
  <n-card :title="isOverviewPage ? '社区角色' : '角色管理'">
    <n-space vertical>
      <n-alert v-if="isOverviewPage" type="info" :show-icon="false">
        跨社区角色总览（只读）：查看各社区有哪些角色、角色权限与当前任职人数。
        普通角色点「到该社区管理」，切到该社区语境后在「角色管理」中维护；
        内置「社区管理员」角色点「去社区权限设置」，其权限由该页统一控制
      </n-alert>
      <n-alert v-else type="info" :show-icon="false">
        以下为当前社区的私有角色；可分配的权限点不超出你自身的权限范围（并受社区权限上限约束）。
        内置「社区管理员」角色仅展示不可编辑，权限上限由超级管理员在「系统管理 → 社区权限」中分配
      </n-alert>
      <n-space align="center">
        <n-button v-if="!isOverviewPage" type="primary" @click="openCreate">新增角色</n-button>
        <n-select
          v-if="isOverviewPage"
          v-model:value="filterCommunityId"
          :options="communityFilters"
          clearable
          placeholder="全部社区"
          style="width: 220px"
        />
      </n-space>
      <n-data-table :columns="columns" :data="displayList" :loading="loading" />
    </n-space>
  </n-card>

  <n-modal v-model:show="showModal" title="角色" preset="card" style="width: 520px; max-width: 90vw">
    <n-space vertical>
      <n-input v-model:value="form.name" placeholder="角色名称" />
      <n-input v-model:value="form.code" placeholder="角色编码（如 staff，社区内唯一）" :disabled="isBuiltinRow" />
      <n-input v-model:value="form.description" placeholder="描述" />
      <n-select
        v-model:value="form.permissions"
        multiple
        :options="permissionOptions"
        placeholder="权限"
        :disabled="isBuiltinRow && !isSuper"
      />
      <n-alert v-if="isBuiltinRow" type="warning" :show-icon="false">
        内置社区管理员角色：不可删除，仅超管可修改；其权限即本社区权限上限，也可在「系统管理 → 社区权限」中调整
      </n-alert>
      <n-button type="primary" @click="submit">保存</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'RoleList' })
import { ref, onMounted, onActivated, computed, watch, h } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NSpace, NTag, NText } from 'naive-ui'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getRoleList, createRole, updateRole, deleteRole } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import { allPermissionOptions } from '@/constants/permissions'
import type { Role } from '@/api/types'

/**
 * 角色管理页面（多社区模型）
 * - 同一组件承载两个入口：员工与角色 → 角色管理（当前社区角色，可增删改）；
 *   系统管理 → 社区角色（超管跨社区只读总览，含所属社区、权限与任职人数）
 * - 普通管理员：管理当前社区的私有角色，权限点不超出自身权限（并受社区权限上限约束）
 * - 超管：社区语境下经顶栏社区切换可管理任意社区角色；内置社区管理员角色仅超管可改
 */
const message = useMessage()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const isSuper = computed(() => authStore.isSuperAdmin)

/** 社区角色总览页（系统管理入口）：只读，跨社区；否则为当前社区的读写管理页 */
const isOverviewPage = computed(() => route.name === 'CommunityRoleList')
const list = ref<Role[]>([])
const loading = ref(false)
/** 总览页的社区筛选（null=全部） */
const filterCommunityId = ref<number | null>(null)
const showModal = ref(false)
const isEdit = ref(false)
/** 当前编辑行（用于内置角色判定） */
const editingRow = ref<Role | null>(null)
const form = ref<Partial<Role>>({
  name: '',
  code: '',
  description: '',
  permissions: [],
})

/** 内置社区管理员角色：每个社区一条，权限即该社区权限上限 */
function isBuiltinRole(row: Role) {
  return row.code === 'community_admin' && (row.community_id ?? 0) > 0
}

/** 历史保留行（community_id = 0）：旧「全局角色」遗留数据，无所属社区、不可指派，仅超管可删 */
function isLegacyRole(row: Role) {
  return (row.community_id ?? 0) <= 0
}

/** 当前编辑行是否内置社区管理员角色 */
const isBuiltinRow = computed(() => !!editingRow.value && isBuiltinRole(editingRow.value))

/** 全量权限点清单（共享常量） */
const allOptions = allPermissionOptions

/**
 * 可勾选的权限点：
 * 超管全量；普通管理员仅自身当前社区的权限（含 "*" 通配时为全量），防止越权放大
 */
const permissionOptions = computed(() => {
  if (isSuper.value) return allOptions
  const perms = authStore.permissions
  if (perms.includes('*')) return allOptions
  const owned = new Set(perms)
  return allOptions.filter((o) => owned.has(o.value))
})

/** 总览页社区筛选项：从总览数据自身推导，免去额外的社区列表请求 */
const communityFilters = computed(() => {
  const seen = new Map<number, string>()
  list.value.forEach((row) => {
    const id = row.community_id ?? 0
    if (id > 0 && !seen.has(id)) seen.set(id, row.community_name || `社区 ${id}`)
  })
  return [...seen.entries()].map(([value, label]) => ({ value, label }))
})

const displayList = computed(() => {
  if (!isOverviewPage.value || !filterCommunityId.value) return list.value
  return list.value.filter((row) => (row.community_id ?? 0) === filterCommunityId.value)
})

/** 权限点列文案：通配符单独标注，其余按原样顿号连接（列已开启 tooltip 省略） */
function permissionText(row: Role) {
  const perms = row.permissions || []
  if (perms.includes('*')) return '全部权限（*）'
  return perms.join('、') || '—'
}

/** 类型标签：内置角色 / 遗留·全局 / 社区角色 */
function renderKind(row: Role) {
  const label = isBuiltinRole(row) ? '内置角色' : isLegacyRole(row) ? '遗留·全局' : '社区角色'
  return h(NTag, { type: isBuiltinRole(row) ? 'warning' : 'default', size: 'small' }, { default: () => label })
}

const columns = computed<DataTableColumns<Role>>(() => {
  const identity: DataTableColumns<Role> = [
    { title: 'ID', key: 'id', width: 80 },
    { title: '角色名称', key: 'name' },
    { title: '角色编码', key: 'code' },
  ]
  if (isOverviewPage.value) {
    return [
      ...identity,
      {
        title: '所属社区',
        key: 'community_id',
        width: 150,
        render: (row) =>
          isLegacyRole(row)
            ? h(NText, { depth: 3 }, { default: () => '—' })
            : row.community_name || `社区 ${row.community_id ?? 0}`,
      },
      { title: '类型', key: 'kind', width: 110, render: renderKind },
      { title: '任职人数', key: 'assignment_count', width: 90, render: (row) => row.assignment_count ?? 0 },
      { title: '权限', key: 'permissions', ellipsis: { tooltip: true }, render: permissionText },
      {
        title: '操作',
        key: 'actions',
        width: 140,
        render(row) {
          // 总览页只读：写操作回到该社区语境；历史保留行仅超管可清理
          if (isLegacyRole(row)) {
            return h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' })
          }
          if (isBuiltinRole(row)) {
            // 内置社区管理员角色的权限由「社区权限」页控制而非角色编辑，
            // 跳角色管理只会看到禁用的编辑按钮（死胡同），直接引导去正确页面
            return h(
              NButton,
              { size: 'small', type: 'primary', secondary: true, onClick: () => goCommunityPermissions(row) },
              { default: () => '去社区权限设置' },
            )
          }
          return h(NButton, { size: 'small', onClick: () => manageInCommunity(row) }, { default: () => '到该社区管理' })
        },
      },
    ]
  }
  return [
    ...identity,
    { title: '类型', key: 'kind', width: 110, render: renderKind },
    { title: '描述', key: 'description', ellipsis: { tooltip: true } },
    {
      title: '操作',
      key: 'actions',
      render(row) {
        const builtin = isBuiltinRole(row)
        return h(NSpace, null, {
          default: () => [
            // 内置社区管理员角色在业务页仅展示禁编辑：其权限由超管在「系统管理 → 社区权限」调整
            h(NButton, { size: 'small', disabled: builtin, onClick: () => edit(row) }, { default: () => '编辑' }),
            !builtin &&
              h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
          ].filter(Boolean),
        })
      },
    },
  ]
})

async function loadData() {
  loading.value = true
  try {
    const res = await getRoleList(isOverviewPage.value ? { scope: 'all' } : undefined)
    if (res.data.code === 0) {
      list.value = res.data.data
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
  }
}

/**
 * 从总览跳转到角色所属社区的管理页
 * 先切换当前社区语境（决定后续请求的 X-Community-Id），再进入「角色管理」
 * @param row 目标角色行
 */
function manageInCommunity(row: Role) {
  const communityId = row.community_id ?? 0
  if (!authStore.switchCommunity(communityId)) {
    message.error('切换社区失败，请从顶栏重新选择社区')
    return
  }
  router.push({ name: 'RoleList' })
}

/**
 * 从总览页跳转到指定社区的权限分配页（内置社区管理员角色的正确控制入口）
 * 社区语境无需切换：权限页自带社区选择器，经 query 带入目标社区
 * @param row 内置角色行
 */
function goCommunityPermissions(row: Role) {
  router.push({ name: 'CommunityPermission', query: { community_id: String(row.community_id ?? '') } })
}

function openCreate() {
  isEdit.value = false
  editingRow.value = null
  form.value = { name: '', code: '', description: '', permissions: [] }
  showModal.value = true
}

function edit(row: Role) {
  isEdit.value = true
  editingRow.value = row
  form.value = { ...row }
  showModal.value = true
}

async function submit() {
  // 角色一律社区私有：归属固定为当前社区（后端对普通管理员会强制锁定为其当前社区）
  const data = { ...form.value, community_id: authStore.currentCommunityId || 0 }
  const res = isEdit.value && form.value.id ? await updateRole(form.value.id, data) : await createRole(data)
  if (res.data.code === 0) {
    message.success('保存成功')
    showModal.value = false
    loadData()
  }
}

async function remove(row: Role) {
  const res = await deleteRole(row.id)
  if (res.data.code === 0) {
    message.success('已删除')
    loadData()
  }
}

onMounted(loadData)

// 两个入口复用同一组件实例，切换路由不会重新挂载：按入口模式重新拉取数据
watch(isOverviewPage, loadData)

// keep-alive 缓存下再次进入时 onMounted 不触发，需重新拉取（顶栏切换社区后数据也随之更新）
onActivated(loadData)
</script>
