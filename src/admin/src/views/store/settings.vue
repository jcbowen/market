<template>
  <n-card title="门店设置">
    <n-space vertical size="large">
      <n-alert type="info" :show-icon="false">
        此处维护本社区门店的运营资料与配送配置。社区名称、地址、经纬度、负责人电话等基础信息由平台统一维护，如需修改请联系平台管理员。
      </n-alert>

      <!-- 不可修改的基础信息：仅展示，提示口径与后端白名单一致 -->
      <n-descriptions label-placement="left" bordered :column="2" size="small">
        <n-descriptions-item label="社区名称">{{ detail.name || '—' }}</n-descriptions-item>
        <n-descriptions-item label="营业状态">
          <n-tag :type="detail.status === 1 ? 'success' : 'default'" size="small">
            {{ detail.status === 1 ? '营业中' : '停业' }}
          </n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="所在地区" :span="2">
          {{ [detail.province, detail.city, detail.district].filter(Boolean).join(' / ') || '—' }}
        </n-descriptions-item>
        <n-descriptions-item label="详细地址" :span="2">{{ detail.address || '—' }}</n-descriptions-item>
        <n-descriptions-item label="经纬度">
          {{ detail.longitude || detail.latitude ? `${detail.longitude}, ${detail.latitude}` : '—' }}
        </n-descriptions-item>
        <n-descriptions-item label="负责人电话">{{ detail.manager_phone || '—' }}</n-descriptions-item>
      </n-descriptions>

      <n-form ref="formRef" :model="form" label-placement="left" label-width="100px">
        <!-- 联系信息 -->
        <n-grid :cols="2" :x-gap="24">
          <n-gi>
            <n-form-item label="联系电话">
              <n-input v-model:value="form.contact_phone" placeholder="门店联系电话" />
            </n-form-item>
          </n-gi>
          <n-gi>
            <n-form-item label="负责人">
              <n-input v-model:value="form.manager_name" placeholder="门店负责人姓名" />
            </n-form-item>
          </n-gi>
        </n-grid>

        <!-- 配送配置 -->
        <n-form-item label="自提点地址">
          <n-input v-model:value="form.pickup_address" placeholder="自提点地址" />
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

        <!-- 营业时间：支持一天内多个运营时间段，提交前校验不可交叉 -->
        <n-form-item label="营业时间">
          <n-space vertical style="width: 100%">
            <n-space v-for="(r, i) in businessRanges" :key="i" align="center" :wrap="false">
              <n-time-picker
                v-model:formatted-value="r.start"
                format="HH:mm"
                value-format="HH:mm"
                placeholder="开始"
                :status="rangeRowErrors[i] ? 'error' : undefined"
                style="width: 110px"
              />
              <span>至</span>
              <n-time-picker
                v-model:formatted-value="r.end"
                format="HH:mm"
                value-format="HH:mm"
                placeholder="结束"
                :status="rangeRowErrors[i] ? 'error' : undefined"
                style="width: 110px"
              />
              <n-button quaternary type="error" size="small" @click="removeRange(i)">删除</n-button>
              <span v-if="rangeRowErrors[i]" class="form-hint range-error">{{ rangeRowErrors[i] }}</span>
            </n-space>
            <n-space align="center">
              <n-button dashed size="small" @click="addRange">+ 添加时间段</n-button>
              <span class="form-hint">可添加多个运营时间段，时间段之间不可交叉重叠</span>
            </n-space>
          </n-space>
        </n-form-item>

        <n-form-item label="社区外配送">
          <n-space align="center">
            <n-switch v-model:value="form.allow_outside_delivery" />
            <span class="form-hint">关闭时，C 端收货地址前缀锁定为本社区省市区，用户仅可填写楼栋门牌等详细地址</span>
          </n-space>
        </n-form-item>

        <n-form-item label="自提渠道">
          <n-space align="center">
            <n-switch v-model:value="form.enable_self_pickup" />
            <span class="form-hint">C 端下单页与收银台的「社区自提」选项开关；关闭后两处均不出现自提渠道，收银台到店模式不受影响</span>
          </n-space>
        </n-form-item>

        <n-form-item label="配送渠道">
          <n-space align="center">
            <n-switch v-model:value="form.enable_delivery" />
            <span class="form-hint">C 端下单页与收银台的「送货上门/配送」选项开关；关闭后两处均不出现配送渠道</span>
          </n-space>
        </n-form-item>

        <n-form-item :show-label="false" style="margin-top: 8px">
          <n-button type="primary" :loading="saving" @click="submit">保存</n-button>
        </n-form-item>
      </n-form>
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StoreSettings' })
import { ref, onMounted, computed } from 'vue'
import { useMessage } from 'naive-ui'
import { getCommunityProfile, updateCommunityProfile } from '@/api/user'
import type { Community } from '@/api/types'

/**
 * 门店设置页面（社区级）
 * 社区管理员自助维护本社区运营资料与配送配置：
 * - 可修改（白名单，与后端 CommunityProfileReq 对齐）：联系电话、负责人、自提点地址、
 *   配送费/起送金额/配送范围、营业时间、社区外配送开关
 * - 不可修改（仅展示）：社区名称、所在地区、详细地址、经纬度、负责人电话、营业状态
 */
const message = useMessage()
const loading = ref(false)
const saving = ref(false)

/** 当前社区详情（含不可修改的展示字段） */
const detail = ref<Partial<Community>>({})

/** 可编辑表单（仅白名单字段） */
const form = ref<Partial<Community>>({
  contact_phone: '',
  manager_name: '',
  pickup_address: '',
  delivery_fee: 0,
  min_order_amount: 0,
  delivery_range_km: 0,
  business_hours: '',
  allow_outside_delivery: false,
})

/**
 * 单个营业时间段（HH:mm 24 小时制）
 * 未填写时为 null 而非空串：n-time-picker 的 formatted-value 只接受 string | null，
 * 传空串会因内部 new Date('') 抛 Invalid time value 导致选择器渲染失败
 */
interface BusinessRange {
  start: string | null
  end: string | null
}

/** 营业时间多段编辑态：提交时序列化回 form.business_hours */
const businessRanges = ref<BusinessRange[]>([{ start: null, end: null }])

/** HH:mm 合法性（00:00 ~ 23:59） */
function isHHmm(s: string | null): boolean {
  return !!s && /^([01]\d|2[0-3]):[0-5]\d$/.test(s)
}

/**
 * 解析存储的营业时间为多段编辑态
 * 兼容两种存量格式：JSON 数组（多段）与 "HH:mm-HH:mm" 单段文本
 */
function parseBusinessHours(v?: string): BusinessRange[] {
  const t = (v || '').trim()
  if (!t) return []
  if (t.startsWith('[')) {
    try {
      const arr = JSON.parse(t)
      if (Array.isArray(arr)) {
        return arr.map((x) => ({
          start: isHHmm(String(x?.start ?? '')) ? String(x.start) : null,
          end: isHHmm(String(x?.end ?? '')) ? String(x.end) : null,
        }))
      }
    } catch {
      // 非 JSON，按旧单段格式继续解析
    }
  }
  const m = t.match(/^(\d{1,2}:\d{2})\s*[-—~至]\s*(\d{1,2}:\d{2})$/)
  return m ? [{ start: m[1], end: m[2] }] : []
}

/** 多段序列化为 JSON 存储；无有效段时存空串 */
function serializeBusinessHours(): string {
  const valid = businessRanges.value.filter(
    (r): r is { start: string; end: string } => isHHmm(r.start) && isHHmm(r.end)
  )
  return valid.length ? JSON.stringify(valid) : ''
}

function addRange() {
  businessRanges.value.push({ start: null, end: null })
}

function removeRange(i: number) {
  businessRanges.value.splice(i, 1)
}

/** 单行校验：起止必须完整且开始早于结束（不支持跨天） */
function rangeRowError(r: BusinessRange): string {
  if (!r.start && !r.end) return ''
  if (!r.start || !r.end) return '请补全起止时间'
  if (r.start >= r.end) return '开始时间需早于结束时间'
  return ''
}

/** 每行的即时错误提示（驱动红框与行内文案） */
const rangeRowErrors = computed(() => businessRanges.value.map(rangeRowError))

/**
 * 多段交叉校验：按开始时间排序后，相邻段开始时间不得早于上一段结束时间
 * @returns 校验失败原因，通过时返回空串
 */
function rangeCrossError(): string {
  const valid = businessRanges.value
    .filter((r): r is { start: string; end: string } => isHHmm(r.start) && isHHmm(r.end))
    .sort((a, b) => a.start.localeCompare(b.start))
  for (let i = 1; i < valid.length; i++) {
    if (valid[i].start < valid[i - 1].end) return '运营时间段之间存在交叉/重叠，请调整'
  }
  return ''
}

async function loadDetail() {
  loading.value = true
  try {
    const res = await getCommunityProfile()
    if (res.data.code === 0 && res.data.data) {
      detail.value = res.data.data
      form.value = {
        contact_phone: res.data.data.contact_phone ?? '',
        manager_name: res.data.data.manager_name ?? '',
        pickup_address: res.data.data.pickup_address ?? '',
        delivery_fee: res.data.data.delivery_fee ?? 0,
        min_order_amount: res.data.data.min_order_amount ?? 0,
        delivery_range_km: res.data.data.delivery_range_km ?? 0,
        business_hours: res.data.data.business_hours ?? '',
        allow_outside_delivery: !!res.data.data.allow_outside_delivery,
        // 渠道开关缺省视为开启（兼容旧数据/字段未返回）
        enable_self_pickup: res.data.data.enable_self_pickup ?? true,
        enable_delivery: res.data.data.enable_delivery ?? true,
      }
      // 营业时间回显：兼容存量 "HH:mm-HH:mm" 单段与 JSON 多段格式
      const parsed = parseBusinessHours(form.value.business_hours)
      businessRanges.value = parsed.length ? parsed : [{ start: null, end: null }]
    }
  } catch {
    // 错误提示由响应拦截器统一弹出
  } finally {
    loading.value = false
  }
}

async function submit() {
  // 营业时间前端校验：起止完整、开始早于结束、多段之间不可交叉
  const rowErr = businessRanges.value.map(rangeRowError).find(Boolean)
  const crossErr = rangeCrossError()
  if (rowErr || crossErr) {
    message.error(rowErr || crossErr || '营业时间填写有误')
    return
  }
  form.value.business_hours = serializeBusinessHours()
  saving.value = true
  try {
    // 仅提交白名单字段，后端同样按白名单合并，名称/地址/经纬度/负责人电话等不会被覆盖
    const res = await updateCommunityProfile({
      contact_phone: form.value.contact_phone,
      manager_name: form.value.manager_name,
      pickup_address: form.value.pickup_address,
      delivery_fee: form.value.delivery_fee,
      min_order_amount: form.value.min_order_amount,
      delivery_range_km: form.value.delivery_range_km,
      business_hours: form.value.business_hours,
      allow_outside_delivery: form.value.allow_outside_delivery,
      enable_self_pickup: form.value.enable_self_pickup ?? true,
      enable_delivery: form.value.enable_delivery ?? true,
    })
    if (res.data.code === 0) {
      message.success('保存成功')
      if (res.data.data) detail.value = res.data.data
    }
  } catch {
    // 错误提示由响应拦截器统一弹出
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

.range-error {
  color: #d03050;
}
</style>
