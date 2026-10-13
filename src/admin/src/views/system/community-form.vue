<template>
  <n-card :title="isEdit ? '编辑社区' : '新增社区'">
    <n-form ref="formRef" :model="form" :rules="rules" label-placement="left" label-width="100px">
      <n-tabs v-model:value="activeTab" type="line" display-directive="show">
        <!-- 基本信息 -->
        <n-tab-pane name="basic" tab="基本信息">
          <n-grid :cols="2" :x-gap="24">
            <n-gi>
              <n-form-item label="社区名称" path="name">
                <n-input v-model:value="form.name" placeholder="请输入社区名称" />
              </n-form-item>
            </n-gi>
            <n-gi>
              <n-form-item label="状态">
                <n-select v-model:value="form.status" :options="statusOptions" />
              </n-form-item>
            </n-gi>
          </n-grid>

          <n-form-item label="地址">
            <n-input v-model:value="form.address" placeholder="社区详细地址" />
          </n-form-item>

          <!-- 省/市/区紧随地址一行排布，作为 C 端锁定地址前缀的数据来源；区划选择器同时产出名称与区划代码 -->
          <n-form-item label="所在地区">
            <RegionPicker
              ref="regionPickerRef"
              style="flex: 1"
              placeholder="请选择省 / 市 / 区"
              @select="onRegionSelect"
            />
          </n-form-item>

          <n-grid :cols="2" :x-gap="24">
            <n-gi>
              <n-form-item label="联系电话">
                <n-input v-model:value="form.contact_phone" />
              </n-form-item>
            </n-gi>
            <n-gi>
              <n-form-item label="负责人">
                <n-input v-model:value="form.manager_name" />
              </n-form-item>
            </n-gi>
          </n-grid>

          <n-grid :cols="2" :x-gap="24">
            <n-gi>
              <n-form-item label="负责人电话">
                <n-input v-model:value="form.manager_phone" />
              </n-form-item>
            </n-gi>
            <n-gi>
              <n-form-item label="排序">
                <n-input-number v-model:value="form.sort" :min="0" style="width: 100%" />
              </n-form-item>
            </n-gi>
          </n-grid>
        </n-tab-pane>

        <!-- 配送设置 -->
        <n-tab-pane name="delivery" tab="配送设置">
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

          <n-grid :cols="3" :x-gap="24">
            <n-gi>
              <n-form-item label="配送费" :show-feedback="false">
                <n-input-number v-model:value="form.delivery_fee" :min="0" style="width: 100%" />
              </n-form-item>
            </n-gi>
            <n-gi>
              <n-form-item label="起送金额" :show-feedback="false">
                <n-input-number v-model:value="form.min_order_amount" :min="0" style="width: 100%" />
              </n-form-item>
            </n-gi>
            <n-gi>
              <n-form-item label="配送范围(km)" :show-feedback="false">
                <n-input-number v-model:value="form.delivery_range_km" :min="0" style="width: 100%" />
              </n-form-item>
            </n-gi>
          </n-grid>

          <n-form-item label="营业时间">
            <n-input v-model:value="form.business_hours" placeholder="如 08:00-22:00" />
          </n-form-item>

          <n-form-item label="社区外配送">
            <n-space align="center">
              <n-switch v-model:value="form.allow_outside_delivery" />
              <span class="form-hint">关闭时，C 端收货地址前缀锁定为本社区省市区，用户仅可填写楼栋门牌等详细地址</span>
            </n-space>
          </n-form-item>
        </n-tab-pane>
      </n-tabs>

      <n-form-item :show-label="false" style="margin-top: 8px">
        <n-space>
          <n-button type="primary" :loading="saving" @click="submit">保存</n-button>
          <n-button :disabled="saving" @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>

  </n-card>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import { createCommunity, updateCommunity, getCommunity } from '@/api/user'
import { matchRegionPathByNames } from '@/api/region'
import RegionPicker from '@/components/RegionPicker.vue'
import { useTabsStore } from '@/stores/tabs'
import type { Community } from '@/api/types'

/**
 * 社区新增/编辑页面
 * 表单按 Tab 分组：基本信息（名称/地址/省市区/联系人与排序状态）、配送设置（自提点/经纬度/费用/外配开关）
 */
const route = useRoute()
const router = useRouter()
const message = useMessage()
const tabsStore = useTabsStore()
const isEdit = ref(false)
const formRef = ref<FormInst | null>(null)
const saving = ref(false)

/** 当前激活的表单分组 Tab */
const activeTab = ref<'basic' | 'delivery'>('basic')

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
  province: '',
  city: '',
  district: '',
  province_code: '',
  city_code: '',
  district_code: '',
  allow_outside_delivery: false,
  sort: 0,
  status: 1,
})

/** 区划选择器实例（存量数据按名称回显用） */
const regionPickerRef = ref<InstanceType<typeof RegionPicker> | null>(null)

/**
 * 区划选择器选中回调：同步省/市/区名称与区划代码到表单
 */
function onRegionSelect(sel: { province: { code: string; name: string } | null; city: { code: string; name: string } | null; district: { code: string; name: string } | null }) {
  form.value.province = sel.province?.name ?? ''
  form.value.city = sel.city?.name ?? ''
  form.value.district = sel.district?.name ?? ''
  form.value.province_code = sel.province?.code ?? ''
  form.value.city_code = sel.city?.code ?? ''
  form.value.district_code = sel.district?.code ?? ''
}
const rules: FormRules = {
  name: { required: true, message: '请输入社区名称', trigger: 'blur' },
}
const statusOptions = [
  { label: '营业', value: 1 },
  { label: '停业', value: 0 },
]

/** 校验失败字段所在表单分组（用于自动切到出错 Tab） */
const fieldTabMap: Record<string, 'basic' | 'delivery'> = {
  name: 'basic',
}

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
  try {
    const res = await getCommunity(id)
    if (res.data.code === 0 && res.data.data) {
      form.value = { ...form.value, ...res.data.data }
      // 加载完成后用社区名区分标签页
      tabsStore.rename(route.fullPath, `编辑社区 · ${res.data.data.name}`)
      // 存量数据可能缺区划代码（历史手填），按名称逐级匹配回显选择器；
      // 匹配失败时选择器留空，保存时保留原名称、代码置空
      await regionPickerRef.value?.setValueByPath(
        await matchRegionPathByNames(form.value.province || '', form.value.city || '', form.value.district || '')
      )
    } else {
      message.error('社区不存在或已被删除')
      router.back()
    }
  } catch {
    // 错误提示由响应拦截器统一弹出
    router.back()
  }
}

async function submit() {
  try {
    await formRef.value?.validate()
  } catch (errors) {
    // 校验失败：自动切到出错字段所在的 Tab，表单内已显示错误提示
    const fields = Array.isArray(errors) ? errors.map((e) => (e as { field?: string }).field) : []
    for (const field of fields) {
      const tab = field ? fieldTabMap[field] : undefined
      if (tab) {
        activeTab.value = tab
        break
      }
    }
    return
  }
  saving.value = true
  try {
    // 管理端为整表提交，后端全量覆盖保存（含清空字段与停业状态）
    const data = { ...form.value } as Community
    const res = isEdit.value ? await updateCommunity(data.id, data) : await createCommunity(data)
    if (res.data.code === 0) {
      message.success(isEdit.value ? '保存成功' : '创建成功')
      router.back()
    }
  } catch {
    // 错误提示由响应拦截器统一弹出，此处仅阻断返回
  } finally {
    saving.value = false
  }
}

onMounted(loadDetail)
</script>

<style scoped>
.form-hint {
  font-size: 12px;
  color: #999;
}
</style>
