// 手串式链布局算法。
//
// 规则（设计讨论）：
// - 绝大多数是普通节点，间距必须极小：相邻普通点中心距 = 普通点直径 d
// - 重要节点之间有最小距离：中心距 ≥ 6 × 重要点直径 D
// - 相邻节点中心距 = 两半径之和（相切不重叠）：小-小 = d，小-大 = d/2 + D/2
// - 重要节点之间的普通节点在满足相切约束下尽量均匀填充
//
// 输出：每个节点的 y 中心坐标（沿链方向）。

export interface LayoutNode {
  id: string
  important: boolean
  ghost: boolean
}

export interface LayoutResult {
  node: LayoutNode
  y: number
}

export const DIAM_SMALL = 8     // 普通节点直径 d
export const DIAM_BIG = 16      // 重要节点直径 D
export const GAP_IMPORTANT = 6 * DIAM_BIG // 重要节点最小中心距 6D

const R_SMALL = DIAM_SMALL / 2  // 4
const R_BIG = DIAM_BIG / 2      // 8

/**
 * 计算链上节点的 y 坐标。
 *
 * 分段算法：链被重要节点切成若干区间。
 * 每个区间（重要节点 A → 重要节点 B，中间 k 个普通节点）：
 * - 基础段 = A→n1 相切(R_S+R_B) + n1→n2... 相切(d) + nk→B 相切(R_S+R_B)
 * - 实际段长 = max(6D, 基础段)
 * - 富余空间均匀分配到各段（保持相切约束下的均匀性）
 *
 * 链首/链尾的普通节点段按最小间距 d 排列（没有重要节点约束）。
 */
export function computeLayout(nodes: LayoutNode[]): LayoutResult[] {
  if (nodes.length === 0) return []

  const result: LayoutResult[] = []
  const impIdx: number[] = []
  nodes.forEach((n, i) => { if (n.important) impIdx.push(i) })

  if (impIdx.length === 0) {
    let y = R_SMALL
    nodes.forEach((n) => {
      result.push({ node: n, y })
      y += DIAM_SMALL
    })
    return result
  }

  // 光标：当前已放置到的 y
  let y = R_SMALL

  // 链首 → 第一个重要节点（含第一个重要节点本身）
  {
    const firstImp = impIdx[0]
    for (let i = 0; i <= firstImp; i++) {
      result.push({ node: nodes[i], y })
      if (i < firstImp) {
        // 普通节点：下一节点若是重要节点则相切 R_S+R_B，否则 d
        const next = nodes[i + 1]
        y += next.important ? R_SMALL + R_BIG : DIAM_SMALL
      }
    }
    // 第一个重要节点之后的间距先不确定，由下一段决定
  }

  // 重要节点之间的区间
  for (let s = 0; s < impIdx.length - 1; s++) {
    const a = impIdx[s]       // 重要节点 A 的索引
    const b = impIdx[s + 1]   // 重要节点 B 的索引
    const k = b - a - 1       // 中间普通节点数
    const startY = result[a].y // A 的中心

    // 基础段：A→n1 (R_S+R_B) + 中间 (k-1)*d + nk→B (R_S+R_B)
    const baseTotal = k === 0
      ? 2 * R_BIG  // 两个重要节点直接相邻（理论上 6D 会覆盖）
      : (R_SMALL + R_BIG) + (k - 1) * DIAM_SMALL + (R_SMALL + R_BIG)
    const segLen = Math.max(GAP_IMPORTANT, baseTotal)
    const extra = segLen - baseTotal

    // 各段长度：首尾相切段 + 中间段，均匀分配富余
    const segCount = k + 1
    const segs: number[] = []
    for (let i = 0; i < segCount; i++) {
      const base = (i === 0 || i === segCount - 1) ? R_SMALL + R_BIG : DIAM_SMALL
      segs.push(base + extra / segCount)
    }

    // 放置普通节点
    let pos = startY
    for (let i = 0; i < k; i++) {
      pos += segs[i]
      result.push({ node: nodes[a + 1 + i], y: pos })
    }
    // 放置重要节点 B
    pos += segs[k]
    result.push({ node: nodes[b], y: pos })
  }

  // 最后一个重要节点 → 链尾
  {
    const lastImp = impIdx[impIdx.length - 1]
    const lastY = result[lastImp].y
    let pos = lastY
    for (let i = lastImp + 1; i < nodes.length; i++) {
      pos += DIAM_SMALL
      result.push({ node: nodes[i], y: pos })
    }
  }

  return result
}
