<template>
  <view class="address-edit-page">
    <!-- 未选择社区：默认不允许社区外配送，阻断保存；点击可直接去切换 -->
    <view v-if="communityChecked && !community" class="community-tip-card" @click="goCommunitySelect">
      <text class="community-tip-text">您还未选择社区，点击此处或前往「我的-切换社区」中选择所在社区后再添加收货地址</text>
    </view>

    <view class="form-card">
      <u-form :model="form" :rules="rules" ref="formRef" label-width="160rpx">
        <u-form-item label="联系人" prop="contact_name" border-bottom>
          <u-input v-model="form.contact_name" placeholder="请输入联系人姓名" border="none" />
        </u-form-item>
        <u-form-item label="手机号" prop="contact_phone" border-bottom>
          <u-input v-model="form.contact_phone" placeholder="请输入手机号" border="none" maxlength="11" />
        </u-form-item>

        <!-- 锁定模式：地址前缀取当前社区省市区，仅可填写详细地址 -->
        <template v-if="!allowOutside">
          <u-form-item label="所在社区" border-bottom>
            <view class="prefix-block">
              <view class="community-row">
                <text class="prefix-community">{{ community?.name || '未选择社区' }}</text>
                <text class="switch-btn" @click.stop="goCommunitySelect">切换</text>
              </view>
              <text class="prefix-region">{{ prefixText || '社区省市区未配置，请联系管理员' }}</text>
              <text v-if="community?.address" class="prefix-address">社区地址：{{ community.address }}</text>
              <text v-if="community?.pickup_address" class="prefix-address">自提点：{{ community.pickup_address }}</text>
            </view>
          </u-form-item>
          <u-form-item label="详细地址" prop="detail" border-bottom>
            <u-input v-model="form.detail" placeholder="请输入楼栋、单元、门牌号" border="none" />
          </u-form-item>
        </template>

        <!-- 自由模式：社区允许社区外配送，省市区通过区划选择器按区划代码选择 -->
        <template v-else>
          <u-form-item label="所在地区" prop="region" border-bottom @click="openPicker">
            <view class="region-display" :class="{ 'region-placeholder': !regionDisplayText }">
              <text>{{ regionDisplayText || '请选择省 / 市 / 区' }}</text>
            </view>
          </u-form-item>
          <u-form-item label="详细地址" prop="detail" border-bottom>
            <u-input v-model="form.detail" placeholder="请输入详细地址" border="none" />
          </u-form-item>
        </template>

        <u-form-item label="标签">
          <u-input v-model="form.tag" placeholder="如：家、公司" border="none" />
        </u-form-item>
      </u-form>
    </view>

    <view class="default-card">
      <text>设为默认地址</text>
      <u-switch v-model="form.is_default" />
    </view>

    <!-- 行政区划三级联动选择器（省 / 市 / 区县，数据来自区划代码库） -->
    <u-picker
      ref="regionPickerRef"
      :show="regionPickerShow"
      :columns="regionColumns"
      :loading="regionLoading"
      key-name="name"
      value-name="code"
      :default-index="regionDefaultIndex"
      title="选择所在地区"
      @change="onRegionChange"
      @confirm="onRegionConfirm"
      @cancel="regionPickerShow = false"
      @close="regionPickerShow = false"
    ></u-picker>

    <!-- 事件必须直接绑在 u-button 上：其 stop 属性默认 true 会拦截冒泡，外层 view 收不到点击 -->
    <view class="save-btn">
      <u-button type="success" text="保存" :loading="saving" @click="onSave" />
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { onLoad, onShow } from '@dcloudio/uni-app'
import { createAddress, updateAddress, getCommunity, locateRegion } from '@/api/user'
import { joinRegionAddress } from '@/utils/region'
import { loadProvinces, loadCities, loadDistricts } from '@/utils/regionCache'
import { useUserStore } from '@/stores/user'
import { goCommunitySelect } from '@/utils/navigate'
import type { Address, Community } from '@/api/types'
import type { Region } from '@/api/user'

const userStore = useUserStore()
const addressId = ref(0)
const saving = ref(false)
const formRef = ref()

const form = ref<Address>({
  id: 0,
  contact_name: '',
  contact_phone: '',
  province: '',
  city: '',
  district: '',
  province_code: '',
  city_code: '',
  district_code: '',
  detail: '',
  tag: '',
  community_id: 0,
  community_name: '',
  is_default: false,
})

const rules = ref({
  contact_name: [{ required: true, message: '请输入联系人', trigger: ['blur', 'change'] }],
  contact_phone: [
    { required: true, message: '请输入手机号', trigger: ['blur', 'change'] },
    { pattern: /^1\d{10}$/, message: '手机号格式错误', trigger: ['blur', 'change'] },
  ],
  detail: [{ required: true, message: '请输入详细地址', trigger: ['blur', 'change'] }],
})

/** 当前用户所属社区详情（含省市区与外配开关） */
const community = ref<Community | null>(null)
/** 社区详情是否已加载完成（避免未加载时误判为未选社区） */
const communityChecked = ref(false)

/** 是否允许社区外配送（默认不允许：未取到社区信息时同样锁定） */
const allowOutside = computed(() => !!community.value?.allow_outside_delivery)

/** 锁定模式展示的地址前缀（省市区，直辖市市省同名时相邻去重） */
const prefixText = computed(() => {
  const c = community.value
  if (!c) return ''
  return joinRegionAddress(c.province, c.city, c.district)
})

// --- 行政区划三级联动选择器（基于区划代码库，逐级懒加载） ---

const regionPickerRef = ref()
const regionPickerShow = ref(false)
const regionLoading = ref(false)
/** 三列选项：[省列表, 市列表, 区县列表]，条目为 {code, name} */
const regionColumns = ref<Region[][]>([[], [], []])
/** 初始选中索引（仅首次打开生效，之后由组件内部保持） */
const regionDefaultIndex = ref<number[]>([0, 0, 0])
/** 当前选择（预选与确认中间态），用于联动加载与回显 */
const selectedRegion = ref<{ province: Region | null; city: Region | null; district: Region | null }>({
  province: null,
  city: null,
  district: null,
})

/**
 * 区划列表加载与缓存统一收敛到 utils/regionCache：
 * 省级持久化缓存（7 天过期自动重拉），市/区县模块级内存缓存（跨页面实例复用）
 */

/** 已选地区展示文案（相邻重复层级去重，如直辖市市省同名） */
const regionDisplayText = computed(() => {
  const parts = [form.value.province, form.value.city, form.value.district]
    .map((p) => (p || '').trim())
    .filter(Boolean)
  return parts.filter((p, i) => i === 0 || p !== parts[i - 1]).join(' / ')
})

/**
 * 打开区划选择器
 * 首次打开时按当前选择（或列表首项）组合三列数据并计算初始索引
 */
async function openPicker() {
  regionLoading.value = true
  regionPickerShow.value = true
  try {
    const provList = await loadProvinces()
    if (!provList.length) return

    const prov =
      selectedRegion.value.province && provList.some((p) => p.code === selectedRegion.value.province?.code)
        ? selectedRegion.value.province
        : provList[0]
    const cityList = await loadCities(prov.code)
    // 直辖市无市级层：省下直接挂区县（第二列即区县，第三列留空）
    const isMunicipality = cityList.length > 0 && cityList[0].level === 3

    let city: Region | null = null
    let districtList: Region[] = []
    let district: Region | null = null
    if (isMunicipality) {
      district =
        selectedRegion.value.district && cityList.some((d) => d.code === selectedRegion.value.district?.code)
          ? selectedRegion.value.district
          : cityList[0] || null
    } else {
      city =
        selectedRegion.value.city && cityList.some((c) => c.code === selectedRegion.value.city?.code)
          ? selectedRegion.value.city
          : cityList[0] || null
      districtList = city ? await loadDistricts(city.code) : []
      district =
        selectedRegion.value.district && districtList.some((d) => d.code === selectedRegion.value.district?.code)
          ? selectedRegion.value.district
          : districtList[0] || null
    }

    regionColumns.value = [provList, cityList, districtList]
    regionDefaultIndex.value = [
      provList.findIndex((p) => p.code === prov.code),
      cityList.findIndex((c) => c.code === (isMunicipality ? district?.code ?? '' : city?.code ?? '')) < 0
        ? 0
        : cityList.findIndex((c) => c.code === (isMunicipality ? district?.code ?? '' : city?.code ?? '')),
      districtList.findIndex((d) => d.code === (district?.code ?? '')) < 0
        ? 0
        : districtList.findIndex((d) => d.code === (district?.code ?? '')),
    ]
    selectedRegion.value = { province: prov, city, district }
  } catch {
    // 错误提示由请求层统一弹出
  } finally {
    regionLoading.value = false
  }
}

/**
 * 选择器列变化：省列变化时重载市/区两列，市列变化时重载区列
 * 通过 setColumnValues 更新后续列，并将后续列选中索引重置为 0
 */
async function onRegionChange(e: { columnIndex: number; value: Region[] }) {
  const columnIndex = e.columnIndex
  try {
    regionLoading.value = true
    if (columnIndex === 0) {
      const prov = e.value[0] || null
      selectedRegion.value.province = prov
      if (!prov) return
      const cityList = await loadCities(prov.code)
      // 直辖市：省下直接挂区县，第二列即区县，第三列留空
      if (cityList.length > 0 && cityList[0].level === 3) {
        selectedRegion.value.city = null
        selectedRegion.value.district = cityList[0] || null
        regionPickerRef.value?.setColumnValues(1, cityList)
        regionPickerRef.value?.setColumnValues(2, [])
        return
      }
      selectedRegion.value.city = cityList[0] || null
      regionPickerRef.value?.setColumnValues(1, cityList)
      if (selectedRegion.value.city) {
        const districtList = await loadDistricts(selectedRegion.value.city.code)
        selectedRegion.value.district = districtList[0] || null
        regionPickerRef.value?.setColumnValues(2, districtList)
      } else {
        selectedRegion.value.district = null
        regionPickerRef.value?.setColumnValues(2, [])
      }
    } else if (columnIndex === 1) {
      const city = e.value[1] || null
      // 直辖市：第二列滚动到区县，直接作为区县选中
      if (city && city.level === 3) {
        selectedRegion.value.city = null
        selectedRegion.value.district = city
        regionPickerRef.value?.setColumnValues(2, [])
        return
      }
      selectedRegion.value.city = city
      if (!city) return
      const districtList = await loadDistricts(city.code)
      selectedRegion.value.district = districtList[0] || null
      regionPickerRef.value?.setColumnValues(2, districtList)
    } else {
      selectedRegion.value.district = e.value[2] || null
    }
  } finally {
    regionLoading.value = false
  }
}

/**
 * 确认选择：同步省/市/区名称与区划代码到表单
 * 直辖市（第二列即区县）无市级层：市与省同名同码落库，保证 city 字段始终有值
 */
function onRegionConfirm(e: { value: Region[] }) {
  const [prov, second, third] = e.value
  if (!prov) return
  if (second && second.level === 3) {
    selectedRegion.value = { province: prov, city: prov, district: second }
    form.value.province = prov.name || ''
    form.value.city = prov.name || ''
    form.value.district = second.name || ''
    form.value.province_code = prov.code || ''
    form.value.city_code = prov.code || ''
    form.value.district_code = second.code || ''
    return
  }
  const city = second || null
  const district = third || null
  selectedRegion.value = { province: prov, city, district }
  form.value.province = prov.name || ''
  form.value.city = city?.name || ''
  form.value.district = district?.name || ''
  form.value.province_code = prov.code || ''
  form.value.city_code = city?.code || ''
  form.value.district_code = district?.code || ''
}

/**
 * IP 定位自动填充（仅自由模式且省市区为空时执行，best-effort）
 * 服务端按出口 IP 反查区划，精度到市，区县需用户补选
 */
async function autoLocateRegion() {
  try {
    const res = await locateRegion()
    const loc = res.data
    if (!loc || !loc.province) return
    if (form.value.province || form.value.city || form.value.district) return
    if (loc.city) {
      // 普通省份：预填省 + 市，区县由用户补选
      selectedRegion.value = { province: loc.province, city: loc.city, district: null }
      form.value.province = loc.province.name
      form.value.city = loc.city.name
      form.value.province_code = loc.province.code
      form.value.city_code = loc.city.code
    } else {
      // 直辖市等无市级层：省预填后市与省同名同码（落库口径），区县由用户在省下直接选择
      selectedRegion.value = { province: loc.province, city: loc.province, district: null }
      form.value.province = loc.province.name
      form.value.city = loc.province.name
      form.value.province_code = loc.province.code
      form.value.city_code = loc.province.code
    }
  } catch {
    // 定位失败静默忽略，不影响手动选择
  }
}

onLoad((query) => {
  addressId.value = Number(query?.id) || 0
})

// 社区信息每次展示时刷新：从社区选择页切换返回后能立即更新，且不重置已填表单（页面栈保留）
onShow(() => {
  loadCommunity()
})

onMounted(() => {
  if (addressId.value) {
    const eventChannel = getOpenerEventChannel()
    eventChannel.on('addressData', (data: Address) => {
      form.value = { ...form.value, ...data }
      // 编辑已有地址时恢复选择器中间态，便于直接微调
      if (data.province) {
        selectedRegion.value = {
          province: data.province_code
            ? { code: data.province_code, name: data.province, parent_code: '0', level: 1, status: 1 }
            : null,
          city: data.city_code
            ? { code: data.city_code, name: data.city, parent_code: '', level: 2, status: 1 }
            : null,
          district: data.district_code
            ? { code: data.district_code, name: data.district, parent_code: '', level: 3, status: 1 }
            : null,
        }
      }
    })
  }
})

/**
 * 加载当前用户所属社区详情，用于判定地址前缀是否锁定
 */
async function loadCommunity() {
  communityChecked.value = false
  const communityId = userStore.userInfo?.community_id || userStore.communityInfo?.id || 0
  if (communityId) {
    try {
      const res = await getCommunity(communityId)
      community.value = res.data || null
    } catch {
      community.value = null
    }
  } else {
    community.value = null
  }
  communityChecked.value = true
  // 新增地址且社区允许外配（自由模式）时，按出口 IP 预填省/市
  if (!addressId.value && allowOutside.value) {
    autoLocateRegion()
  }
}

/**
 * 保存地址
 */
async function onSave() {
  if (communityChecked.value && !community.value) {
    uni.showToast({ title: '请先选择社区', icon: 'none' })
    return
  }
  // 自由模式下所在地区必选（区划选择器产出，含区划代码）
  if (allowOutside.value && !form.value.province) {
    uni.showToast({ title: '请选择所在地区', icon: 'none' })
    return
  }
  const valid = await formRef.value?.validate()
  if (!valid) return

  saving.value = true
  try {
    // 锁定模式下前缀（含区划代码）由服务端按用户当前社区强制覆盖，这里同步填入社区省市区
    if (!allowOutside.value && community.value) {
      form.value.province = community.value.province
      form.value.city = community.value.city
      form.value.district = community.value.district
      form.value.province_code = community.value.province_code || ''
      form.value.city_code = community.value.city_code || ''
      form.value.district_code = community.value.district_code || ''
      form.value.community_id = community.value.id
    }
    if (addressId.value) {
      await updateAddress(addressId.value, form.value)
    } else {
      await createAddress(form.value)
    }
    uni.showToast({ title: '保存成功', icon: 'success' })
    setTimeout(() => uni.navigateBack({}), 1000)
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.address-edit-page {
  min-height: 100vh;
  padding: 20rpx 30rpx;
  background-color: #f5f5f5;
}

.form-card {
  padding: 0 20rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.community-tip-card {
  margin-bottom: 20rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #fff7e6;
}

.community-tip-text {
  font-size: 26rpx;
  color: #fa8c16;
  line-height: 1.5;
}

.prefix-block {
  display: flex;
  flex-direction: column;
}

.community-row {
  display: flex;
  align-items: center;
}

.prefix-community {
  font-size: 28rpx;
  color: #333333;
  font-weight: bold;
}

.switch-btn {
  margin-left: 16rpx;
  padding: 4rpx 20rpx;
  border: 1rpx solid #07c160;
  border-radius: 999rpx;
  font-size: 22rpx;
  color: #07c160;
}

.prefix-region {
  margin-top: 6rpx;
  font-size: 26rpx;
  color: #666666;
}

.prefix-address {
  margin-top: 4rpx;
  font-size: 24rpx;
  color: #999999;
  line-height: 1.5;
}

.default-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 20rpx;
  padding: 24rpx 30rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
  font-size: 28rpx;
  color: #333333;
}

.save-btn {
  margin-top: 40rpx;
}

.save-btn .u-button {
  width: 100%;
}

.region-display {
  min-height: 48rpx;
  display: flex;
  align-items: center;
}

.region-placeholder {
  color: #c0c4cc;
}
</style>
