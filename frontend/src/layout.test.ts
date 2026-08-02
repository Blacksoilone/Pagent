import { describe, it, expect } from 'vitest'
import { computeLayout, DIAM_SMALL, GAP_IMPORTANT, type LayoutNode } from './layout'

function node(id: string, opts: Partial<LayoutNode> = {}): LayoutNode {
  return { id, important: false, ghost: false, ...opts }
}

describe('computeLayout 手串布局', () => {
  it('全普通节点：等间距 = 点直径', () => {
    const nodes = [node('a'), node('b'), node('c')]
    const r = computeLayout(nodes)
    expect(r.length).toBe(3)
    expect(r[1].y - r[0].y).toBeCloseTo(DIAM_SMALL)
    expect(r[2].y - r[1].y).toBeCloseTo(DIAM_SMALL)
  })

  it('两个重要节点：间距至少 6D，中间普通节点均匀填充', () => {
    const nodes = [
      node('a', { important: true }),
      node('b'),
      node('c', { important: true }),
    ]
    const r = computeLayout(nodes)
    // 重要节点 a → c 间距 ≥ 6D
    const gap = r[2].y - r[0].y
    expect(gap).toBeGreaterThanOrEqual(GAP_IMPORTANT)
    // 中间普通节点 b 在 a 和 c 正中间
    const mid = (r[0].y + r[2].y) / 2
    expect(r[1].y).toBeCloseTo(mid, 5)
  })

  it('两个重要节点间普通节点多时，间距扩展且均匀', () => {
    const nodes = [
      node('a', { important: true }),
      node('b1'), node('b2'), node('b3'), node('b4'),
      node('c', { important: true }),
    ]
    const r = computeLayout(nodes)
    const gap = r[5].y - r[0].y
    // 4 个普通节点需要 (4+1)*d = 5d = 40px，大于 6D=96？不，5*8=40 < 96
    // 所以仍是最小 6D
    expect(gap).toBeCloseTo(GAP_IMPORTANT)
    // 普通节点均匀：相邻间距相等
    const d1 = r[2].y - r[1].y
    const d2 = r[3].y - r[2].y
    const d3 = r[4].y - r[3].y
    expect(d1).toBeCloseTo(d2, 5)
    expect(d2).toBeCloseTo(d3, 5)
  })

  it('很多普通节点时扩展重要节点间距', () => {
    // 20 个普通节点夹在两个重要节点之间：需要 (20+1)*8 = 168px > 96
    const nodes: LayoutNode[] = [node('a', { important: true })]
    for (let i = 0; i < 20; i++) nodes.push(node('n' + i))
    nodes.push(node('z', { important: true }))
    const r = computeLayout(nodes)
    const gap = r[r.length - 1].y - r[0].y
    expect(gap).toBeGreaterThan(GAP_IMPORTANT) // 已扩展
    // 基础段 = 首尾相切(12+12) + 中间 19*8 = 176
    expect(gap).toBeCloseTo(12 + 19 * DIAM_SMALL + 12, 3)
  })

  it('虚节点按普通节点处理', () => {
    const nodes = [
      node('a', { important: true }),
      node('g', { ghost: true }),
      node('c', { important: true }),
    ]
    const r = computeLayout(nodes)
    expect(r[1].node.ghost).toBe(true)
    expect(r[2].y - r[0].y).toBeGreaterThanOrEqual(GAP_IMPORTANT)
  })
})

it('调试：1 重要 + 3 普通', () => {
  const nodes = [
    node('n0'),
    node('imp', { important: true }),
    node('n2'),
    node('n3'),
  ]
  const r = computeLayout(nodes)
  console.log('布局:', r.map(x => `${x.node.id}@${x.y.toFixed(1)}`).join(' '))
  console.log('间距:', r.slice(1).map((x, i) => (x.y - r[i].y).toFixed(1)).join(' '))
  expect(r.length).toBe(4)
})

describe('相切约束', () => {
  it('小点与大点相邻：中心距 = 两半径之和 (4+8=12)', () => {
    const nodes = [
      node('s1'),
      node('big', { important: true }),
      node('s2'),
    ]
    const r = computeLayout(nodes)
    // s1 → big：12
    expect(r[1].y - r[0].y).toBeCloseTo(12, 5)
    // big → s2：链尾普通节点按 d=8？还是也相切？——链尾无约束，按 8
    // 但 big 是重要节点，s2 是普通，视觉上应相切 12？当前实现链尾段用 d。
    // 这里验证 big 与 s2 至少不重叠（≥ 8）
    expect(r[2].y - r[1].y).toBeGreaterThanOrEqual(8)
  })

  it('重要节点之间的段长包含首尾相切（含大点半径）', () => {
    // 两个重要节点之间 1 个普通节点：段长 = max(6D, 12+12) = 96
    const nodes = [
      node('a', { important: true }),
      node('m'),
      node('b', { important: true }),
    ]
    const r = computeLayout(nodes)
    // a → b 中心距 ≥ 6D = 96
    expect(r[2].y - r[0].y).toBeCloseTo(GAP_IMPORTANT)
    // m 在中间（均匀）
    expect(r[1].y).toBeCloseTo((r[0].y + r[2].y) / 2, 5)
  })

  it('大量普通节点时：首尾段相切 12，中间段 8', () => {
    const nodes: LayoutNode[] = [node('a', { important: true })]
    for (let i = 0; i < 20; i++) nodes.push(node('n' + i))
    nodes.push(node('z', { important: true }))
    const r = computeLayout(nodes)
    // 段长 = 12 + 19*8 + 12 = 176（已超过 6D=96，不扩展富余）
    const gap = r[r.length - 1].y - r[0].y
    expect(gap).toBeCloseTo(176, 5)
    // 首段 = 12（大→小相切）
    expect(r[1].y - r[0].y).toBeCloseTo(12, 5)
    // 中间段 = 8
    expect(r[3].y - r[2].y).toBeCloseTo(8, 5)
    // 尾段 = 12（小→大相切）
    expect(r[r.length - 1].y - r[r.length - 2].y).toBeCloseTo(12, 5)
  })
})

describe('虚节点按重要节点规则', () => {
  it('链尾虚节点与其前大节点间距 ≥ 6D', () => {
    const nodes = [
      node('a', { important: true }),
      node('n1'),
      node('n2'),
      node('n3'),
      node('g', { ghost: true }), // 链尾虚节点
    ]
    const r = computeLayout(nodes)
    // 虚节点 g 与重要节点 a 之间：中间 3 个普通节点，段长 = max(6D, 12+2*8+12=44) = 96
    const gap = r[4].y - r[0].y
    expect(gap).toBeGreaterThanOrEqual(GAP_IMPORTANT)
  })

  it('链首虚节点与重要节点之间按 6D', () => {
    const nodes = [
      node('g', { ghost: true }),
      node('a', { important: true }),
    ]
    const r = computeLayout(nodes)
    expect(r[1].y - r[0].y).toBeGreaterThanOrEqual(GAP_IMPORTANT)
  })

  it('虚节点-普通节点相邻：6D 主导时均匀分配，不重叠', () => {
    const nodes = [
      node('g', { ghost: true }),
      node('s'),
      node('z', { important: true }),
    ]
    const r = computeLayout(nodes)
    // g、z 都是大节点，中间 1 个普通节点：段长 = 6D = 96
    const gap = r[2].y - r[0].y
    expect(gap).toBeCloseTo(GAP_IMPORTANT, 5)
    // 均匀填充：s 在 g 和 z 正中间
    expect(r[1].y).toBeCloseTo((r[0].y + r[2].y) / 2, 5)
    // 不重叠：每段 ≥ 相切
    expect(r[1].y - r[0].y).toBeGreaterThanOrEqual(12)
    expect(r[2].y - r[1].y).toBeGreaterThanOrEqual(12)
  })
})

