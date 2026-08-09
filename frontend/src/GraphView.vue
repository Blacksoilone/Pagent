<script setup lang="ts">
import { computed } from 'vue'
import { computeBranchLayout, type BranchNode } from './branchLayout'
import type { LayoutOptions } from './layout'
import type { NodeDTO } from './api'

const props = defineProps<{
  nodes: NodeDTO[]
  settings: Required<LayoutOptions>
}>()

const branchResult = computed(() => {
  const bns: BranchNode[] = props.nodes.map(n => ({
    id: n.id,
    seq: n.seq,
    parentId: n.parent_id || '',
    branch: n.branch || '',
    kind: n.kind,
    status: n.status,
    copiedFrom: n.copied_from || '',
    parts: n.parts,
  }))
  return computeBranchLayout(bns, props.settings)
})

const braceletHeight = computed(() => branchResult.value.height)
</script>

<template>
  <main class="graph-main">
    <div class="graph-toolbar">
      <slot name="title" />
      <span class="node-count">{{ nodes.length }} 个节点</span>
    </div>
    <div class="bracelet-wrap">
      <svg class="chain-svg" :viewBox="'0 0 ' + branchResult.width + ' ' + (braceletHeight + 40)"
        :width="branchResult.width">
        <!-- 分叉弧线：锚点 → 各分支第一个节点（倒 Y，左上 → 右下） -->
        <path v-for="(f, i) in branchResult.forks" :key="'fork-' + i"
          :d="f.d"
          :stroke="settings.colorLine" :stroke-width="1.5"
          fill="none" stroke-linecap="round" opacity="0.7" />
        <!-- 链线（串绳）：先于珠子渲染，保证珠子盖住线 -->
        <line v-for="(b, i) in branchResult.segments" :key="'segline-' + i"
          :x1="b.x" :y1="b.topY" :x2="b.x" :y2="b.bottomY"
          :stroke="settings.colorLine" :stroke-width="settings.lineWidth"
          stroke-linecap="round" opacity="0.6" />
        <!-- 珠子（后渲染，盖住链线交叉点） -->
        <g v-for="(b, i) in branchResult.segments" :key="'seg-' + i">
          <g v-for="it in b.items" :key="it.node.id" :transform="'translate(' + b.x + ',' + it.y + ')'">
            <circle
              :r="it.big ? settings.diamBig / 2 : settings.diamSmall / 2"
              :fill="it.node.ghost ? '#fff' : (it.big ? settings.colorBig : settings.colorSmall)"
              :stroke="it.node.ghost ? settings.colorSmall : (it.big ? settings.colorBig : 'none')"
              :stroke-width="it.node.ghost ? 2 : 0"
              :stroke-dasharray="it.node.ghost ? '3,2' : 'none'"
              :opacity="it.status === 'partial' ? 0.5 : 1"
            />
          </g>
        </g>
      </svg>
    </div>
  </main>
</template>

<style scoped>
.graph-main { flex: 1; overflow: auto; padding: 16px; }
.graph-toolbar { display: flex; gap: 16px; font-size: 13px; color: #555; padding: 8px 0; }
.bracelet-wrap { display: flex; justify-content: center; }
.chain-svg { min-height: 300px; }
</style>
