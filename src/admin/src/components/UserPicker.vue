<template>
  <n-modal
    :show="show"
    :title="title"
    preset="card"
    style="width: 640px; max-width: 92vw"
    @update:show="emit('update:show', $event)"
  >
    <div class="toolbar">
      <n-input
        v-model:value="keyword"
        placeholder="按昵称或手机号搜索"
        clearable
        class="keyword"
      >
        <template #prefix>
          <n-icon><SearchOutline /></n-icon>
        </template>
      </n-input>
      <n-button v-if="registerable" size="small" @click="showRegister = true">
        <template #icon><n-icon><PersonAddOutline /></n-icon></template>
        新增会员
      </n-button>
    </div>

    <!-- 多选模式：已选标签回显 -->
    <div v-if="multiple && selectedUsers.length" class="picked-tags">
      <span class="label">已选：</span>
      <n-tag
        v-for="u in selectedUsers"
        :key="u.id"
        size="small"
        closable
        type="primary"
        @close="removePicked(u)"
      >
        {{ u.nickname }}（{{ maskPhone(u.phone) }}）
      </n-tag>
    </div>

    <n-spin :show="loading">
      <n-empty v-if="!list.length && !loading" description="暂无会员" class="empty" />
      <div v-else class="user-list">
        <div
          v-for="u in list"
          :key="u.id"
          class="user-row"
          :class="{ selected: isPicked(u.id), disabled: isDisabled(u) }"
          @click="onPick(u)"
        >
          <n-checkbox v-if="multiple" :checked="isPicked(u.id)" @click.stop @update:checked="() => onPick(u)" />
          <n-radio v-else :checked="isPicked(u.id)" @click.stop @update:checked="() => onPick(u)" />
          <div class="info">
            <span class="nickname">{{ u.nickname || '未设置昵称' }}</span>
            <span class="phone">{{ maskPhone(u.phone) }}</span>
          </div>
          <n-tag v-if="u.status === 0" size="small" type="error">已拉黑</n-tag>
        </div>
      </div>
    </n-spin>

    <div class="footer">
      <span v-if="multiple" class="picked-count">已选 {{ pickedIds.size }} 人</span>
      <n-pagination
        v-model:page="page"
        :item-count="total"
        :page-size="pageSize"
        @update:page="loadList"
      />
      <n-button
        v-if="multiple"
        type="primary"
        :disabled="!pickedIds.size"
        @click="onConfirm"
      >
        确认选择
      </n-button>
    </div>

    <!-- 快速注册会员弹窗（registerable 开启时可用；后端幂等，已注册手机号自动复用并关联本社区） -->
    <n-modal
      :show="showRegister"
      preset="card"
      title="新增会员"
      style="width: 400px; max-width: 92vw"
      @update:show="(v: boolean) => (showRegister = v)"
    >
      <n-form label-placement="left" label-width="64">
        <n-form-item-row label="手机号" required>
          <n-input
            v-model:value="regPhone"
            placeholder="11 位手机号"
            maxlength="11"
            :input-props="{ inputmode: 'numeric' }"
            @keydown.enter="submitRegister"
          />
        </n-form-item-row>
        <n-form-item-row label="昵称">
          <n-input v-model:value="regNickname" placeholder="选填，缺省为「会员+尾号4位」" maxlength="30" />
        </n-form-item-row>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showRegister = false">取消</n-button>
          <n-button type="primary" :loading="registering" @click="submitRegister">注册</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useMessage } from 'naive-ui'
import { PersonAddOutline, SearchOutline } from '@vicons/ionicons5'
import { createUser, getUserList } from '@/api/user'
import type { User } from '@/api/types'

/**
 * 会员选择弹窗组件（公共组件，优惠券手动发放 / 收银台代客下单共用）
 * 分页浏览当前社区会员（数据源 getUserList，按当前社区语境过滤），
 * 支持昵称/手机号模糊搜索（300ms 防抖）。
 * 选择交互：单选模式点击行即回传并关闭；多选模式点选/取消（跨页保留），底部确认批量回传。
 * 已拉黑会员置灰且不可选。
 * 开启 registerable 时提供「新增会员」快速注册（手机号幂等：已注册自动复用并关联本社区），
 * 注册成功单选模式直接选中回传，多选模式加入已选列表。
 */

const props = withDefaults(
  defineProps<{
    /** 弹窗显示（v-model:show） */
    show: boolean
    /** 是否多选模式 */
    multiple?: boolean
    /** 是否提供「新增会员」快速注册入口 */
    registerable?: boolean
    /** 注册场景（仅影响后端会员-社区关联来源标记）：admin 后台 / pos 收银台 */
    registerScene?: 'admin' | 'pos'
    /** 弹窗标题 */
    title?: string
    /** 多选模式打开时的已选会员列表，用于标记选中态 */
    selected?: User[]
    /** 禁用选择的会员 ID 列表（如已存在于名单中的项） */
    disabledIds?: number[]
  }>(),
  {
    multiple: false,
    registerable: false,
    registerScene: 'admin',
    title: '选择会员',
    selected: () => [],
    disabledIds: () => [],
  }
)

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  /** 单选模式点击行回传 */
  (e: 'select', user: User): void
  /** 多选模式确认回传 */
  (e: 'confirm', users: User[]): void
}>()

const PAGE_SIZE = 10
const pageSize = PAGE_SIZE

const list = ref<User[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const keyword = ref('')
const selectedUsers = ref<User[]>([])
const disabledIdSet = ref<Set<number>>(new Set(props.disabledIds))

let keywordTimer: ReturnType<typeof setTimeout> | null = null

watch(
  () => props.show,
  (v) => {
    if (v) init()
  }
)

watch(
  () => props.disabledIds,
  (v) => {
    disabledIdSet.value = new Set(v)
  }
)

watch(keyword, () => {
  // 关键字输入 300ms 防抖后自动搜索
  if (keywordTimer) clearTimeout(keywordTimer)
  keywordTimer = setTimeout(() => {
    keywordTimer = null
    reload()
  }, 300)
})

/**
 * 打开弹窗时的初始化：重置筛选与页码，多选模式带入已选项
 */
function init() {
  page.value = 1
  keyword.value = ''
  selectedUsers.value = props.multiple ? [...props.selected] : []
  void loadList()
}

/**
 * 重置到第一页并加载列表
 */
function reload() {
  page.value = 1
  void loadList()
}

/**
 * 加载会员分页列表（当前社区语境）
 */
async function loadList() {
  loading.value = true
  try {
    const params: Parameters<typeof getUserList>[0] = { page: page.value, page_size: PAGE_SIZE }
    if (keyword.value.trim()) {
      params.keyword = keyword.value.trim()
    }
    const res = await getUserList(params)
    // proto3 omitempty：空列表时 total/list 字段可能缺省，需兜底
    list.value = res.data.data.list || []
    total.value = res.data.data.total || 0
  } finally {
    loading.value = false
  }
}

const pickedIds = computed(() => new Set(selectedUsers.value.map((u) => u.id)))

/**
 * 是否已选
 */
function isPicked(id: number): boolean {
  return pickedIds.value.has(id)
}

/**
 * 是否禁用选择
 */
function isDisabled(u: User): boolean {
  return u.status === 0 || disabledIdSet.value.has(u.id)
}

/**
 * 手机号脱敏展示：138****1234
 */
function maskPhone(phone: string): string {
  if (!phone || phone.length < 7) return phone
  return `${phone.slice(0, 3)}****${phone.slice(-4)}`
}

/**
 * 点击行：单选模式回传并关闭；多选模式切换选中态
 */
function onPick(u: User) {
  if (isDisabled(u)) return
  if (!props.multiple) {
    emit('select', u)
    emit('update:show', false)
    return
  }
  if (pickedIds.value.has(u.id)) {
    selectedUsers.value = selectedUsers.value.filter((x) => x.id !== u.id)
  } else {
    selectedUsers.value = [...selectedUsers.value, u]
  }
}

/**
 * 移除已选标签
 */
function removePicked(u: User) {
  selectedUsers.value = selectedUsers.value.filter((x) => x.id !== u.id)
}

/**
 * 多选模式确认：回传全部已选会员并关闭
 */
function onConfirm() {
  emit('confirm', [...selectedUsers.value])
  emit('update:show', false)
}

// --- 快速注册会员 ---
const message = useMessage()
const showRegister = ref(false)
const regPhone = ref('')
const regNickname = ref('')
const registering = ref(false)

watch(showRegister, (v) => {
  if (v) {
    regPhone.value = ''
    regNickname.value = ''
  }
})

/**
 * 提交注册：手机号格式校验后调用幂等注册接口。
 * 新注册 → 提示成功；复用既有会员 → 提示已自动关联本社区。
 * 单选模式注册后直接选中回传并关闭；多选模式加入已选列表并刷新列表页。
 */
async function submitRegister() {
  const phone = regPhone.value.trim()
  if (!/^1\d{10}$/.test(phone)) {
    message.warning('请输入正确的 11 位手机号')
    return
  }
  registering.value = true
  try {
    const res = await createUser({ phone, nickname: regNickname.value.trim() || undefined, scene: props.registerScene })
    if (res.data.code !== 0 || !res.data.data) return
    const user = res.data.data
    message.success(user.is_new ? `已注册会员 ${phone}` : `该手机号已是会员，已自动关联本社区`)
    showRegister.value = false
    if (props.multiple) {
      if (!pickedIds.value.has(user.id)) {
        selectedUsers.value = [...selectedUsers.value, user]
      }
      void loadList()
    } else {
      emit('select', user)
      emit('update:show', false)
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    registering.value = false
  }
}
</script>

<style lang="scss" scoped>
.toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 12px;

  .keyword {
    width: 260px;
  }
}

.picked-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  margin-bottom: 12px;

  .label {
    font-size: 13px;
    color: #666;
  }
}

.empty {
  padding: 40px 0;
}

.user-list {
  max-height: 380px;
  overflow-y: auto;
  border: 1px solid #efeff5;
  border-radius: 6px;
}

.user-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-bottom: 1px solid #f3f3f7;
  cursor: pointer;
  transition: background 0.15s;

  &:last-child {
    border-bottom: none;
  }

  &:hover {
    background: #f7f9fa;
  }

  &.selected {
    background: rgba(24, 160, 88, 0.06);
  }

  &.disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .info {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;

    .nickname {
      font-size: 14px;
      color: #333;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .phone {
      font-size: 13px;
      color: #888;
      font-variant-numeric: tabular-nums;
    }
  }
}

.footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 14px;

  .picked-count {
    margin-right: auto;
    font-size: 13px;
    color: #18a058;
  }
}
</style>
