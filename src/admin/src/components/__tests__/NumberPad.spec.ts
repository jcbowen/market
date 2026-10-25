import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NumberPad from '../NumberPad.vue'

/**
 * NumberPad 数字小键盘单测（收银台触屏点按入口）
 * 布局规则：金额模式含小数点无清空键；整数模式含清空键无小数点；
 * 动作键（退格/清空/确认）抛独立事件，数字与小数点走 input
 */

describe('NumberPad', () => {
  it('整数模式：12 键布局，含清空不含小数点', () => {
    const wrapper = mount(NumberPad)
    const keys = wrapper.findAll('button').map((b) => b.text())
    expect(keys).toEqual(['1', '2', '3', '4', '5', '6', '7', '8', '9', '清空', '0', '⌫'])
  })

  it('金额模式：含小数点不含清空键', () => {
    const wrapper = mount(NumberPad, { props: { dot: true } })
    const keys = wrapper.findAll('button').map((b) => b.text())
    expect(keys).toContain('.')
    expect(keys).not.toContain('清空')
    expect(keys).toHaveLength(12)
  })

  it('数字键抛 input 事件（携带字符）', async () => {
    const wrapper = mount(NumberPad)
    await wrapper.findAll('button').find((b) => b.text() === '5')!.trigger('click')
    await wrapper.findAll('button').find((b) => b.text() === '0')!.trigger('click')
    expect(wrapper.emitted('input')).toEqual([['5'], ['0']])
  })

  it('小数点走 input 事件', async () => {
    const wrapper = mount(NumberPad, { props: { dot: true } })
    await wrapper.findAll('button').find((b) => b.text() === '.')!.trigger('click')
    expect(wrapper.emitted('input')).toEqual([['.']])
  })

  it('退格与清空抛独立事件（整数模式）', async () => {
    const wrapper = mount(NumberPad)
    const buttons = wrapper.findAll('button')
    await buttons.find((b) => b.text() === '⌫')!.trigger('click')
    await buttons.find((b) => b.text() === '清空')!.trigger('click')
    expect(wrapper.emitted('backspace')).toHaveLength(1)
    expect(wrapper.emitted('clear')).toHaveLength(1)
    expect(wrapper.emitted('input')).toBeUndefined()
  })

  it('确认键默认隐藏，showConfirm 时显示并抛 confirm', async () => {
    const hidden = mount(NumberPad)
    expect(hidden.findAll('button').some((b) => b.text() === '确认')).toBe(false)

    const shown = mount(NumberPad, { props: { showConfirm: true, confirmText: '查询' } })
    const confirmBtn = shown.findAll('button').find((b) => b.text() === '查询')!
    await confirmBtn.trigger('click')
    expect(shown.emitted('confirm')).toHaveLength(1)
  })

  it('清空键文案可自定义', () => {
    const wrapper = mount(NumberPad, { props: { clearText: '重输' } })
    expect(wrapper.findAll('button').some((b) => b.text() === '重输')).toBe(true)
  })
})
