import { describe, it, expect } from 'vitest'
import {
  computeBranchLayout,
  collectForkPoints,
  ColumnAllocator,
  toLayoutItems,
  MAIN_X,
  BRANCH_X_OFFSET,
} from './branchLayout'
import { computeLayout } from './layout'

function node(id: string, seq: number, branch: string, extra: Partial<{ parentId: string; kind: string; status: string; copiedFrom: string }> = {}) {
  return {
    id,
    seq,
    parentId: extra.parentId ?? '',
    branch,
    kind: extra.kind ?? 'normal',
    status: extra.status ?? 'done',
    copiedFrom: extra.copiedFrom ?? '',
    parts: [],
  }
}

function mainChain(count: number, branch = 'main'): ReturnType<typeof node>[] {
  const ns: ReturnType<typeof node>[] = []
  for (let i = 1; i <= count; i++) {
    ns.push(node('n' + i, i, branch, { parentId: i > 1 ? 'n' + (i - 1) : '' }))
  }
  return ns
}

function forkBranch(id: string, seqStart: number, anchorId: string, parentId: string, count = 3): ReturnType<typeof node>[] {
  const ns: ReturnType<typeof node>[] = []
  ns.push(node(id, seqStart, id, { parentId, copiedFrom: anchorId, kind: 'normal' }))
  for (let i = 1; i < count; i++) {
    ns.push(node(id + '-t' + i, seqStart + i, id, { parentId: i === 1 ? id : id + '-t' + (i - 1) }))
  }
  return ns
}

describe('ColumnAllocator', () => {
  it('isFree 返回 true 当列无占用', () => {
    const a = new ColumnAllocator()
    expect(a.isFree(100, 10, 20)).toBe(true)
  })

  it('reserve 后同列重叠区间被拒绝', () => {
    const a = new ColumnAllocator()
    a.reserve(100, 10, 20)
    expect(a.isFree(100, 15, 25)).toBe(false)
    expect(a.isFree(100, 0, 9)).toBe(true)
    expect(a.isFree(100, 21, 30)).toBe(true)
  })

  it('findFreeNear 对称外扩：左优先，被占则跳右侧', () => {
    const a = new ColumnAllocator()
    a.reserve(MAIN_X, 0, 100)
    a.reserve(MAIN_X - BRANCH_X_OFFSET, 0, 100)
    // 左 65 被占 → 右 135
    expect(a.findFreeNear(MAIN_X, 0, 100)).toBe(MAIN_X + BRANCH_X_OFFSET)
  })

  it('findFreeNear 复用在竖直区间不冲突的列', () => {
    const a = new ColumnAllocator()
    a.reserve(MAIN_X - BRANCH_X_OFFSET, 0, 100)
    // 左列区间 [0,100] 与 [200,300] 不重叠 → 可复用左列
    expect(a.findFreeNear(MAIN_X, 200, 300)).toBe(MAIN_X - BRANCH_X_OFFSET)
  })
})

describe('collectForkPoints', () => {
  it('收集 fork 分支并按锚点 seq 排序', () => {
    const main = mainChain(5)
    // fork 根的 parentId = 视觉锚点（前端用 root.parentId 找分叉点）
    const f1 = forkBranch('f1', 10, 'n4', 'n3') // 锚点 n3
    const f2 = forkBranch('f2', 13, 'n5', 'n3') // 锚点 n3（同一锚点）
    const groups = new Map([
      ['main', main],
      ['f1', f1],
      ['f2', f2],
    ])
    const points = collectForkPoints(main, ['f1', 'f2'], groups)
    expect(points.length).toBe(1)
    expect(points[0].anchor.id).toBe('n3')
    expect(points[0].forks.length).toBe(2)
  })
})

describe('computeBranchLayout', () => {
  it('空输入返回空布局', () => {
    const r = computeBranchLayout([])
    expect(r.segments).toEqual([])
    expect(r.forks).toEqual([])
  })

  it('无 fork：单列在 MAIN_X，无弧线', () => {
    const r = computeBranchLayout(mainChain(4))
    expect(r.segments.length).toBe(1)
    expect(r.segments[0].x).toBe(MAIN_X)
    expect(r.segments[0].items.length).toBe(4)
    expect(r.forks.length).toBe(0)
  })

  it('单 fork（2 子方向）：fork 在左列，主链继续在右列，两条弧线', () => {
    const main = mainChain(5)
    const f1 = forkBranch('f1', 6, 'n3', 'n2') // 锚点 = n2（parentId）
    const r = computeBranchLayout([...main, ...f1])
    expect(r.segments.length).toBe(3) // 祖先段 + 主链继续段 + fork 分支
    expect(r.forks.length).toBe(2)    // fork 弧线 + 主链继续弧线

    const forkSeg = r.segments.find(s => s.items[0].nodeId === 'f1')
    expect(forkSeg!.x).toBe(MAIN_X - BRANCH_X_OFFSET)
    // 主链继续段（n3 起）在右列
    const tailSeg = r.segments.find(s => s.items.some(it => it.nodeId === 'n3'))
    expect(tailSeg!.x).toBe(MAIN_X + BRANCH_X_OFFSET)
    // fork 首珠在锚点（n2）下方 BRANCH_Y_DROP
    const anchorY = r.segments.find(s => s.items.some(it => it.nodeId === 'n2'))!
      .items.find(it => it.nodeId === 'n2')!.y
    expect(forkSeg!.items[0].y).toBe(anchorY + 36)
  })

  it('同一锚点 2 fork（3 子方向）：主链继续竖直，fork 对称两侧', () => {
    const main = mainChain(5)
    const f1 = forkBranch('f1', 6, 'n3', 'n2')
    const f2 = forkBranch('f2', 9, 'n4', 'n2')
    const r = computeBranchLayout([...main, ...f1, ...f2])
    const ancestorSeg = r.segments.find(s => s.items.some(it => it.nodeId === 'n2'))!
    const anchorY = ancestorSeg.items.find(it => it.nodeId === 'n2')!.y

    // 主链继续段竖直：n3 段与祖先段同列
    const tailSeg = r.segments.find(s => s.items.some(it => it.nodeId === 'n3'))
    expect(tailSeg!.x).toBe(ancestorSeg.x)

    // fork 对称：一左一右
    const f1x = r.segments.find(s => s.items[0].nodeId === 'f1')!.x
    const f2x = r.segments.find(s => s.items[0].nodeId === 'f2')!.x
    expect(Math.abs(f1x - ancestorSeg.x)).toBe(BRANCH_X_OFFSET)
    expect(Math.abs(f2x - ancestorSeg.x)).toBe(BRANCH_X_OFFSET)
    expect(f1x).not.toBe(f2x)
    // 两条 fork 弧线
    expect(r.forks.filter(f => f.to.y === anchorY + 36).length).toBe(2)
  })

  it('多分叉点：第一 2 叉、第二 3 叉，列不重叠', () => {
    const main = mainChain(8)
    const f1 = forkBranch('f1', 9, 'n3', 'n2')   // 锚点 n2，N=2 偶数
    const f2 = forkBranch('f2', 12, 'n6', 'n5')  // 锚点 n5，N=3 奇数
    const f3 = forkBranch('f3', 15, 'n7', 'n5')
    const r = computeBranchLayout([...main, ...f1, ...f2, ...f3])
    expect(r.forks.length).toBe(4) // f1弧线 + 主链继续1弧线 + f2/f3弧线（第二点 N=3 主链竖直无弧线）

    // 所有列互不重叠（同一列内区间不重叠）
    const byCol = new Map<number, [number, number][]>()
    for (const s of r.segments) {
      if (!byCol.has(s.x)) byCol.set(s.x, [])
      byCol.get(s.x)!.push([s.topY, s.bottomY])
    }
    for (const [, ranges] of byCol) {
      ranges.sort((a, b) => a[0] - b[0])
      for (let i = 1; i < ranges.length; i++) {
        expect(ranges[i][0]).toBeGreaterThanOrEqual(ranges[i - 1][1])
      }
    }
  })

  it('toLayoutItems 保留 status/nodeId/parentId', () => {
    const ns = [node('a', 1, 'm', { parentId: '', status: 'done' })]
    const laid = computeLayout([{ id: 'a', important: false, ghost: false }])
    const items = toLayoutItems(ns, laid)
    expect(items[0].nodeId).toBe('a')
    expect(items[0].status).toBe('done')
    expect(items[0].parentId).toBe('')
  })
})
