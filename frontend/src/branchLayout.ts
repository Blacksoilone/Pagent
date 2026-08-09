// 分支布局：倒 Y 分叉，全部纵向，支持主链多个分叉点。
//
// 列分配：不是"一链一槽位"，而是"区间冲突检测"——
//   每条链只占用自己的竖直区间 [y1, y2]；新链优先复用已有列，
//   仅当与同列已有链的竖直区间重叠时才外移一列。
//
// 结构：
//   主链从 x=MAIN_X 开始；每个分叉点把主链分成"祖先段 + 继续段"
//   分叉点的子方向 = fork 分支们 + 主链继续段，按奇偶规则分配列：
//     子方向数 N 为奇数（如 3）：主链继续段竖直对齐（保持分叉点所在列）
//     子方向数 N 为偶数（如 2）：主链继续段在最右一列，fork 分支在左
//   分叉点用三次贝塞尔弧线连到每个偏移子方向的第一个节点（左上 → 右下）
//   所有链纵向（从上到下），虚节点在底部

import { computeLayout, type LayoutNode, type LayoutResult, type LayoutOptions } from './layout'

export interface BranchNode {
  id: string
  seq: number
  parentId: string
  branch: string
  kind: string
  status: string
  copiedFrom: string
  parts: { seq: number; role: string; content: string }[]
}

export interface LayoutItem extends LayoutResult {
  status: string
  nodeId: string
  parentId: string
}

export interface BranchSegment {
  x: number
  items: LayoutItem[]
  topY: number
  bottomY: number
}

export interface ForkConn {
  from: { x: number; y: number }
  to: { x: number; y: number }
  d: string
}

export interface BranchLayoutResult {
  segments: BranchSegment[]
  forks: ForkConn[]
  width: number
  height: number
}

export const BRANCH_X_OFFSET = 35
export const BRANCH_Y_DROP = 36
export const MAIN_X = 100

// 分叉弧线：三次贝塞尔，先垂直向下再弯向分支（左上 → 右下）。
export function forkPath(from: { x: number; y: number }, to: { x: number; y: number }): string {
  const dy = to.y - from.y
  const k = Math.max(20, dy * 0.6)
  return `M ${from.x},${from.y} C ${from.x},${from.y + k} ${to.x},${to.y - k} ${to.x},${to.y}`
}

// 列区间分配器：每列记录已占用竖直区间，新链优先复用不冲突的列。
// 独立于布局算法，后续链生成/连接/合并可复用同一分配策略。
export class ColumnAllocator {
  private ranges = new Map<number, Array<[number, number]>>()

  isFree(col: number, y1: number, y2: number): boolean {
    const rs = this.ranges.get(col)
    if (!rs) return true
    return !rs.some(([a, b]) => y1 <= b && y2 >= a)
  }

  reserve(col: number, y1: number, y2: number): void {
    if (!this.ranges.has(col)) this.ranges.set(col, [])
    this.ranges.get(col)!.push([y1, y2])
  }

  // 从 centerCol 向外对称找第一个竖直区间不冲突的列（-1,+1,-2,+2...）。
  findFreeNear(centerCol: number, y1: number, y2: number, step: number = BRANCH_X_OFFSET): number {
    let dist = 1
    for (;;) {
      for (const side of [-1, 1]) {
        const cand = centerCol + side * dist * step
        if (this.isFree(cand, y1, y2)) return cand
      }
      dist++
    }
  }

  // 从 centerCol 向单侧（side = -1 左 / +1 右）逐列外移找不冲突列。
  findFreeSide(centerCol: number, y1: number, y2: number, side: number, step: number = BRANCH_X_OFFSET): number {
    let dist = 1
    for (;;) {
      const cand = centerCol + side * dist * step
      if (this.isFree(cand, y1, y2)) return cand
      dist++
    }
  }

  columns(): number[] {
    return [...this.ranges.keys()]
  }

  bounds(): { min: number; max: number } {
    const cols = this.columns()
    if (cols.length === 0) return { min: MAIN_X, max: MAIN_X }
    return { min: Math.min(...cols), max: Math.max(...cols) }
  }
}

// 纯函数：branch 分组 + 主链识别。
export function groupByBranch(nodes: BranchNode[]): Map<string, BranchNode[]> {
  const groups = new Map<string, BranchNode[]>()
  for (const n of nodes) {
    if (!groups.has(n.branch)) groups.set(n.branch, [])
    groups.get(n.branch)!.push(n)
  }
  return groups
}

// 纯函数：找主链（根节点没有 copiedFrom 的分支）。
export function findMainBranch(groups: Map<string, BranchNode[]>): string {
  for (const [b, ns] of groups) {
    if (!ns.some(n => n.copiedFrom)) return b
  }
  return [...groups.keys()][0] ?? ''
}

// 纯函数：收集分叉点（fork 分支根 copiedFrom 非空 → 其 parent 在主链上的节点）。
export function collectForkPoints(
  mainNodes: BranchNode[],
  forkBranches: string[],
  groups: Map<string, BranchNode[]>,
): { anchor: BranchNode; forks: BranchNode[][] }[] {
  const forkMap = new Map<string, BranchNode[][]>()
  for (const b of forkBranches) {
    const ns = [...(groups.get(b)!)].sort((a, c) => a.seq - c.seq)
    const root = ns.find(n => n.copiedFrom) || ns[0]
    const a = mainNodes.find(n => n.id === root.parentId)
    if (!a) continue
    if (!forkMap.has(a.id)) forkMap.set(a.id, [])
    forkMap.get(a.id)!.push(ns)
  }
  return [...forkMap.entries()]
    .map(([anchorId, forks]) => ({
      anchor: mainNodes.find(n => n.id === anchorId)!,
      forks,
    }))
    .filter(p => p.anchor)
    .sort((a, c) => a.anchor.seq - c.anchor.seq)
}

// 纯函数：LayoutResult → LayoutItem（补 status/nodeId/parentId）。
export function toLayoutItems(ns: BranchNode[], laid: LayoutResult[]): LayoutItem[] {
  return laid.map((it, i) => ({
    ...it,
    status: ns[i].status,
    nodeId: ns[i].id,
    parentId: ns[i].parentId,
  }))
}

function layoutSeq(ns: BranchNode[], options?: LayoutOptions) {
  const lns: LayoutNode[] = ns.map(n => ({
    id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
  }))
  return computeLayout(lns, options)
}

export function computeBranchLayout(nodes: BranchNode[], options?: LayoutOptions): BranchLayoutResult {
  if (nodes.length === 0) return { segments: [], forks: [], width: 300, height: 300 }

  const groups = groupByBranch(nodes)
  const mainBranch = findMainBranch(groups)
  if (!mainBranch) return { segments: [], forks: [], width: 300, height: 300 }

  const mainNodes = [...(groups.get(mainBranch)!)].sort((a, c) => a.seq - c.seq)
  const forkBranches = [...groups.keys()].filter(b => b !== mainBranch)

  if (forkBranches.length === 0) {
    return layoutSingleColumn(mainNodes, options)
  }

  const forkPoints = collectForkPoints(mainNodes, forkBranches, groups)
  if (forkPoints.length === 0) {
    return layoutSingleColumn(mainNodes, options)
  }

  // 布局
  const allocator = new ColumnAllocator()
  const segments: BranchSegment[] = []
  const forks: ForkConn[] = []
  let maxY = 300
  allocator.reserve(MAIN_X, 0, 0)

  // 布局主链段，返回节点 id → y；lineTop 指定链线起始 y（竖直延续时用分叉点 y）
  const layoutMainSegment = (
    start: number, end: number, col: number,
    baseY?: number, lineTop?: number,
  ): Map<string, number> => {
    const ns = mainNodes.slice(start, end)
    let items = toLayoutItems(ns, layoutSeq(ns, options))
    if (baseY !== undefined && items.length > 0) {
      const shift = baseY - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
    }
    if (items.length > 0) {
      const top = lineTop ?? items[0].y
      segments.push({ x: col, items, topY: top, bottomY: items[items.length - 1].y })
      allocator.reserve(col, top, items[items.length - 1].y)
      maxY = Math.max(maxY, items[items.length - 1].y)
    }
    return new Map(items.map(it => [it.nodeId, it.y]))
  }

  // 布局 fork 分支（先算 y 再分配列）
  const layoutForkBranch = (ns: BranchNode[], anchorCol: number, anchorY: number): void => {
    let items = toLayoutItems(ns, layoutSeq(ns, options))
    const firstY = anchorY + BRANCH_Y_DROP
    if (items.length > 0) {
      const shift = firstY - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
    }
    const y1 = items[0]?.y ?? firstY
    const y2 = items.length > 0 ? items[items.length - 1].y : firstY
    const cand = allocator.findFreeNear(anchorCol, y1, y2)
    allocator.reserve(cand, y1, y2)
    segments.push({ x: cand, items, topY: y1, bottomY: y2 })
    maxY = Math.max(maxY, y2)
    const from = { x: anchorCol, y: anchorY }
    const to = { x: cand, y: y1 }
    forks.push({ from, to, d: forkPath(from, to) })
  }

  // 递归：主链 [startIdx..] 在 col 列，处理 forkPoints[fpIdx..]
  const walk = (startIdx: number, fpIdx: number, col: number, baseY?: number, lineTop?: number) => {
    if (fpIdx >= forkPoints.length) {
      layoutMainSegment(startIdx, mainNodes.length, col, baseY, lineTop)
      return
    }
    const p = forkPoints[fpIdx]
    const anchorIdx = mainNodes.findIndex(n => n.id === p.anchor.id)
    if (anchorIdx < startIdx) {
      layoutMainSegment(startIdx, mainNodes.length, col, baseY, lineTop)
      return
    }

    const yOf = layoutMainSegment(startIdx, anchorIdx + 1, col, baseY, lineTop)
    const anchorY = yOf.get(p.anchor.id) ?? 8
    maxY = Math.max(maxY, anchorY)

    const forkList = [...p.forks].sort((a, c) => a[0].seq - c[0].seq)
    const hasTail = anchorIdx + 1 < mainNodes.length
    const N = forkList.length + (hasTail ? 1 : 0)
    if (N === 0) return

    // 主链继续段列：奇数 → 竖直（col）；偶数 → 最右一列
    let tailCol = col
    if (N % 2 === 0) {
      tailCol = allocator.findFreeSide(col, anchorY + BRANCH_Y_DROP, anchorY + BRANCH_Y_DROP, 1)
    }

    // fork 分支：对称分配不冲突列
    for (const ns of forkList) {
      layoutForkBranch(ns, col, anchorY)
    }

    // 主链继续段：偏移时画弧线，竖直时链线延续
    if (hasTail) {
      if (tailCol === col) {
        walk(anchorIdx + 1, fpIdx + 1, tailCol, anchorY + BRANCH_Y_DROP, anchorY)
      } else {
        const from = { x: col, y: anchorY }
        const to = { x: tailCol, y: anchorY + BRANCH_Y_DROP }
        forks.push({ from, to, d: forkPath(from, to) })
        walk(anchorIdx + 1, fpIdx + 1, tailCol, anchorY + BRANCH_Y_DROP, undefined)
      }
    }
  }

  walk(0, 0, MAIN_X)

  const { min, max } = allocator.bounds()
  return { segments, forks, width: max - min + 140, height: maxY + 20 }
}

// 纯函数：无分叉时的单列布局。
export function layoutSingleColumn(mainNodes: BranchNode[], options?: LayoutOptions): BranchLayoutResult {
  let items = toLayoutItems(mainNodes, layoutSeq(mainNodes, options))
  if (items.length === 0) return { segments: [], forks: [], width: 300, height: 300 }
  return {
    segments: [{
      x: MAIN_X,
      items,
      topY: items[0].y,
      bottomY: items[items.length - 1].y,
    }],
    forks: [],
    width: MAIN_X * 2 + 80,
    height: Math.max(300, items[items.length - 1].y + 20),
  }
}
