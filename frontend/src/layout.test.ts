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
    expect(gap).toBeCloseTo(21 * DIAM_SMALL, 3) // = (20+1)*8
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
