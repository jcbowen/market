<template>
  <view class="address-list-page">
    <view v-if="!addresses.length" class="empty-wrap">
      <text class="empty-text">暂无收货地址</text>
    </view>

    <view v-for="group in addressGroups" :key="group.communityId" class="community-group">
      <view class="group-header">
        <text class="group-title">{{ group.communityName || '其他地址' }}</text>
      </view>
      <view
        v-for="item in group.items"
        :key="item.id"
        class="address-card"
        @click="onSelect(item)"
      >
        <view class="address-top">
          <text class="name">{{ item.contact_name }}</text>
          <text class="phone">{{ item.contact_phone }}</text>
          <text v-if="item.is_default" class="default-tag">默认</text>
        </view>
        <text class="address-text">
          {{ joinRegionAddress(item.province, item.city, item.district, item.detail) }}
        </text>
        <view class="address-actions">
          <text class="action" @click.stop="onEdit(item.id)">编辑</text>
          <text class="action delete" @click.stop="onDelete(item.id)">删除</text>
        </view>
      </view>
    </view>

    <view class="add-btn" @click="onAdd">
      <u-button type="success" text="新增地址" />
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getAddressList, deleteAddress } from '@/api/user'
import { goAddressEdit, goLogin, goBack } from '@/utils/navigate'
import { joinRegionAddress } from '@/utils/region'
import type { Address } from '@/api/types'

const userStore = useUserStore()
const addresses = ref<Address[]>([])

/** 按所属社区分组的地址列表（保持默认地址/最新地址优先的原始顺序） */
const addressGroups = computed(() => {
  const groups: { communityId: number; communityName: string; items: Address[] }[] = []
  const indexBy = new Map<number, number>()
  for (const item of addresses.value) {
    const key = item.community_id || 0
    let idx = indexBy.get(key)
    if (idx === undefined) {
      idx = groups.length
      indexBy.set(key, idx)
      groups.push({ communityId: key, communityName: item.community_name || '', items: [] })
    }
    groups[idx].items.push(item)
  }
  return groups
})

onShow(() => {
  if (!userStore.isLogin) {
    goLogin()
    return
  }
  loadAddresses()
})

/**
 * 加载地址列表
 */
async function loadAddresses() {
  try {
    const res = await getAddressList()
    addresses.value = res.data || []
  } catch {
    addresses.value = []
  }
}

/**
 * 选择地址并返回
 * @param address 地址对象
 */
function onSelect(address: Address) {
  userStore.setAddress(address)
  goBack()
}

/**
 * 新增地址
 */
function onAdd() {
  goAddressEdit()
}

/**
 * 编辑地址
 * @param id 地址 ID
 */
function onEdit(id: number) {
  const address = addresses.value.find((item) => item.id === id)
  uni.navigateTo({
    url: `/pages/address/edit?id=${id}`,
    success: (res) => {
      if (address) {
        res.eventChannel.emit('addressData', address)
      }
    },
  })
}

/**
 * 删除地址
 * @param id 地址 ID
 */
function onDelete(id: number) {
  uni.showModal({
    title: '提示',
    content: '确定删除该地址吗？',
    success: async (res) => {
      if (res.confirm) {
        try {
          await deleteAddress(id)
          uni.showToast({ title: '已删除', icon: 'success' })
          loadAddresses()
        } catch {
          // ignore
        }
      }
    },
  })
}
</script>

<style scoped>
.address-list-page {
  min-height: 100vh;
  padding: 0 30rpx 140rpx;
  background-color: #f5f5f5;
}

.empty-wrap {
  display: flex;
  justify-content: center;
  padding-top: 200rpx;
}

.empty-text {
  font-size: 28rpx;
  color: #999999;
}

.community-group {
  margin-top: 20rpx;
}

.group-header {
  padding: 12rpx 8rpx;
}

.group-title {
  font-size: 28rpx;
  font-weight: bold;
  color: #333333;
}

.address-card {
  margin-bottom: 20rpx;
  padding: 24rpx;
  border-radius: 16rpx;
  background-color: #ffffff;
}

.address-top {
  display: flex;
  align-items: center;
  margin-bottom: 12rpx;
}

.address-top .name {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.address-top .phone {
  margin-left: 20rpx;
  font-size: 28rpx;
  color: #666666;
}

.default-tag {
  margin-left: 16rpx;
  padding: 2rpx 10rpx;
  border-radius: 8rpx;
  font-size: 20rpx;
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.1);
}

.address-text {
  font-size: 26rpx;
  color: #666666;
  line-height: 1.4;
}

.address-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 16rpx;
}

.action {
  margin-left: 24rpx;
  font-size: 26rpx;
  color: #666666;
}

.action.delete {
  color: #ff4d4f;
}

.add-btn {
  margin-top: 40rpx;
}

.add-btn .u-button {
  width: 100%;
}
</style>
