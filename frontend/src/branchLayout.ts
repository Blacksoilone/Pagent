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
  const forkMap = new Map<string, BranchNode[][]>()
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
    return computeBranchLayout(mainNodes, options)
  }

  // 4. 列区间管理：每列记录已占用竖直区间，新链优先复用不冲突的列
  const colRanges = new Map<number, Array<[number, number]>>()
  const colFree = (col: number, y1: number, y2: number): boolean => {
    const rs = colRanges.get(col)
    if (!rs) return true
    return !rs.some(([a, b]) => y1 <= b && y2 >= a)
  }
  const addRange = (col: number, y1: number, y2: number) => {
    if (!colRanges.has(col)) colRanges.set(col, [])
    colRanges.get(col)!.push([y1, y2])
  }

  const layoutSeq = (ns: BranchNode[]) => {
    const lns: LayoutNode[] = ns.map(n => ({
      id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
    }))
    return computeLayout(lns, options)
  }

  const segments: BranchSegment[] = []
  const forks: ForkConn[] = []
  let maxY = 300

  // 布局主链段，返回节点 id → y；lineTop 指定链线起始 y（竖直延续时用分叉点 y）
  const layoutMainSegment = (
    start: number, end: number, col: number,
    baseY?: number, lineTop?: number,
  ): Map<string, number> => {
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
      const top = lineTop ?? items[0].y
      segments.push({
        x: col,
        items,
        topY: top,
        bottomY: items[items.length - 1].y,
      })
      addRange(col, top, items[items.length - 1].y)
      maxY = Math.max(maxY, items[items.length - 1].y)
    }
    return new Map(items.map(it => [it.nodeId, it.y]))
  }

  // 布局 fork 分支（先算 y 再分配列），返回 [列, 首珠 y]
  const layoutForkBranch = (ns: BranchNode[], anchorCol: number, anchorY: number): [number, number] => {
    const laid = layoutSeq(ns)
    let items: LayoutItem[] = laid.map((it, i) => ({
      ...it,
      status: ns[i].status,
      nodeId: ns[i].id,
      parentId: ns[i].parentId,
    }))
    const firstY = anchorY + BRANCH_Y_DROP
    if (items.length > 0) {
      const shift = firstY - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
    }
    const y1 = items[0]?.y ?? firstY
    const y2 = items.length > 0 ? items[items.length - 1].y : firstY
    // 从分叉点列向外对称找第一个竖直区间不冲突的列（-1,+1,-2,+2...）
    let step = 1
    let placed = false
    while (!placed) {
      for (const side of [-1, 1]) {
        const cand = anchorCol + side * step * BRANCH_X_OFFSET
        if (colFree(cand, y1, y2)) {
          placed = true
        addRange(cand, y1, y2)
        segments.push({
          x: cand,
          items,
          topY: y1,
          bottomY: y2,
        })
        maxY = Math.max(maxY, y2)
        forks.push({
          from: { x: anchorCol, y: anchorY },
          to: { x: cand, y: y1 },
          d: forkPath({ x: anchorCol, y: anchorY }, { x: cand, y: y1 }),
        })
          return [cand, y1]
        }
      }
      step++
    }
    return [anchorCol, y1]
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

    // 祖先段（含分叉点）
    const yOf = layoutMainSegment(startIdx, anchorIdx + 1, col, baseY, lineTop)
    const anchorY = yOf.get(p.anchor.id) ?? 8
    maxY = Math.max(maxY, anchorY)

    const forkList = [...p.forks].sort((a, c) => a[0].seq - c[0].seq)
    const hasTail = anchorIdx + 1 < mainNodes.length
    const N = forkList.length + (hasTail ? 1 : 0)
    if (N === 0) return

    // 主链继续段列：奇数 → 竖直（col）；偶数 → 最右一列（+35）
    let tailCol = col
    if (N % 2 === 0) {
      tailCol = col + BRANCH_X_OFFSET
      // 若被占用则继续外移
      while (!colFree(tailCol, anchorY + BRANCH_Y_DROP, anchorY + BRANCH_Y_DROP)) {
        tailCol += BRANCH_X_OFFSET
      }
    }

    // fork 分支：左右交替分配不冲突列
    let side = -1
    for (let i = 0; i < forkList.length; i++) {
      layoutForkBranch(forkList[i], col, anchorY)
      // 交替方向：单 fork 时左；多 fork 时左右交替
      side = -side
    }

    // 主链继续段：偏移时画弧线，竖直时链线延续
    if (hasTail) {
      if (tailCol === col) {
        walk(anchorIdx + 1, fpIdx + 1, tailCol, anchorY + BRANCH_Y_DROP, anchorY)
      } else {
        const to = { x: tailCol, y: anchorY + BRANCH_Y_DROP }
        forks.push({
          from: { x: col, y: anchorY },
          to,
          d: forkPath({ x: col, y: anchorY }, to),
        })
        walk(anchorIdx + 1, fpIdx + 1, tailCol, anchorY + BRANCH_Y_DROP, undefined)
      }
    }
  }

  walk(0, 0, MAIN_X)

  const cols = [...colRanges.keys()]
  const minCol = cols.length > 0 ? Math.min(...cols) : MAIN_X
  const maxCol = cols.length > 0 ? Math.max(...cols) : MAIN_X
  const width = maxCol - minCol + 140

  return { segments, forks, width, height: maxY + 20 }
}
