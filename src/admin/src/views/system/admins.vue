<template>
  <n-card title="员工列表">
    <n-space vertical>
      <n-space>
        <n-input v-model:value="keyword" placeholder="用户名/姓名/手机号" clearable @keyup.enter="loadData" />
        <n-button type="primary" @click="loadData">查询</n-button>
        <n-button @click="openCreate">添加员工</n-button>
      </n-space>
      <n-alert type="info" :show-icon="false">
        列表为当前社区的任职员工。员工账号全局唯一：添加已有账号时将复用该账号（不修改其密码），并追加本社区任职——同一人可在多个社区任职。
      </n-alert>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>

  <n-modal v-model:show="showModal" :title="isEdit ? '编辑员工' : '添加员工'" preset="card" style="width: 520px; max-width: 90vw">
    <n-space vertical>
      <n-input v-model:value="form.username" placeholder="用户名（登录账号）" :disabled="isEdit" />
      <n-input
        v-if="!isEdit"
        v-model:value="form.password"
        type="password"
        show-password-on="click"
        placeholder="密码（账号已存在时忽略此项）"
      />
      <n-input v-model:value="form.real_name" placeholder="真实姓名" />
      <n-input v-model:value="form.phone" placeholder="手机号" />
      <n-select v-model:value="form.role_ids" multiple :options="roleOptions" placeholder="本社区角色（可多选）" />
      <n-select
        v-model:value="form.user_id"
        :options="memberOptions"
        :loading="memberLoading"
        filterable
        remote
        clearable
        placeholder="关联会员（选填；绑定后该会员在任职社区即履约人员）"
        @search="searchMembers"
        @clear="searchMembers('')"
      />
      <n-alert v-if="isEdit" type="info" :show-icon="false">
        关联会员在员工全部任职社区获得履约人员身份（送货上门 + 自提核销），随任职变动自动增减。
      </n-alert>
      <n-select v-if="isEdit" v-model:value="form.status" :options="statusOptions" placeholder="账号状态" />
      <n-alert v-if="assignBuiltinWarn" type="warning" :show-icon="false">
        该员工担任「社区管理员」，此角色不在上方列表中：如需更换社区管理员，请超级管理员在「系统管理 → 社区管理 → 编辑社区」中操作
      </n-alert>
      <n-button type="primary" @click="submit">保存</n-button>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'AdminList' })
import { ref, onMounted, computed, h } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { NButton, NSpace, NTag } from 'naive-ui'
import { useMessage, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getAdminList, createAdmin, updateAdmin, deleteAdmin, getRoleList } from '@/api/admin'
import { getUserList } from '@/api/user'
import { useAuthStore } from '@/stores/auth'
import type { AdminInfo, Role } from '@/api/types'

/**
 * 员工管理页面（多社区任职模型）
 * - 列表为当前社区的任职员工（随顶栏社区切换而变化）
 * - 添加员工：username 已存在则复用账号追加任职，否则新建账号
 * - 编辑：资料 + 本社区角色任职（多角色，全量覆盖）+ 关联会员绑定
 * - 关联会员：绑定后该会员在员工全部任职社区即履约人员（B 履约工作台）
 * - 移除：仅删除本社区任职，账号与其他社区任职保留
 */
const message = useMessage()
const dialog = useDialog()
const authStore = useAuthStore()

const keyword = ref('')
const list = ref<AdminInfo[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })
const showModal = ref(false)
const isEdit = ref(false)
/** 本社区角色选项（内置社区管理员角色仅超管可分配） */
const roles = ref<Role[]>([])
const form = ref<{
  id?: number
  username: string
  password: string
  real_name: string
  phone: string
  status: number
  role_ids: number[]
  /** 关联会员 ID（null=未选择） */
  user_id: number | null
}>({
  username: '',
  password: '',
  real_name: '',
  phone: '',
  status: 1,
  role_ids: [],
  user_id: null,
})
/** 编辑打开时的绑定值（未变化则不提交 user_id，保持后端"未传=不改动"语义） */
const initialUserId = ref<number | null>(null)
/** 关联会员远程搜索选项 */
const memberOptions = ref<{ label: string; value: number }[]>([])
const memberLoading = ref(false)

const statusOptions = [
  { label: '正常', value: 1 },
  { label: '禁用', value: 0 },
]

const roleOptions = computed(() =>
  roles.value
    // 内置社区管理员角色不进入员工分配下拉（指定社区管理员在「系统管理 → 社区编辑」操作）；
    // 历史保留行（community_id=0 的遗留角色）兜底过滤（后端已隔离，此处双保险）
    .filter((r) => (r.community_id ?? 0) > 0 && r.code !== 'community_admin')
    .map((r) => ({ label: r.name, value: r.id })),
)

/** 编辑表单是否携带内置社区管理员角色（提示该角色不可在此页调整） */
const assignBuiltinWarn = computed(() => {
  const builtin = roles.value.find((r) => r.code === 'community_admin' && (r.community_id ?? 0) > 0)
  return !!builtin && form.value.role_ids.includes(builtin.id)
})

const columns = computed<DataTableColumns<AdminInfo>>(() => [
  { title: 'ID', key: 'id', width: 80 },
  { title: '用户名', key: 'username' },
  { title: '真实姓名', key: 'real_name' },
  { title: '手机号', key: 'phone' },
  {
    title: '本社区角色',
    key: 'roles',
    render: (row) =>
      h(NSpace, { size: 'small' }, {
        default: () =>
          (row.roles ?? []).map((r) =>
            h(NTag, { size: 'small', type: r.code === 'community_admin' ? 'warning' : 'default' }, { default: () => r.name }),
          ),
      }),
  },
  {
    title: '关联会员',
    key: 'user_id',
    render: (row) =>
      h(NSpace, { size: 'small', align: 'center' }, {
        default: () =>
          row.user_id
            ? [
                h(NTag, { size: 'small', type: 'info', bordered: false }, { default: () => '履约' }),
                h('span', null, row.user_phone ? `${row.user_nickname || '未设置昵称'}（${row.user_phone}）` : row.user_nickname),
              ]
            : h('span', { style: 'color: #999' }, '-'),
      }),
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
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '移除' }),
        ],
      })
    },
  },
])

/** 加载本社区角色（供角色分配下拉） */
async function loadRoles() {
  const res = await getRoleList()
  if (res.data.code === 0) {
    roles.value = res.data.data || []
  }
}

/**
 * 远程搜索会员（关联会员选择器数据源）
 * @param query 关键词（昵称/手机号），空串时加载默认列表
 */
async function searchMembers(query: string) {
  memberLoading.value = true
  try {
    const res = await getUserList({ page: 1, page_size: 20, keyword: query || undefined })
    if (res.data.code === 0) {
      memberOptions.value = (res.data.data.list ?? []).map((u) => ({
        label: u.phone ? `${u.nickname || '未设置昵称'}（${u.phone}）` : u.nickname || `会员 #${u.id}`,
        value: u.id,
      }))
    }
  } finally {
    memberLoading.value = false
  }
}

async function loadData() {
  loading.value = true
  try {
    const res = await getAdminList({ page: pagination.value.page, page_size: pagination.value.pageSize, keyword: keyword.value })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈
    loading.value = false
  }
}

function openCreate() {
  isEdit.value = false
  form.value = { username: '', password: '', real_name: '', phone: '', status: 1, role_ids: [], user_id: null }
  initialUserId.value = null
  memberOptions.value = []
  searchMembers('')
  showModal.value = true
}

function edit(row: AdminInfo) {
  isEdit.value = true
  form.value = {
    id: row.id,
    username: row.username,
    password: '',
    real_name: row.real_name ?? '',
    phone: row.phone ?? '',
    status: row.status,
    role_ids: (row.roles ?? []).map((r) => r.id),
    user_id: row.user_id ?? null,
  }
  initialUserId.value = row.user_id ?? null
  // 编辑回显：用列表返回的绑定会员昵称/手机号预置选项
  memberOptions.value = []
  if (row.user_id) {
    memberOptions.value = [
      {
        label: row.user_phone ? `${row.user_nickname || '未设置昵称'}（${row.user_phone}）` : row.user_nickname || `会员 #${row.user_id}`,
        value: row.user_id,
      },
    ]
  }
  showModal.value = true
}

async function submit() {
  if (!form.value.username) {
    message.warning('请填写用户名')
    return
  }
  if (form.value.role_ids.length === 0) {
    message.warning('请至少分配一个本社区角色')
    return
  }

  if (isEdit.value && form.value.id) {
    // 绑定值有变化才提交（未传=后端不改动；0=解绑；>0=绑定/换绑）
    const payload: {
      real_name: string
      phone: string
      status: number
      role_ids: number[]
      user_id?: number
    } = {
      real_name: form.value.real_name,
      phone: form.value.phone,
      status: form.value.status,
      role_ids: form.value.role_ids,
    }
    if (form.value.user_id !== initialUserId.value) {
      // 三态语义：null（清空选择）→ 0 提交表示解绑；>0 表示绑定/换绑；不提交表示不改动
      payload.user_id = form.value.user_id ?? 0
    }
    const res = await updateAdmin(form.value.id, payload)
    if (res.data.code === 0) {
      message.success('保存成功')
      showModal.value = false
      loadData()
    }
    return
  }

  const res = await createAdmin({
    username: form.value.username,
    password: form.value.password || undefined,
    real_name: form.value.real_name,
    phone: form.value.phone,
    role_ids: form.value.role_ids,
    user_id: form.value.user_id ?? 0,
  })
  if (res.data.code === 0) {
    message.success('保存成功')
    showModal.value = false
    loadData()
  }
}

/** 移除员工：仅移除本社区任职，账号保留 */
function remove(row: AdminInfo) {
  dialog.warning({
    title: '移除员工',
    content: `确定将「${row.real_name || row.username}」移出当前社区吗？其账号与其他社区的任职不受影响。`,
    positiveText: '移除',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await deleteAdmin(row.id)
      if (res.data.code === 0) {
        message.success('已移除')
        loadData()
      }
    },
  })
}

// 角色下拉与账号列表的加载（首屏与切回本页共用）
function loadAll() {
  loadRoles()
  loadData()
}

onMounted(loadAll)
// keep-alive 缓存下切回本页不重新挂载，需在激活时刷新（其他管理员新建账号/角色后同步）
refreshOnActivated(loadAll)
</script>
