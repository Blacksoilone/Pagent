import { describe, it, expect } from 'vitest'
import { computeLayout, DEFAULT_OPTIONS, DIAM_SMALL, DIAM_BIG, GAP_IMPORTANT, type LayoutNode } from './layout'

function node(id: string, opts: Partial<LayoutNode> = {}): LayoutNode {
  return { id, important: false, ghost: false, ...opts }
}

describe('computeLayout 手串布局', () => {
  it('全普通节点：首尾自动大节点，中间等间距', () => {
    const nodes = [node('a'), node('b'), node('c')]
    const r = computeLayout(nodes)
    expect(r.length).toBe(3)
    // a、c 自动是大节点（big=true）
    expect(r[0].big).toBe(true)
    expect(r[2].big).toBe(true)
    expect(r[1].big).toBe(false)
    // a→c 之间 1 个小点：间距 = 6D
    expect(r[2].y - r[0].y).toBeCloseTo(GAP_IMPORTANT, 5)
    // b 在中间
    expect(r[1].y).toBeCloseTo((r[0].y + r[2].y) / 2, 5)
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
    // 基础段 = 28 + 3*24 + 28 = 128 > 96 → 小点主导
    expect(gap).toBeCloseTo(28 + 3 * 24 + 28, 5)
    // 普通节点均匀：相邻间距相等（富余 0）
    const d1 = r[2].y - r[1].y
    const d2 = r[3].y - r[2].y
    const d3 = r[4].y - r[3].y
    expect(d1).toBeCloseTo(24, 5)
    expect(d2).toBeCloseTo(24, 5)
    expect(d3).toBeCloseTo(24, 5)
  })

  it('很多普通节点时扩展重要节点间距', () => {
    // 20 个普通节点夹在两个重要节点之间：需要 (20+1)*8 = 168px > 96
    const nodes: LayoutNode[] = [node('a', { important: true })]
    for (let i = 0; i < 20; i++) nodes.push(node('n' + i))
    nodes.push(node('z', { important: true }))
    const r = computeLayout(nodes)
    const gap = r[r.length - 1].y - r[0].y
    expect(gap).toBeGreaterThan(GAP_IMPORTANT) // 已扩展
    // 基础段 = 首尾(28+28) + 中间 19*24 = 512
    expect(gap).toBeCloseTo(28 + 19 * 24 + 28, 3)
  })

  it('虚节点按大节点处理（中间有小点时 6D 生效）', () => {
    const nodes = [
      node('a', { important: true }),
      node('g', { ghost: true }),
      node('c', { important: true }),
    ]
    const r = computeLayout(nodes)
    expect(r[1].node.ghost).toBe(true)
    // a→g 相邻（k=0）：间距 = gapBB = 32（不用 6D）
    expect(r[1].y - r[0].y).toBeCloseTo(32, 5)
    // g→c 相邻（k=0）：间距 = gapBB = 32
    expect(r[2].y - r[1].y).toBeCloseTo(32, 5)
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
  it('链尾节点自动大节点：首尾大节点之间 1 个小点 → 6D', () => {
    const nodes = [
      node('s1'),
      node('big', { important: true }),
      node('s2'),
    ]
    const r = computeLayout(nodes)
    // s1 是链首 → 自动大节点；big 重要；s2 链尾 → 自动大节点
    expect(r[0].big).toBe(true)
    expect(r[1].big).toBe(true)
    expect(r[2].big).toBe(true)
    // 两个区间都是 k=0（大点直接相邻）：间距 = gapBB = 32
    expect(r[1].y - r[0].y).toBeCloseTo(32, 5)
    expect(r[2].y - r[1].y).toBeCloseTo(32, 5)
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
    // 段长 = 28 + 19*24 + 28 = 512（已超过 6D=96，不扩展富余）
    const gap = r[r.length - 1].y - r[0].y
    expect(gap).toBeCloseTo(512, 5)
    // 首段 = 28（大→小：半径和+间隙）
    expect(r[1].y - r[0].y).toBeCloseTo(28, 5)
    // 中间段 = 24（小点：半径和+间隙）
    expect(r[3].y - r[2].y).toBeCloseTo(24, 5)
    // 尾段 = 28（小→大）
    expect(r[r.length - 1].y - r[r.length - 2].y).toBeCloseTo(28, 5)
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

  it('链首虚节点与重要节点相邻（k=0）：间距 = gapBB = 32', () => {
    const nodes = [
      node('g', { ghost: true }),
      node('a', { important: true }),
    ]
    const r = computeLayout(nodes)
    expect(r[1].y - r[0].y).toBeCloseTo(32, 5)
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


describe('6D 是最小值（可被小点数量推远）', () => {
  function gapWithK(k: number): number {
    const nodes: LayoutNode[] = [node('a', { important: true })]
    for (let i = 0; i < k; i++) nodes.push(node('n' + i))
    nodes.push(node('z', { important: true }))
    const r = computeLayout(nodes)
    return r[r.length - 1].y - r[0].y
  }

  it('k=1：间距恰好 = 6D（下限）', () => {
    expect(gapWithK(1)).toBeCloseTo(GAP_IMPORTANT, 5)
  })

  it('k=2：仍为 6D（基础段 80 < 96）', () => {
    // 基础段 = 28 + 1*24 + 28 = 80 < 6D=96 → 6D 主导
    expect(gapWithK(2)).toBeCloseTo(GAP_IMPORTANT, 5)
  })

  it('k=3：超过 6D，小点距离主导（基础段 104 > 96）', () => {
    // 基础段 = 28 + 2*24 + 28 = 104 > 96 → 小点主导
    expect(gapWithK(3)).toBeCloseTo(28 + 2 * 24 + 28, 5)
    expect(gapWithK(3)).toBeGreaterThan(GAP_IMPORTANT)
  })

  it('两个区间独立：一个小点区间 6D，多小点区间扩展', () => {
    const nodes: LayoutNode[] = [node('a', { important: true })]
    nodes.push(node('only'))                    // 区间1：1个小点
    nodes.push(node('b', { important: true }))
    for (let i = 0; i < 15; i++) nodes.push(node('m' + i)) // 区间2：15个小点
    nodes.push(node('c', { important: true }))

    const r = computeLayout(nodes)
    // 找大点索引
    const ia = 0, ib = 2, ic = r.length - 1
    // 区间1 (a→b)：1个小点 → 6D
    expect(r[ib].y - r[ia].y).toBeCloseTo(GAP_IMPORTANT, 5)
    // 区间2 (b→c)：15个小点 → 基础段 = 28+14*24+28 = 392 > 96
    const gap2 = r[ic].y - r[ib].y
    expect(gap2).toBeCloseTo(28 + 14 * 24 + 28, 5)
    expect(gap2).toBeGreaterThan(GAP_IMPORTANT)
  })
})

describe('自定义参数', () => {
  it('自定义 d/D/间距：布局按参数计算', () => {
    const nodes = [
      node('a', { important: true }),
      node('m'),
      node('b', { important: true }),
    ]
    const r = computeLayout(nodes, { diamSmall: 10, diamBig: 20, gapImportant: 100 })
    // 1 个小点：段长 = max(100, 相切 15+15=30) = 100
    expect(r[2].y - r[0].y).toBeCloseTo(100, 5)
    // 首尾相切 = R_S(5) + R_B(10) = 15
    expect(r[1].y - r[0].y).toBeCloseTo(50, 5) // 100 均匀两段
    // m 在中间
    expect(r[1].y).toBeCloseTo((r[0].y + r[2].y) / 2, 5)
  })

  it('默认参数与常量一致', () => {
    expect(DEFAULT_OPTIONS.diamSmall).toBe(DIAM_SMALL)
    expect(DEFAULT_OPTIONS.diamBig).toBe(DIAM_BIG)
    expect(DEFAULT_OPTIONS.gapImportant).toBe(GAP_IMPORTANT)
  })
})

describe('小点间距恒定（大点间距可调）', () => {
  function build(k: number) {
    const nodes: LayoutNode[] = [node('a', { important: true })]
    for (let i = 0; i < k; i++) nodes.push(node('n' + i))
    nodes.push(node('z', { important: true }))
    return nodes
  }

  it('2 个小点：富余分到所有段，小点间距 ≥ 基准', () => {
    const nodes = build(2)
    // 基础段 = 28 + 1*24 + 28 = 80；段长 = max(96, 80) = 96
    const r96 = computeLayout(nodes, { diamSmall: 8, diamBig: 16, gapImportant: 96 })
    expect(r96[3].y - r96[0].y).toBeCloseTo(96, 5)
    // 富余 96-80=16 分到 3 段 → 每段 +5.33
    // 小点之间 ≈ 24 + 5.33 = 29.33
    const midGap = r96[2].y - r96[1].y
    expect(midGap).toBeCloseTo(24 + 16 / 3, 5)
  })

  it('大点间距低于基础段时被小点推开', () => {
    const nodes = build(2)
    const r = computeLayout(nodes, { diamSmall: 8, diamBig: 16, gapImportant: 40 })
    // 基础段 80 > 40 → 段长 = 80
    expect(r[3].y - r[0].y).toBeCloseTo(80, 5)
    // 小点之间间距 = 24（富余 0）
    expect(r[2].y - r[1].y).toBeCloseTo(24, 5)
  })
})
