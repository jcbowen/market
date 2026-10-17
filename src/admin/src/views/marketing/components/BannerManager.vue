<template>
  <div class="banner-manager">
    <n-space vertical>
      <n-space wrap>
        <n-input v-model:value="searchTitle" placeholder="标题" clearable />
        <n-button type="primary" @click="loadBanners">查询</n-button>
        <n-button type="primary" @click="openModal()">{{ isSystemScope ? '新增全局 Banner' : '新增 Banner' }}</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="filteredBanners" :loading="loading" scroll-x="900" />
    </n-space>

    <n-modal v-model:show="showModal" title="Banner" preset="card" style="width: 560px; max-width: 90vw" scrollable>
      <n-form :model="form" label-placement="top">
        <n-form-item label="标题">
          <n-input v-model:value="form.title" placeholder="请输入 Banner 标题" />
        </n-form-item>
        <n-form-item label="图片地址">
          <ImageUploadInput v-model:value="form.image_url" placeholder="图片 URL 或点击上传" hint="建议尺寸 750×320（21:9）" />
        </n-form-item>
        <n-form-item label="链接地址">
          <LinkSelector v-model:value="form.link_url" placeholder="请输入或选择跳转链接" />
        </n-form-item>
        <n-form-item label="展示位置">
          <n-select v-model:value="form.position" :options="positionOptions" placeholder="请选择展示位置" />
        </n-form-item>
        <n-form-item label="适用社区">
          <!-- 系统管理语境锁定为全平台通用，社区业务语境可选（不选=当前社区语境） -->
          <n-input v-if="isSystemScope" value="全平台通用" disabled />
          <n-select
            v-else
            v-model:value="form.community_id"
            :options="communityOptions"
            placeholder="请选择适用社区"
            :loading="communityLoading"
          />
        </n-form-item>
        <n-form-item label="排序">
          <n-input-number v-model:value="form.sort" placeholder="数值越小越靠前" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="状态">
          <n-switch v-model:value="form.status" :checked-value="1" :unchecked-value="2">
            <template #checked>启用</template>
            <template #unchecked>禁用</template>
          </n-switch>
        </n-form-item>
        <n-form-item label="有效期">
          <n-date-picker v-model:value="timeRange" type="datetimerange" clearable style="width: 100%;" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="modalLoading" @click="saveBanner">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'BannerList' })
import { h, onMounted, reactive, ref, computed } from 'vue'
import { useMessage } from 'naive-ui'
import { NButton, NSpace, NTag, NImage } from 'naive-ui'
import ImageUploadInput from '@/components/ImageUploadInput.vue'
import LinkSelector from '@/components/LinkSelector.vue'
import type { DataTableColumns } from 'naive-ui'
import { getBannerList, createBanner, updateBanner, deleteBanner } from '@/api/marketing'
import { getCommunityList } from '@/api/user'
import { useAuthStore } from '@/stores/auth'
import type { Banner } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 组件语境：
 * - community（默认）：社区业务语境，管理当前社区 banner，全局 banner 只读展示
 * - system：系统管理语境，仅管理全局 banner（community_id=0，适用社区锁定"全平台通用"）
 */
const props = defineProps<{ scope?: 'community' | 'system' }>()

const isSystemScope = computed(() => props.scope === 'system')

const message = useMessage()
const authStore = useAuthStore()

// C 端旧首页已下线（分类页升级为首页），position=1 不再有任何展示位，仅保留选项供历史数据显示
const positionOptions = [
  { label: '分类页', value: 2 },
]

// 适用社区下拉：全部社区(0) + 全部启用的社区
const communityOptions = ref<{ label: string; value: number }[]>([
  { label: '全部社区', value: 0 },
])
const communityLoading = ref(false)
const communityNameMap = ref<Record<number, string>>({})

/**
 * 加载社区列表（下拉选项 + 表格列回显）
 * 仅在有 community:view 权限时调用（与 Layout 社区名称映射同一权限口径），
 * 无权限时静默降级：下拉仅"全部社区"，表格列回显"社区 #id"；
 * 系统管理语境仅管理全局 banner，无需社区下拉与名称映射
 */
async function loadCommunities() {
  if (isSystemScope.value || !authStore.hasPermission('community:view')) return
  communityLoading.value = true
  try {
    const res = await getCommunityList({ page: 1, page_size: 200 })
    const list = res.data.data.list || []
    communityOptions.value = [
      { label: '全部社区', value: 0 },
      ...list.map((c: { id: number; name: string }) => ({ label: c.name, value: c.id })),
    ]
    const map: Record<number, string> = {}
    list.forEach((c: { id: number; name: string }) => {
      map[c.id] = c.name
    })
    communityNameMap.value = map
  } catch {
    // 拉取失败静默降级，不影响主流程
  } finally {
    communityLoading.value = false
  }
}

const columns: DataTableColumns<Banner> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '标题', key: 'title', width: 140, ellipsis: { tooltip: true } },
  {
    title: '图片',
    key: 'image_url',
    width: 140,
    render(row) {
      if (!row.image_url) {
        return h('span', { style: 'color: #999;' }, '—')
      }
      // 缩略图预览：21:9 比例（建议尺寸 750×320），点击放大，hover 显示完整 URL；
      // 外域图片禁 referrer 防 OSS 防盗链 403；加载失败回落文案提示，URL 在 title 中可查
      return h(
        NImage,
        {
          src: row.image_url,
          class: 'banner-thumb',
          width: 120,
          height: 52,
          objectFit: 'cover',
          lazy: true,
          imgProps: { referrerpolicy: 'no-referrer', title: row.image_url },
        },
        {
          error: () =>
            h('span', { title: row.image_url, style: 'color: #d03050; font-size: 12px;' }, '图片加载失败'),
        }
      )
    },
  },
  {
    title: '位置',
    key: 'position',
    width: 90,
    render(row) {
      return row.position === 1 ? '首页' : '分类页'
    },
  },
  {
    title: '适用社区',
    key: 'community_id',
    width: 120,
    render(row) {
      // 全局 banner 用"全平台通用"标识（社区业务语境只读展示）
      if (row.community_id === 0) {
        return h(NTag, { size: 'small', type: 'info', bordered: false }, { default: () => '全平台通用' })
      }
      return communityNameMap.value[row.community_id] || `社区#${row.community_id}`
    },
  },
  { title: '排序', key: 'sort', width: 80 },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '启用' : '禁用') })
    },
  },
  { title: '开始时间', key: 'start_time', width: 150, render: (row) => formatDateTime(row.start_time) },
  { title: '结束时间', key: 'end_time', width: 150, render: (row) => formatDateTime(row.end_time) },
  {
    title: '操作',
    key: 'actions',
    width: 130,
    render(row) {
      // 社区业务语境下全局 banner 只读，修改入口在系统管理页
      if (!isSystemScope.value && row.community_id === 0) {
        return h('span', { style: 'color: #999;' }, '只读')
      }
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openModal(row) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

const banners = ref<Banner[]>([])
const loading = ref(false)
const searchTitle = ref('')
const showModal = ref(false)
const modalLoading = ref(false)
const timeRange = ref<[number, number] | null>(null)
const form = reactive<Partial<Banner>>({
  id: undefined,
  community_id: 0,
  title: '',
  image_url: '',
  link_url: '',
  position: 2,
  sort: 0,
  status: 1,
})

/**
 * 根据标题本地过滤后的 Banner 列表
 */
const filteredBanners = computed(() => {
  const keyword = searchTitle.value.trim()
  if (!keyword) return banners.value
  return banners.value.filter((item) => item.title.toLowerCase().includes(keyword.toLowerCase()))
})

/**
 * 加载 Banner 列表
 * 系统管理语境显式传 community_id=0（仅全局 banner，超管专用参数）；
 * 社区业务语境不传，走当前社区语境（后端返回本社区 + 全局兜底）
 */
async function loadBanners() {
  loading.value = true
  try {
    const res = await getBannerList(isSystemScope.value ? { community_id: 0 } : {})
    if (res.data.code === 0) {
      banners.value = res.data.data || []
    }
  } finally {
    loading.value = false
  }
}

/**
 * 打开 Banner 编辑弹窗
 * @param row Banner 数据，为空时新增
 */
function openModal(row?: Banner) {
  form.id = row?.id
  // 新增默认当前社区语境（不传 community_id 由后端取 header）；编辑回显原适用社区；
  // 系统管理语境新增固定全局（0）
  form.community_id = row?.community_id ?? (isSystemScope.value ? 0 : undefined)
  form.title = row?.title || ''
  form.image_url = row?.image_url || ''
  form.link_url = row?.link_url || ''
  form.position = row?.position || 2
  form.sort = row?.sort ?? 0
  form.status = row?.status ?? 1
  if (row?.start_time && row?.end_time) {
    const start = new Date(row.start_time).getTime()
    const end = new Date(row.end_time).getTime()
    timeRange.value = !Number.isNaN(start) && !Number.isNaN(end) ? [start, end] : null
  } else {
    timeRange.value = null
  }
  showModal.value = true
}

/**
 * 将时间戳格式化为 YYYY-MM-DD HH:mm:00
 * @param ts 时间戳
 * @returns 格式化后的时间字符串
 */
function formatTime(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00`
}

/**
 * 保存 Banner（新增或更新）
 */
async function saveBanner() {
  if (!form.title) {
    message.warning('请输入标题')
    return
  }
  if (!form.image_url) {
    message.warning('请输入图片地址')
    return
  }
  if (!form.position) {
    message.warning('请选择展示位置')
    return
  }

  modalLoading.value = true
  try {
    const data: Partial<Banner> = {
      title: form.title,
      image_url: form.image_url,
      link_url: form.link_url,
      position: form.position,
      sort: form.sort,
      status: form.status,
    }
    // 适用社区：未选择时不下发，由后端取当前社区语境（编辑时不下发=不修改）；
    // 系统管理语境固定下发 0（超管显式指定=全部社区通用）
    if (isSystemScope.value) {
      data.community_id = 0
    } else if (form.community_id !== undefined && form.community_id !== null) {
      data.community_id = form.community_id
    }
    if (timeRange.value) {
      data.start_time = formatTime(timeRange.value[0])
      data.end_time = formatTime(timeRange.value[1])
    }

    if (form.id) {
      await updateBanner(form.id, data)
    } else {
      const res = await createBanner(data)
      // 后端创建时固定 status=1，若前端选择禁用则创建后再次更新状态
      if (form.status === 2 && res.data.data?.id) {
        await updateBanner(res.data.data.id, { status: 2 })
      }
    }
    message.success('保存成功')
    showModal.value = false
    loadBanners()
  } finally {
    modalLoading.value = false
  }
}

/**
 * 删除 Banner
 * @param row Banner 数据
 */
async function remove(row: Banner) {
  const res = await deleteBanner(row.id)
  if (res.data.code === 0) {
    message.success('删除成功')
    loadBanners()
  }
}

onMounted(() => {
  loadBanners()
  loadCommunities()
})
</script>

<style lang="scss" scoped>
/* 列表 Banner 缩略图：120×52（21:9），与建议上传尺寸 750×320 同比例 */
.banner-thumb {
  display: inline-block;
  vertical-align: middle;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  overflow: hidden;
  cursor: zoom-in;
}
</style>
