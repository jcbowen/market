<template>
  <n-cascader
    v-model:value="inner"
    :options="options"
    check-strategy="child"
    show-path
    filterable
    clearable
    :loading="loading"
    :placeholder="placeholder || '请选择省 / 市 / 区，可输入名称搜索'"
    @update:value="handleChange"
  />
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { CascaderOption } from 'naive-ui'
import { getAllRegions } from '@/api/region'
import type { Region, RegionSelection } from '@/api/region'

/**
 * 行政区划三级联动选择器（省 / 市 / 区县），支持输入名称搜索
 * 数据来源：管理端 /admin/regions 分页接口一次性拉全量（约 3400 条），
 * 前端本地建树后交给 n-cascader filterable 过滤——懒加载模式下级联
 * 搜索只能命中已展开的节点，因此这里放弃逐级懒加载。
 * v-model 语义为区划代码（value），选中或回显时通过 select 事件
 * 回传完整路径（省/市/区的代码与名称），供表单同时提交名称与代码。
 * 存量数据（只有名称没有代码）通过 setValueByPath 按名称逐级匹配回显。
 */

const props = defineProps<{
  /** 占位提示 */
  placeholder?: string
  /** 受控值：区县级区划代码（叶子节点） */
  value?: string | null
}>()

const emit = defineEmits<{
  /** 选中路径变化（清空时三个字段均为 null） */
  (e: 'update:value', code: string | null): void
  (e: 'select', selection: RegionSelection): void
}>()

const inner = ref<string | null>(props.value ?? null)
const options = ref<CascaderOption[]>([])
const loading = ref(false)

/** 区划代码 → 区划信息映射（构建 options 时登记，用于回传路径） */
const regionMap = new Map<string, Region>()

function toOption(r: Region): CascaderOption {
  regionMap.set(r.code, r)
  return {
    label: r.name,
    value: r.code,
    // 层级以后端返回的 level 为准：直辖市省下直接挂区县（无市级层）
    isLeaf: r.level >= 3,
  }
}

/**
 * 拉取全量区划并本地建树（仅正常状态）
 * 父级缺失（被停用等）的节点直接丢弃，与旧懒加载「父级不可达则子级不可见」的行为一致
 * 并发安全：组件初始化与外部 setValueByPath 可能同时触发，共享同一在途 Promise，
 * 避免后者拿到未就绪的空树导致名称匹配落空（回显失败）
 */
let loadPromise: Promise<void> | null = null

function loadAll(): Promise<void> {
  if (options.value.length > 0) return Promise.resolve()
  if (!loadPromise) {
    loadPromise = (async () => {
      loading.value = true
      try {
        const list = (await getAllRegions()).filter((r) => r.status === 1)
        const nodeByCode = new Map<string, CascaderOption>()
        for (const r of list) nodeByCode.set(r.code, toOption(r))

        const roots: CascaderOption[] = []
        for (const r of list) {
          const opt = nodeByCode.get(r.code)!
          if (r.parent_code === '0') {
            roots.push(opt)
            continue
          }
          const parent = nodeByCode.get(r.parent_code)
          if (!parent) continue
          if (!parent.children) parent.children = []
          parent.children.push(opt)
        }
        options.value = roots
      } finally {
        loading.value = false
        loadPromise = null
      }
    })()
  }
  return loadPromise
}

onMounted(loadAll)

function findRegionByCode(code: string | number | null | undefined): Region | null {
  if (code === null || code === undefined) return null
  return regionMap.get(String(code)) ?? null
}

/**
 * 选中变化：按路径组装省/市/区选择结果并回传
 * 通过父级代码逐级回溯 regionMap，各节点层级按自身代码位数判定
 * （2 位省 / 4 位市 / 6 位区县）
 * 直辖市无市级层（区县直挂省）时，市回传为省的同名同码别名，
 * 保证落库的 city 字段始终有值（市与省同名口径）
 */
function handleChange(value: string | number | null) {
  emit('update:value', value === null ? null : String(value))

  const district = findRegionByCode(value)
  let province: Region | null = null
  let city: Region | null = null
  let node = district
  while (node && node.parent_code && node.parent_code !== '0') {
    const parent = findRegionByCode(node.parent_code)
    if (!parent) break
    if (parent.code.length === 4) {
      city = parent
    } else if (parent.code.length === 2) {
      province = parent
      break
    } else {
      // 异常层级，继续向上兜底
    }
    node = parent
  }
  // 直辖市：市与省同名同码（仅在有省有区且无市时套用）
  if (!city && province && district) city = province
  emit('select', { province, city, district })
}

/**
 * 按名称匹配并回显区划路径（存量数据回显用）
 * 匹配失败时仅清空选择器显示，不回传 select（保留表单原有名称，由用户重新选择）
 * 兼容直辖市：市级缺失（或市级名与省级同名 / "市辖区"）时按省下直接挂区县匹配
 * @param selection 含省/市/区名称的选择结果（code 可为空，按名称逐级匹配）
 */
async function setValueByPath(selection: { province: Region | null; city: Region | null; district: Region | null }) {
  await loadAll()

  const { province, city, district } = selection
  if (!province || !district) {
    inner.value = null
    return
  }

  // 全量树已就绪，直接在本地 options 中逐级匹配
  const provOpt =
    options.value.find((o) => String(o.value) === province.code) ??
    options.value.find((o) => o.label === province.name)
  if (!provOpt) {
    inner.value = null
    return
  }
  const levelOneChildren = provOpt.children ?? []

  // 市级存在且为正常地级（4 位码）时走 省→市→区 三级路径
  const cityOpt =
    city && city.code.length === 4 && city.code !== province.code
      ? levelOneChildren.find((o) => String(o.value) === city.code) ??
        levelOneChildren.find((o) => o.label === city.name && String(o.value).length === 4)
      : undefined
  let districtOpt: CascaderOption | undefined
  if (cityOpt) {
    const districtChildren = cityOpt.children ?? []
    districtOpt =
      districtChildren.find((o) => String(o.value) === district.code) ??
      districtChildren.find((o) => o.label === district.name)
  } else {
    // 直辖市或存量市级无法匹配：区县直接挂在省级下
    districtOpt =
      levelOneChildren.find((o) => String(o.value) === district.code) ??
      levelOneChildren.find((o) => o.label === district.name)
  }
  if (!districtOpt) {
    inner.value = null
    return
  }

  inner.value = String(districtOpt.value)
  // 触发一次 select，让表单同步代码（存量数据可能缺代码）；
  // 直辖市（市级节点不存在）时市回传为省的同名同码别名，与 handleChange 口径一致
  const districtRegion = findRegionByCode(districtOpt.value)
  emit('select', {
    province: findRegionByCode(provOpt.value),
    city: cityOpt ? findRegionByCode(cityOpt.value) : findRegionByCode(provOpt.value),
    district: districtRegion,
  })
}

defineExpose({ setValueByPath })
</script>
