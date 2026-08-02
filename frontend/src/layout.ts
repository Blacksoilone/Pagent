// 手串式链布局算法。
//
// 规则（设计讨论）：
// - 绝大多数是普通节点，间距必须极小：相邻普通点中心距 = 普通点直径 d
// - 重要节点/虚节点之间有最小距离：中心距 ≥ 6 × 重要点直径 D
// - 相邻节点中心距 = 两半径之和（相切不重叠）：小-小 = d，小-大 = d/2 + D/2
// - 重要节点之间的普通节点在满足相切约束下尽量均匀填充
// - 虚节点（ghost）沿用重要节点的距离规则与尺寸（实化前是可操作对象）
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
export const DIAM_BIG = 16      // 重要节点/虚节点直径 D
export const GAP_IMPORTANT = 6 * DIAM_BIG // 重要节点最小中心距 6D

const R_SMALL = DIAM_SMALL / 2  // 4
const R_BIG = DIAM_BIG / 2      // 8

// isBig 判断节点是否按重要节点规则处理（重要节点与虚节点都是）
function isBig(n: LayoutNode): boolean {
  return n.important || n.ghost
}

/**
 * 计算链上节点的 y 坐标。
 *
 * 分段算法：链被"大节点"（重要/虚节点）切成若干区间。
 * 每个区间（大节点 A → 大节点 B，中间 k 个普通节点）：
 * - 基础段 = A→n1 相切(R_S+R_B) + n1→n2... 相切(d) + nk→B 相切(R_S+R_B)
 * - 实际段长 = max(6D, 基础段)
 * - 富余空间均匀分配到各段（保持相切约束下的均匀性）
 *
 * 链首/链尾的普通节点段按最小间距排列（无大节点约束的一端相切处理）。
 */
export function computeLayout(nodes: LayoutNode[]): LayoutResult[] {
  if (nodes.length === 0) return []

  const result: LayoutResult[] = []
  const bigIdx: number[] = []
  nodes.forEach((n, i) => { if (isBig(n)) bigIdx.push(i) })

  if (bigIdx.length === 0) {
    let y = R_SMALL
    nodes.forEach((n) => {
      result.push({ node: n, y })
      y += DIAM_SMALL
    })
    return result
  }

  // 链首起点：若第一个节点是大节点，从 R_BIG 开始（否则 R_SMALL）
  let y = isBig(nodes[0]) ? R_BIG : R_SMALL

  // 链首 → 第一个大节点（含第一个大节点）
  {
    const first = bigIdx[0]
    for (let i = 0; i <= first; i++) {
      result.push({ node: nodes[i], y })
      if (i < first) {
        // 当前是普通节点，看下一个是否大节点 → 相切 R_S+R_B，否则 d
        const next = nodes[i + 1]
        y += isBig(next) ? R_SMALL + R_BIG : DIAM_SMALL
      }
    }
  }

  // 大节点之间的区间
  for (let s = 0; s < bigIdx.length - 1; s++) {
    const a = bigIdx[s]
    const b = bigIdx[s + 1]
    const k = b - a - 1
    const startY = result[a].y

    const baseTotal = k === 0
      ? 2 * R_BIG
      : (R_SMALL + R_BIG) + (k - 1) * DIAM_SMALL + (R_SMALL + R_BIG)
    const segLen = Math.max(GAP_IMPORTANT, baseTotal)
    const extra = segLen - baseTotal

    const segCount = k + 1
    const segs: number[] = []
    for (let i = 0; i < segCount; i++) {
      let base: number
      if (k === 0) {
        // 两个大节点直接相邻：相切 = 2*R_BIG
        base = 2 * R_BIG
      } else {
        base = (i === 0 || i === segCount - 1) ? R_SMALL + R_BIG : DIAM_SMALL
      }
      segs.push(base + extra / segCount)
    }

    let pos = startY
    for (let i = 0; i < k; i++) {
      pos += segs[i]
      result.push({ node: nodes[a + 1 + i], y: pos })
    }
    pos += segs[k]
    result.push({ node: nodes[b], y: pos })
  }

  // 最后一个大节点 → 链尾
  {
    const last = bigIdx[bigIdx.length - 1]
    const lastY = result[last].y
    let pos = lastY
    for (let i = last + 1; i < nodes.length; i++) {
      // 普通节点之间 d；后续若还有大节点不会走到这里（bigIdx 已含全部）
      pos += DIAM_SMALL
      result.push({ node: nodes[i], y: pos })
    }
  }

  return result
}
