// 分支布局：倒 Y（二叉树）分叉，全部纵向。
//
// 结构：
//   共同祖先段（A1→A2→A3）画在中间列 x=MAIN_X
//   分叉后每条分支偏移到自己的列：
//     主链继续（A4→A5...）  x = MAIN_X + BRANCH_X_OFFSET（右列）
//     fork 分支（A4'→...）  x = MAIN_X - BRANCH_X_OFFSET（左列）
//   A3 用斜线分别连到两条分支的第一个节点
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

  // 3. 找 fork 锚点（第一个 fork 分支根的 parent）
  //    只支持一级 fork：锚点在主链上
  let anchor: BranchNode | undefined
  let forkBranch = ''
  for (const [b, ns] of groups) {
    if (b === mainBranch) continue
    const root = ns.find(n => n.copiedFrom) || ns[0]
    const a = mainNodes.find(n => n.id === root.parentId)
    if (a) { anchor = a; forkBranch = b; break }
  }

  // 4. 分割主链：锚点之前（含锚点）= 祖先段；锚点之后 = 主链继续段
  let ancestorNodes: BranchNode[] = mainNodes
  let mainTailNodes: BranchNode[] = []
  if (anchor) {
    const anchorIdx = mainNodes.findIndex(n => n.id === anchor!.id)
    if (anchorIdx >= 0) {
      ancestorNodes = mainNodes.slice(0, anchorIdx + 1)
      mainTailNodes = mainNodes.slice(anchorIdx + 1)
    }
  }

  // 5. 布局祖先段（中间列）
  const layoutSeq = (ns: BranchNode[]) => {
    const lns: LayoutNode[] = ns.map(n => ({
      id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
    }))
    return computeLayout(lns, options)
  }

  const ancestorLaid = layoutSeq(ancestorNodes)
  const ancestorItems: LayoutItem[] = ancestorLaid.map((it, i) => ({
    ...it,
    status: ancestorNodes[i].status,
    nodeId: ancestorNodes[i].id,
    parentId: ancestorNodes[i].parentId,
  }))
  const yOf = new Map(ancestorItems.map(it => [it.nodeId, it.y]))

  const segments: BranchSegment[] = []
  const forks: ForkConn[] = []
  let maxY = 0

  segments.push({
    x: MAIN_X,
    items: ancestorItems,
    topY: ancestorItems.length > 0 ? ancestorItems[0].y : 8,
    bottomY: ancestorItems.length > 0 ? ancestorItems[ancestorItems.length - 1].y : 8,
  })
  if (ancestorItems.length > 0) maxY = Math.max(maxY, ancestorItems[ancestorItems.length - 1].y)

  // 6. 主链继续段（右列）：从锚点 y 开始
  if (mainTailNodes.length > 0) {
    const anchorY = anchor ? (yOf.get(anchor.id) ?? 8) : 8
    const laid = layoutSeq(mainTailNodes)
    let items: LayoutItem[] = laid.map((it, i) => ({
      ...it,
      status: mainTailNodes[i].status,
      nodeId: mainTailNodes[i].id,
      parentId: mainTailNodes[i].parentId,
    }))
    // 平移：使第一个节点从锚点 y 下方 BRANCH_Y_DROP 开始（弧线从左上到右下）
    if (items.length > 0) {
      const shift = anchorY + BRANCH_Y_DROP - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
    }
    const x = MAIN_X + BRANCH_X_OFFSET
    segments.push({
      x,
      items,
      topY: items.length > 0 ? items[0].y : anchorY,
      bottomY: items.length > 0 ? items[items.length - 1].y : anchorY,
    })
    if (anchor) {
      const to = { x, y: items[0]?.y ?? anchorY + BRANCH_Y_DROP }
      forks.push({ from: { x: MAIN_X, y: anchorY }, to, d: forkPath({ x: MAIN_X, y: anchorY }, to) })
    }
    if (items.length > 0) maxY = Math.max(maxY, items[items.length - 1].y)
  }

  // 7. fork 分支段（左列）：从锚点 y 开始
  if (anchor && forkBranch) {
    const forkNodes = [...(groups.get(forkBranch)!)].sort((a, c) => a.seq - c.seq)
    // 分支根是虚节点（fork 出口），保留；不含锚点
    const anchorY = yOf.get(anchor.id) ?? 8
    const laid = layoutSeq(forkNodes)
    let items: LayoutItem[] = laid.map((it, i) => ({
      ...it,
      status: forkNodes[i].status,
      nodeId: forkNodes[i].id,
      parentId: forkNodes[i].parentId,
    }))
    if (items.length > 0) {
      const shift = anchorY + BRANCH_Y_DROP - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
    }
    const x = MAIN_X - BRANCH_X_OFFSET
    segments.push({
      x,
      items,
      topY: items.length > 0 ? items[0].y : anchorY,
      bottomY: items.length > 0 ? items[items.length - 1].y : anchorY,
    })
    const to = { x, y: items[0]?.y ?? anchorY + BRANCH_Y_DROP }
    forks.push({ from: { x: MAIN_X, y: anchorY }, to, d: forkPath({ x: MAIN_X, y: anchorY }, to) })
    if (items.length > 0) maxY = Math.max(maxY, items[items.length - 1].y)
  }

  return {
    segments,
    forks,
    width: MAIN_X + BRANCH_X_OFFSET * 2 + 80,
    height: Math.max(300, maxY + 20),
  }
}
