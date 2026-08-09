// 分支布局：倒 Y 分叉，全部纵向，支持主链多个分叉点。
//
// 结构：
//   主链从 x=MAIN_X 开始；每个分叉点把主链分成"祖先段 + 继续段"
//   分叉点的子方向 = fork 分支们 + 主链继续段，按奇偶规则分配列：
//     子方向数 N 为奇数（如 3）：主链继续段竖直对齐（保持分叉点所在列）
//     子方向数 N 为偶数（如 2）：主链继续段在最右一列，fork 分支在左
//   fork 分支列从分叉点列向外扩展，跳过已被占用的列
//   分叉点用三次贝塞尔弧线连到每个 fork 分支第一个节点（左上 → 右下）
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

export const BRANCH_X_OFFSET = 35
export const BRANCH_Y_DROP = 36
export const MAIN_X = 100

// 分叉弧线：三次贝塞尔，先垂直向下再弯向分支（左上 → 右下）。
function forkPath(from: { x: number; y: number }, to: { x: number; y: number }): string {
  const dy = to.y - from.y
  const k = Math.max(20, dy * 0.6)
  return `M ${from.x},${from.y} C ${from.x},${from.y + k} ${to.x},${to.y - k} ${to.x},${to.y}`
}

export function computeBranchLayout(
  nodes: BranchNode[],
  options?: LayoutOptions,
): { segments: BranchSegment[]; forks: ForkConn[]; width: number; height: number } {
  if (nodes.length === 0) return { segments: [], forks: [], width: 300, height: 300 }

  // 1. 按 branch 分组
  const groups = new Map<string, BranchNode[]>()
  for (const n of nodes) {
    if (!groups.has(n.branch)) groups.set(n.branch, [])
    groups.get(n.branch)!.push(n)
  }

  // 2. 找主链（根节点没有 copiedFrom 的分支）与其他分支
  let mainBranch = ''
  for (const [b, ns] of groups) {
    if (!ns.some(n => n.copiedFrom)) { mainBranch = b; break }
  }
  if (!mainBranch) mainBranch = [...groups.keys()][0]

  const mainNodes = [...(groups.get(mainBranch)!)].sort((a, c) => a.seq - c.seq)
  const forkBranches = [...groups.keys()].filter(b => b !== mainBranch)

  if (forkBranches.length === 0) {
    // 无分叉：单列线性布局
    const laid = computeLayout(mainNodes.map(n => ({
      id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
    })), options)
    const items: LayoutItem[] = laid.map((it, i) => ({
      ...it,
      status: mainNodes[i].status,
      nodeId: mainNodes[i].id,
      parentId: mainNodes[i].parentId,
    }))
    return {
      segments: [{
        x: MAIN_X,
        items,
        topY: items.length > 0 ? items[0].y : 8,
        bottomY: items.length > 0 ? items[items.length - 1].y : 8,
      }],
      forks: [],
      width: MAIN_X * 2 + 80,
      height: Math.max(300, (items[items.length - 1]?.y ?? 8) + 20),
    }
  }

  // 3. 收集分叉点：fork 分支根（copiedFrom 非空）的 parent 在主链上的节点
  //    同一锚点多次 fork → 多个 fork 分支共享同一分叉点
  const forkMap = new Map<string, BranchNode[][]>() // anchorId → fork 分支列表
  for (const b of forkBranches) {
    const ns = [...(groups.get(b)!)].sort((a, c) => a.seq - c.seq)
    const root = ns.find(n => n.copiedFrom) || ns[0]
    const a = mainNodes.find(n => n.id === root.parentId)
    if (!a) continue
    if (!forkMap.has(a.id)) forkMap.set(a.id, [])
    forkMap.get(a.id)!.push(ns)
  }
  const forkPoints = [...forkMap.entries()]
    .map(([anchorId, forks]) => ({
      anchor: mainNodes.find(n => n.id === anchorId)!,
      forks,
    }))
    .filter(p => p.anchor)
    .sort((a, c) => a.anchor.seq - c.anchor.seq)

  if (forkPoints.length === 0) {
    // 无法确定分叉点（异常数据）：退化为主链单列
    return computeBranchLayout(mainNodes, options)
  }

  // 4. 布局
  const layoutSeq = (ns: BranchNode[]) => {
    const lns: LayoutNode[] = ns.map(n => ({
      id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
    }))
    return computeLayout(lns, options)
  }

  const segments: BranchSegment[] = []
  const forks: ForkConn[] = []
  const usedCols = new Set<number>([MAIN_X])
  let maxY = 300

  const layoutMainSegment = (start: number, end: number, col: number, baseY?: number): Map<string, number> => {
    const ns = mainNodes.slice(start, end)
    const laid = layoutSeq(ns)
    let items: LayoutItem[] = laid.map((it, i) => ({
      ...it,
      status: ns[i].status,
      nodeId: ns[i].id,
      parentId: ns[i].parentId,
    }))
    if (baseY !== undefined && items.length > 0) {
      const shift = baseY - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
    }
    if (items.length > 0) {
      segments.push({
        x: col,
        items,
        topY: items[0].y,
        bottomY: items[items.length - 1].y,
      })
      maxY = Math.max(maxY, items[items.length - 1].y)
    }
    return new Map(items.map(it => [it.nodeId, it.y]))
  }

  const layoutForkBranch = (ns: BranchNode[], anchorCol: number, anchorY: number, col: number) => {
    const laid = layoutSeq(ns)
    let items: LayoutItem[] = laid.map((it, i) => ({
      ...it,
      status: ns[i].status,
      nodeId: ns[i].id,
      parentId: ns[i].parentId,
    }))
    if (items.length > 0) {
      const shift = anchorY + BRANCH_Y_DROP - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
      segments.push({
        x: col,
        items,
        topY: items[0].y,
        bottomY: items[items.length - 1].y,
      })
      maxY = Math.max(maxY, items[items.length - 1].y)
    }
    const to = { x: col, y: items[0]?.y ?? anchorY + BRANCH_Y_DROP }
    forks.push({ from: { x: anchorCol, y: anchorY }, to, d: forkPath({ x: anchorCol, y: anchorY }, to) })
  }

  const allocCol = (base: number, side: number): number => {
    let step = 1
    while (usedCols.has(base + side * step * BRANCH_X_OFFSET)) step++
    const col = base + side * step * BRANCH_X_OFFSET
    usedCols.add(col)
    return col
  }

  const walk = (startIdx: number, fpIdx: number, col: number, baseY?: number) => {
    if (fpIdx >= forkPoints.length) {
      layoutMainSegment(startIdx, mainNodes.length, col, baseY)
      return
    }
    const p = forkPoints[fpIdx]
    const anchorIdx = mainNodes.findIndex(n => n.id === p.anchor.id)
    if (anchorIdx < startIdx) {
      layoutMainSegment(startIdx, mainNodes.length, col, baseY)
      return
    }

    const yOf = layoutMainSegment(startIdx, anchorIdx + 1, col, baseY)
    const anchorY = yOf.get(p.anchor.id) ?? 8
    maxY = Math.max(maxY, anchorY)

    const forkList = [...p.forks].sort((a, c) => a[0].seq - c[0].seq)
    const hasTail = anchorIdx + 1 < mainNodes.length
    const N = forkList.length + (hasTail ? 1 : 0)

    if (N === 0) return

    let tailCol = col
    if (N % 2 === 0) {
      tailCol = allocCol(col, 1)
    }

    const forkCols: number[] = []
    let side = -1
    for (let i = 0; i < forkList.length; i++) {
      if (N % 2 === 0) {
        forkCols.push(allocCol(col, -1))
      } else {
        forkCols.push(allocCol(col, side))
        side = -side
      }
    }

    forkList.forEach((ns, i) => layoutForkBranch(ns, col, anchorY, forkCols[i]))

    walk(anchorIdx + 1, fpIdx + 1, tailCol, anchorY + BRANCH_Y_DROP)
  }

  walk(0, 0, MAIN_X)

  const cols = [...usedCols]
  const minCol = Math.min(...cols)
  const maxCol = Math.max(...cols)
  const width = maxCol - minCol + 140

  return { segments, forks, width, height: maxY + 20 }
}
