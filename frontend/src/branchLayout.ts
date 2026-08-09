// 分支布局：倒 Y（二叉树）分叉。
//
// 结构：
//   共同祖先段（A1→A2→A3）画在中间 x=100
//   分叉后每条分支向两侧偏移：
//     原链继续 A4→A5...   x = 100 + offset（右）
//     fork 分支 A4'→A5'... x = 100 - offset（左）
//   A3 用斜线连到每条分支的第一个节点
//
// 每条分支从 fork 点位置开始向下（虚节点在各自底部）。

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

export interface ForkConn {
  from: { x: number; y: number }  // 分叉点位置
  to: { x: number; y: number }    // 分支第一个节点位置
}

export interface BranchLayoutItem {
  branch: string
  x: number
  items: LayoutItem[]
  topY: number
  bottomY: number
}

export const BRANCH_X_OFFSET = 80 // 分叉水平偏移
export const MAIN_X = 100         // 共同祖先段 x

export function computeBranchLayout(
  nodes: BranchNode[],
  options?: LayoutOptions,
): { branches: BranchLayoutItem[]; forks: ForkConn[]; width: number; height: number } {
  if (nodes.length === 0) return { branches: [], forks: [], width: 300, height: 300 }

  // 1. 按 branch 分组
  const groups = new Map<string, BranchNode[]>()
  for (const n of nodes) {
    if (!groups.has(n.branch)) groups.set(n.branch, [])
    groups.get(n.branch)!.push(n)
  }

  // 2. 找主链（未被 fork 出的）与其他分支
  //    主链 = 根节点没有 copiedFrom 的分支
  let mainBranch = ''
  for (const [b, ns] of groups) {
    if (!ns.some(n => n.copiedFrom)) { mainBranch = b; break }
  }
  if (!mainBranch) mainBranch = [...groups.keys()][0]

  const mainNodes = [...(groups.get(mainBranch)!)].sort((a, c) => a.seq - c.seq)
  const otherBranches = [...groups.keys()].filter(b => b !== mainBranch)

  // 3. 主链布局（完整链）
  const layoutSeq = (ns: BranchNode[]) => {
    const lns: LayoutNode[] = ns.map(n => ({
      id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
    }))
    return computeLayout(lns, options)
  }
  const mainLaid = layoutSeq(mainNodes)
  const mainItems: LayoutItem[] = mainLaid.map((it, i) => ({
    ...it,
    status: mainNodes[i].status,
    nodeId: mainNodes[i].id,
    parentId: mainNodes[i].parentId,
  }))
  const mainYOf = new Map(mainItems.map(it => [it.nodeId, it.y]))

  const branches: BranchLayoutItem[] = []
  const forks: ForkConn[] = []
  let maxY = 0

  branches.push({
    branch: mainBranch, x: MAIN_X, items: mainItems,
    topY: mainItems.length > 0 ? mainItems[0].y : 8,
    bottomY: mainItems.length > 0 ? mainItems[mainItems.length - 1].y : 8,
  })
  if (mainItems.length > 0) maxY = Math.max(maxY, mainItems[mainItems.length - 1].y)

  // 4. 其他分支：从 fork 锚点位置分叉
  const mainById = new Map(mainNodes.map(n => [n.id, n]))

  for (const b of otherBranches) {
    const ns = [...(groups.get(b)!)].sort((a, c) => a.seq - c.seq)
    // 找 fork 锚点：分支根（copiedFrom 非空）的 parent 在主链上的位置
    const root = ns.find(n => n.copiedFrom) || ns[0]
    const anchor = mainById.get(root.parentId)
    if (!anchor) {
      // 锚点不在主链（多级 fork 暂不支持），跳过或放右侧
      continue
    }
    const anchorY = mainYOf.get(anchor.id) ?? 8

    // 分支布局：从锚点之后开始（不含锚点）
    const branchNodes = ns.filter(n => n.id !== anchor.id)
    const branchLaid = layoutSeq(branchNodes)
    let branchItems: LayoutItem[] = branchLaid.map((it, i) => ({
      ...it,
      status: branchNodes[i].status,
      nodeId: branchNodes[i].id,
      parentId: branchNodes[i].parentId,
    }))

    // 平移：使分支第一个节点从锚点位置开始（时间连续）
    if (branchItems.length > 0) {
      const shift = anchorY - branchItems[0].y
      branchItems = branchItems.map(it => ({ ...it, y: it.y + shift }))
    }

    // 分叉方向：交替左右（第一个 fork 左，第二个右……但无主次，这里简单左）
    const x = MAIN_X - BRANCH_X_OFFSET

    branches.push({
      branch: b, x, items: branchItems,
      topY: branchItems.length > 0 ? branchItems[0].y : anchorY,
      bottomY: branchItems.length > 0 ? branchItems[branchItems.length - 1].y : anchorY,
    })

    // 分叉连接：锚点 → 分支第一个节点
    if (branchItems.length > 0) {
      forks.push({
        from: { x: MAIN_X, y: anchorY },
        to: { x, y: branchItems[0].y },
      })
    }
    if (branchItems.length > 0) {
      maxY = Math.max(maxY, branchItems[branchItems.length - 1].y)
    }
  }

  return {
    branches,
    forks,
    width: MAIN_X + BRANCH_X_OFFSET + 80,
    height: Math.max(300, maxY + 20),
  }
}
