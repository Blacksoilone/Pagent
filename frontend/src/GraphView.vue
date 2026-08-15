<script setup lang="ts">
import { computed, ref } from 'vue'
import { computeBranchLayout, type BranchNode } from './branchLayout'
import type { LayoutOptions } from './layout'
import { forkChain, promoteNode, demoteNode, materializeNode, createTestNode, type NodeDTO } from './api'

const props = defineProps<{
  chainId: string
  nodes: NodeDTO[]
  settings: Required<LayoutOptions>
}>()

const emit = defineEmits<{
  (e: 'changed'): void
  (e: 'error', msg: string): void
  (e: 'fork', anchorNodeId: string): void
}>()

// 节点操作菜单：选中节点 + 菜单位置（svg 坐标）
const menu = ref<{ node: NodeDTO; x: number; y: number } | null>(null)
const busy = ref(false)
// 测试模式（开发构建）：手工新建节点，不依赖 LLM
const isDev = import.meta.env.DEV
const testHint = ref('')

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

function openMenu(node: NodeDTO, x: number, y: number) {
  menu.value = { node, x, y }
}

function closeMenu() {
  menu.value = null
}

async function run(action: 'fork' | 'promote' | 'demote' | 'materialize') {
  const m = menu.value
  if (!m || busy.value) return
  busy.value = true
  try {
    switch (action) {
      case 'fork':
        await forkChain(props.chainId, m.node.id)
        emit('fork', m.node.id)
        break
      case 'promote':
        await promoteNode(props.chainId, m.node.id)
        break
      case 'demote':
        await demoteNode(props.chainId, m.node.id)
        break
      case 'materialize':
        await materializeNode(props.chainId, m.node.id)
        break
    }
    emit('changed')
  } catch (e) {
    emit('error', String(e))
  } finally {
    busy.value = false
    closeMenu()
  }
}

// 测试专用：新建节点（normal/ghost/important），仅开发模式显示按钮
async function addTestNode(kind: 'normal' | 'ghost' | 'important') {
  if (busy.value) return
  busy.value = true
  testHint.value = ''
  try {
    await createTestNode(props.chainId, kind)
    testHint.value = '已创建 ' + kind + ' 节点'
    emit('changed')
  } catch (e) {
    testHint.value = '创建失败: ' + String(e)
  } finally {
    busy.value = false
  }
}

function menuActions(): { key: 'fork' | 'promote' | 'demote' | 'materialize'; label: string }[] {
  const m = menu.value
  if (!m) return []
  const kind = m.node.kind
  const actions: { key: 'fork' | 'promote' | 'demote' | 'materialize'; label: string }[] = []
  actions.push({ key: 'fork', label: '分支' })
  if (kind === 'normal') actions.push({ key: 'promote', label: '提升' })
  if (kind === 'important') actions.push({ key: 'demote', label: '降低' })
  if (kind === 'ghost') actions.push({ key: 'materialize', label: '实化' })
  return actions
}
</script>

<template>
  <main class="graph-main">
    <div class="graph-toolbar">
      <slot name="title" />
      <span class="node-count">{{ nodes.length }} 个节点</span>
      <span class="toolbar-spacer" />
      <!-- 仅开发构建：测试专用手工建节点（后端需 PAGENT_TEST_MODE=1） -->
      <template v-if="isDev">
        <span class="test-hint">{{ testHint }}</span>
        <span class="test-group">
          <button class="test-btn" :disabled="busy" @click="addTestNode('normal')">+普通</button>
          <button class="test-btn" :disabled="busy" @click="addTestNode('ghost')">+虚</button>
          <button class="test-btn" :disabled="busy" @click="addTestNode('important')">+重要</button>
        </span>
      </template>
    </div>
    <div class="bracelet-wrap">
      <svg class="chain-svg" :viewBox="'0 0 ' + branchResult.width + ' ' + (braceletHeight + 40)"
        :width="branchResult.width" @click="closeMenu">
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
        <!-- 珠子（后渲染，盖住链线交叉点；可点击弹出操作菜单） -->
        <g v-for="(b, i) in branchResult.segments" :key="'seg-' + i">
          <g v-for="it in b.items" :key="it.node.id" :transform="'translate(' + b.x + ',' + it.y + ')'"
            class="bead-g" :class="{ selected: menu && menu.node.id === it.nodeId }"
            @click.stop="openMenu(nodes.find(n => n.id === it.nodeId)!, b.x, it.y)">
            <circle
              :r="it.big ? settings.diamBig / 2 : settings.diamSmall / 2"
              :fill="it.node.ghost ? '#fff' : (it.big ? settings.colorBig : settings.colorSmall)"
              :stroke="it.node.ghost ? settings.colorSmall : (it.big ? settings.colorBig : 'none')"
              :stroke-width="it.node.ghost ? 2 : 0"
              :stroke-dasharray="it.node.ghost ? '3,2' : 'none'"
              :opacity="it.status === 'partial' ? 0.5 : 1"
            />
            <!-- 选中放大圆环 -->
            <circle v-if="menu && menu.node.id === it.nodeId"
              :r="(it.big ? settings.diamBig : settings.diamSmall) / 2 + 5"
              fill="none" stroke="#4a90d9" stroke-width="2" />
          </g>
        </g>
      </svg>
    </div>

    <!-- 节点操作菜单 -->
    <div v-if="menu" class="node-menu" :style="{ left: menu.x + 'px', top: menu.y + 'px' }" @click.stop>
      <div class="menu-title">{{ menu.node.kind === 'ghost' ? '虚节点' : menu.node.kind === 'important' ? '重要节点' : '节点' }}</div>
      <button v-for="a in menuActions()" :key="a.key" class="menu-item" :disabled="busy" @click="run(a.key)">
        {{ a.label }}
      </button>
    </div>
  </main>
</template>

<style scoped>
.graph-main { flex: 1; overflow: auto; padding: 16px; position: relative; }
.graph-toolbar { display: flex; align-items: center; gap: 16px; font-size: 13px; color: #555; padding: 8px 0; }
.toolbar-spacer { flex: 1; }
.test-hint { font-size: 12px; color: #b8860b; }
.test-group { display: flex; gap: 6px; }
.test-btn {
  padding: 3px 10px; border: 1px dashed #c9a86a; border-radius: 6px;
  background: #fdf8ee; color: #8a6d1f; font-size: 12px; cursor: pointer;
}
.test-btn:hover { background: #f7ecd2; }
.test-btn:disabled { opacity: 0.5; cursor: default; }
.bracelet-wrap { display: flex; justify-content: center; }
.chain-svg { min-height: 300px; }
.bead-g { cursor: pointer; }
.bead-g:hover circle:first-of-type { filter: brightness(1.15); }

.node-menu {
  position: absolute;
  background: #fff;
  border: 1px solid #e3e3ec;
  border-radius: 10px;
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.12);
  padding: 6px;
  min-width: 110px;
  z-index: 10;
}
.menu-title { font-size: 11px; color: #9a9ab0; padding: 4px 8px 6px; }
.menu-item {
  display: block; width: 100%; text-align: left;
  padding: 7px 10px; border: none; background: none;
  font-size: 13px; color: #333; border-radius: 6px; cursor: pointer;
}
.menu-item:hover { background: #f0f0f5; }
.menu-item:disabled { opacity: 0.5; cursor: default; }
</style>
