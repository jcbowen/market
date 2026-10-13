<template>
  <n-card title="管理员列表">
    <n-space vertical>
      <n-space>
        <n-input v-model:value="keyword" placeholder="用户名/手机号" clearable />
        <n-button type="primary" @click="loadData">查询</n-button>
        <n-button @click="openCreate">新增管理员</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>

  <n-modal v-model:show="showModal" title="新增管理员" preset="card" style="width: 500px; max-width: 90vw">
    <n-space vertical>
      <n-input v-model:value="form.username" placeholder="用户名" :disabled="isEdit" />
      <n-input v-model:value="form.password" type="password" placeholder="密码（编辑时留空表示不修改）" />
      <n-input v-model:value="form.real_name" placeholder="真实姓名" />
      <n-input v-model:value="form.phone" placeholder="手机号" />
      <n-select v-model:value="form.role_id" :options="roleOptions" placeholder="角色" />
      <!-- 多社区绑定：决定该管理员可切换与访问的社区范围，至少绑定一个 -->
      <n-select
        v-model:value="form.community_ids"
        :options="communityOptions"
        multiple
        placeholder="所属社区（可多选）"
      />
      <n-button type="primary" @click="submit">保存</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'AdminList' })
import { ref, onMounted, computed, h } from 'vue'
import { NButton, NSpace, NTag } from 'naive-ui'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getAdminList, createAdmin, updateAdmin, deleteAdmin, getRoleList } from '@/api/admin'
import { getCommunityList } from '@/api/user'
import type { AdminInfo, Role, Community } from '@/api/types'

/**
 * 管理员列表页面
 * 支持管理员的增删改查与多社区绑定管理
 */
const message = useMessage()
const keyword = ref('')
const list = ref<AdminInfo[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })
const showModal = ref(false)
const roleOptions = ref<Array<{ label: string; value: number }>>([])
/** 社区列表（绑定选项与名称展示共用） */
const communities = ref<Community[]>([])
const isEdit = ref(false)
const form = ref<Partial<AdminInfo & { password: string }>>({
  username: '',
  password: '',
  real_name: '',
  phone: '',
  role_id: undefined,
  community_ids: [],
  status: 1,
})

/** 社区绑定下拉选项 */
const communityOptions = computed(() =>
  communities.value.map((c) => ({ label: c.name, value: c.id })),
)

/** 社区 ID → 名称映射 */
const communityNameMap = computed(() => {
  const map = new Map<number, string>()
  for (const c of communities.value) map.set(c.id, c.name)
  return map
})

/** 渲染管理员绑定的社区名称串 */
function renderCommunityNames(ids?: number[]): string {
  if (!ids || ids.length === 0) return '—'
  return ids.map((id) => communityNameMap.value.get(id) ?? `社区 #${id}`).join('、')
}

const columns: DataTableColumns<AdminInfo> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '用户名', key: 'username' },
  { title: '真实姓名', key: 'real_name' },
  { title: '手机号', key: 'phone' },
  { title: '角色', key: 'role_name' },
  {
    title: '所属社区',
    key: 'community_ids',
    width: 200,
    ellipsis: { tooltip: true },
    render: (row) => renderCommunityNames(row.community_ids),
  },
  {
    title: '状态',
    key: 'status',
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '正常' : '禁用') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => edit(row) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

async function loadRoles() {
  const res = await getRoleList()
  if (res.data.code === 0) {
    roleOptions.value = (res.data.data || []).map((r: Role) => ({ label: r.name, value: r.id }))
  }
}

/** 加载社区列表：绑定选项 + 列表页名称展示 */
async function loadCommunities() {
  try {
    const res = await getCommunityList({ page: 1, page_size: 200 })
    if (res.data.code === 0) {
      communities.value = res.data.data.list
    }
  } catch {
    // 拉取失败时选项降级为空，不影响列表主体
  }
}

async function loadData() {
  loading.value = true
  const res = await getAdminList({ page: pagination.value.page, page_size: pagination.value.pageSize, keyword: keyword.value })
  loading.value = false
  if (res.data.code === 0) {
    list.value = res.data.data.list
    pagination.value.itemCount = res.data.data.total
  }
}

function openCreate() {
  isEdit.value = false
  form.value = {
    username: '',
    password: '',
    real_name: '',
    phone: '',
    role_id: undefined,
    community_ids: [],
    status: 1,
  }
  showModal.value = true
}

function edit(row: AdminInfo) {
  isEdit.value = true
  form.value = { ...row, community_ids: [...(row.community_ids ?? [])], password: '' }
  showModal.value = true
}

async function submit() {
  if (!form.value.community_ids || form.value.community_ids.length === 0) {
    message.warning('请至少绑定一个社区')
    return
  }
  const data = { ...form.value }
  const id = data.id
  // 编辑时密码留空表示不修改，避免把空密码传给后端
  if (id && !data.password) {
    delete data.password
  }
  const res = id ? await updateAdmin(id, data) : await createAdmin(data as any)
  if (res.data.code === 0) {
    message.success('保存成功')
    showModal.value = false
    loadData()
  }
}

async function remove(row: AdminInfo) {
  const res = await deleteAdmin(row.id)
  if (res.data.code === 0) loadData()
}

onMounted(() => {
  loadRoles()
  loadCommunities()
  loadData()
})
</script>
