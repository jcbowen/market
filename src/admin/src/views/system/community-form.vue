<template>
  <n-card :title="isEdit ? '编辑社区' : '新增社区'">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="社区名称">
        <n-input v-model:value="form.name" />
      </n-form-item>
      <n-form-item label="地址">
        <n-input v-model:value="form.address" />
      </n-form-item>
      <n-form-item label="联系电话">
        <n-input v-model:value="form.contact_phone" />
      </n-form-item>
      <n-form-item label="负责人">
        <n-input v-model:value="form.manager_name" />
      </n-form-item>
      <n-form-item label="负责人电话">
        <n-input v-model:value="form.manager_phone" />
      </n-form-item>
      <n-form-item label="自提点地址">
        <n-input v-model:value="form.pickup_address" />
      </n-form-item>
      <n-form-item label="经纬度">
        <n-space vertical>
          <n-space>
            <n-input v-model:value="longitudeText" placeholder="经度" style="width: 140px" />
            <n-input v-model:value="latitudeText" placeholder="纬度" style="width: 140px" />
            <n-button @click="openMapPicker">在地图中选择</n-button>
          </n-space>
          <n-space>
            <n-input
              v-model:value="coordPasteText"
              placeholder="粘贴坐标后回填，如 116.397428,39.90923"
              style="width: 300px"
              clearable
              @keyup.enter="applyPastedCoords"
            />
            <n-button @click="applyPastedCoords">解析回填</n-button>
          </n-space>
        </n-space>
      </n-form-item>
      <n-form-item label="配送费">
        <n-input-number v-model:value="form.delivery_fee" :min="0" />
      </n-form-item>
      <n-form-item label="起送金额">
        <n-input-number v-model:value="form.min_order_amount" :min="0" />
      </n-form-item>
      <n-form-item label="配送范围(km)">
        <n-input-number v-model:value="form.delivery_range_km" :min="0" />
      </n-form-item>
      <n-form-item label="营业时间">
        <n-input v-model:value="form.business_hours" placeholder="如 08:00-22:00" />
      </n-form-item>
      <n-form-item label="排序">
        <n-input-number v-model:value="form.sort" :min="0" />
      </n-form-item>
      <n-form-item label="状态">
        <n-select v-model:value="form.status" :options="statusOptions" />
      </n-form-item>
      <n-form-item>
        <n-space>
          <n-button type="primary" @click="submit">保存</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>

  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { createCommunity, updateCommunity, getCommunityList } from '@/api/user'
import type { Community } from '@/api/types'

/**
 * 社区新增/编辑页面
 */
const route = useRoute()
const router = useRouter()
const message = useMessage()
const isEdit = ref(false)
const form = ref<Partial<Community>>({
  name: '',
  address: '',
  contact_phone: '',
  manager_name: '',
  manager_phone: '',
  pickup_address: '',
  longitude: 0,
  latitude: 0,
  delivery_fee: 0,
  min_order_amount: 0,
  delivery_range_km: 0,
  business_hours: '',
  sort: 0,
  status: 1,
})
const statusOptions = [
  { label: '营业', value: 1 },
  { label: '停业', value: 0 },
]

const longitudeText = computed({
  get: () => (form.value.longitude ? String(form.value.longitude) : ''),
  set: (v: string) => {
    form.value.longitude = parseFloat(v) || 0
  },
})

const latitudeText = computed({
  get: () => (form.value.latitude ? String(form.value.latitude) : ''),
  set: (v: string) => {
    form.value.latitude = parseFloat(v) || 0
  },
})

/** 粘贴坐标输入框内容，格式如 "116.397428,39.90923" */
const coordPasteText = ref('')

/**
 * 新标签页打开高德官方坐标拾取页
 * 高德拾取页有反嵌套限制，无法 iframe 内嵌，改为新窗口打开，
 * 用户选点后复制坐标，回到本页通过"解析回填"粘贴回填
 */
function openMapPicker() {
  window.open('https://lbs.amap.com/tools/picker', '_blank', 'noopener')
}

/**
 * 解析粘贴的坐标文本并回填经纬度
 * 支持 "经度,纬度" 格式，分隔符兼容中英文逗号、空格、制表符，
 * 例如高德拾取页复制的 "116.397428,39.90923"
 * 校验经度范围 [-180, 180]、纬度范围 [-90, 90]
 * @throws 无合法坐标时不抛异常，通过 message.warning 提示后保持原值
 */
function applyPastedCoords() {
  const parts = coordPasteText.value.trim().split(/[,，\s]+/).filter(Boolean)
  if (parts.length < 2) {
    message.warning('请粘贴 "经度,纬度" 格式的坐标，如 116.397428,39.90923')
    return
  }

  const lngValue = parseFloat(parts[0])
  const latValue = parseFloat(parts[1])
  if (Number.isNaN(lngValue) || Number.isNaN(latValue) || Math.abs(lngValue) > 180 || Math.abs(latValue) > 90) {
    message.warning('坐标解析失败，请确认经度在前、纬度在后且数值在有效范围内')
    return
  }

  form.value.longitude = lngValue
  form.value.latitude = latValue
  coordPasteText.value = ''
  message.success('坐标已回填')
}

async function loadDetail() {
  const id = Number(route.params.id)
  if (!id) return
  isEdit.value = true
  const res = await getCommunityList({ page: 1, page_size: 100 })
  if (res.data.code === 0) {
    const item = res.data.data.list.find((c: Community) => c.id === id)
    if (item) form.value = { ...item }
  }
}

async function submit() {
  const data = { ...form.value } as Community
  const res = isEdit.value ? await updateCommunity(data.id, data) : await createCommunity(data)
  if (res.data.code === 0) {
    router.back()
  }
}

onMounted(loadDetail)
</script>
