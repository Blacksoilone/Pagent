// 手串式链布局算法。
//
// 规则（设计讨论）：
// - 绝大多数是普通节点，间距必须极小：相邻普通点中心距 = 普通点直径 d
// - 重要节点之间有最小距离：中心距 ≥ 6 × 重要点直径 D
// - 重要节点之间的普通节点均匀填充；放不下时扩展重要节点间距
//
// 输出：每个节点的 y 中心坐标（沿链方向），由调用方决定水平布局。

export interface LayoutNode {
  id: string
  important: boolean
  ghost: boolean
}

export interface LayoutResult {
  node: LayoutNode
  y: number
}

export const DIAM_SMALL = 8    // 普通节点直径 d
export const DIAM_BIG = 16     // 重要节点直径 D
export const GAP_IMPORTANT = 6 * DIAM_BIG // 重要节点最小中心距 6D

/**
 * 计算链上节点的 y 坐标。
 *
 * 分段算法：
 * 1. 找出所有重要节点索引，链被切成 [首→imp1, imp1→imp2, ..., impN→尾]
 * 2. 每段内普通节点均匀填充：
 *    - 段首尾都是重要节点时，段长 = max(6D, (k+1)*d)，k = 段内普通节点数
 *    - 段首/尾是链端时，普通节点按最小间距 d 排
 */
export function computeLayout(nodes: LayoutNode[]): LayoutResult[] {
  if (nodes.length === 0) return []

  const result: LayoutResult[] = []

  // 重要节点索引（不含链首链尾的虚节点处理，虚节点按普通处理但更小）
  const impIdx: number[] = []
  nodes.forEach((n, i) => { if (n.important) impIdx.push(i) })

  // 段边界：[start, end]，start/end 是节点索引，段内普通节点均匀填充
  // 段 = [prevImp, nextImp]（首尾可能是链端 -1 / len）
  const segStarts: number[] = [-1, ...impIdx]
  const segEnds: number[] = [...impIdx, nodes.length]

  let y = DIAM_SMALL / 2

  for (let s = 0; s < segStarts.length; s++) {
    const start = segStarts[s] // 上一个重要节点索引（-1 = 链首）
    const end = segEnds[s]     // 下一个重要节点索引（len = 链尾）

    // 段内普通节点：start+1 .. end-1
    const normals: LayoutNode[] = []
    for (let i = start + 1; i < end; i++) {
      normals.push(nodes[i])
    }
    // 段尾的重要节点（如果有）
    const tailImp = end < nodes.length ? nodes[end] : null

    const k = normals.length
    const segLen = k === 0 ? 0 : (k + 1) * DIAM_SMALL

    // 段长 = max(最小要求, 6D if 首尾都有重要节点)
    let effectiveLen = segLen
    if (start >= 0 && end < nodes.length) {
      effectiveLen = Math.max(GAP_IMPORTANT, segLen)
    } else if (start >= 0 || end < nodes.length) {
      // 一端是重要节点：普通节点最小间距即可（重要节点前/后直接贴）
      effectiveLen = segLen
    }

    // 放置普通节点（均匀）
    for (let i = 0; i < k; i++) {
      if (k === 0) continue
      const ny = y + effectiveLen * (i + 1) / (k + 1)
      result.push({ node: normals[i], y: ny })
    }

    // 放置段尾重要节点
    if (tailImp) {
      y += effectiveLen
      result.push({ node: tailImp, y })
    } else {
      y += effectiveLen
    }
  }

  return result
}
