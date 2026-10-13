<template>
  <div class="banner-manager">
    <n-space vertical>
      <n-space wrap>
        <n-input v-model:value="searchTitle" placeholder="标题" clearable />
        <n-button type="primary" @click="loadBanners">查询</n-button>
        <n-button type="primary" @click="openModal()">新增 Banner</n-button>
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
import { NButton, NSpace, NTag } from 'naive-ui'
import ImageUploadInput from '@/components/ImageUploadInput.vue'
import LinkSelector from '@/components/LinkSelector.vue'
import type { DataTableColumns } from 'naive-ui'
import { getBannerList, createBanner, updateBanner, deleteBanner } from '@/api/marketing'
import type { Banner } from '@/api/types'

const message = useMessage()

const positionOptions = [
  { label: '首页', value: 1 },
  { label: '分类页', value: 2 },
]

const columns: DataTableColumns<Banner> = [
  { title: 'ID', key: 'id', width: 70 },
  { title: '标题', key: 'title', width: 140, ellipsis: { tooltip: true } },
  {
    title: '图片地址',
    key: 'image_url',
    width: 180,
    ellipsis: { tooltip: true },
    render(row) {
      return h('span', { title: row.image_url }, row.image_url)
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
  { title: '排序', key: 'sort', width: 80 },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '启用' : '禁用') })
    },
  },
  { title: '开始时间', key: 'start_time', width: 150 },
  { title: '结束时间', key: 'end_time', width: 150 },
  {
    title: '操作',
    key: 'actions',
    width: 130,
    render(row) {
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
  title: '',
  image_url: '',
  link_url: '',
  position: 1,
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
 */
async function loadBanners() {
  loading.value = true
  try {
    const res = await getBannerList({})
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
  form.title = row?.title || ''
  form.image_url = row?.image_url || ''
  form.link_url = row?.link_url || ''
  form.position = row?.position || 1
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
})
</script>
