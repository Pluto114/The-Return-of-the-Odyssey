<script setup>
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, MarkLineComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

echarts.use([LineChart, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent, CanvasRenderer])

const DEFAULT_THRESHOLDS = Object.freeze({ tickMs: 20, queueDepth: 8, rejections: 1, droppedSnapshots: 10 })
const snapshot = ref(null)
const connectionState = ref('connecting')
const lastError = ref('')
const lastReceivedAt = ref(0)
const now = ref(Date.now())
const watchPaused = ref(false)
const pendingSnapshot = ref(null)
const sampleWindow = ref(60)
const chartElement = ref(null)
const roomSearch = ref('')
const roomPhase = ref('all')
const roomSort = ref('room')
const onlyAttention = ref(false)
const selectedRoomID = ref(null)
const directorRoomFilter = ref('all')
const selectedDecisionKey = ref('')
const showAcknowledged = ref(false)
const acknowledgedAlerts = ref(new Set())
const thresholds = ref({ ...DEFAULT_THRESHOLDS })
const notice = ref('')
const samples = []
let chart, socket, reconnectTimer, pollTimer, clockTimer, noticeTimer
let reconnectDelay = 1000
let socketGeneration = 0

const rooms = computed(() => snapshot.value?.rooms ?? [])
const decisions = computed(() => [...(snapshot.value?.recent_director_decisions ?? [])].reverse())
const latestTickWork = computed(() => rooms.value.reduce((maximum, room) => Math.max(maximum, room.tick_work_ms ?? 0), 0))
const tickBudgetRatio = computed(() => Math.min(1, latestTickWork.value / 33.33))
const activeTickSegments = computed(() => Math.ceil(tickBudgetRatio.value * 30))
const queueDepth = computed(() => (snapshot.value?.room_queues?.control_depth ?? 0) + (snapshot.value?.room_queues?.input_depth ?? 0) + (snapshot.value?.network?.reliable_queue_depth ?? 0))
const roomPhaseOptions = computed(() => [...new Set(rooms.value.map((room) => room.phase))].sort())
const directorRoomOptions = computed(() => [...new Set(decisions.value.map((decision) => decision.room_id))].sort((left, right) => left - right))
const selectedRoom = computed(() => rooms.value.find((room) => room.room_id === selectedRoomID.value) ?? null)
const updatedLabel = computed(() => !snapshot.value?.generated_at ? '等待第一份遥测' : new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(snapshot.value.generated_at)))
const freshnessLabel = computed(() => {
  if (!lastReceivedAt.value) return '尚未收到数据'
  const seconds = Math.max(0, Math.floor((now.value - lastReceivedAt.value) / 1000))
  return seconds < 2 ? '刚刚更新' : `${seconds} 秒前更新`
})
const connectionLabel = computed(() => watchPaused.value ? '画面已冻结' : ({ live: '实时链路', polling: '轮询接替', connecting: '正在接入', offline: '链路中断' })[connectionState.value])

function roomPressure(room) { return (room.control_queue_depth ?? 0) + (room.input_queue_depth ?? 0) }
function roomRejects(room) { return (room.queue_rejections ?? 0) + (room.rejected_inputs ?? 0) }
function roomDrops(room) { return (room.dropped_snapshots ?? 0) + (room.dropped_tick_samples ?? 0) }
function roomNeedsAttention(room) {
  return (room.tick_work_ms ?? 0) >= thresholds.value.tickMs || roomPressure(room) >= thresholds.value.queueDepth || roomRejects(room) >= thresholds.value.rejections || roomDrops(room) >= thresholds.value.droppedSnapshots
}

const filteredRooms = computed(() => {
  const query = roomSearch.value.trim().toLowerCase().replace(/^#/, '')
  return rooms.value.filter((room) => {
    if (query && !String(room.room_id).includes(query) && !String(room.stage_seed).includes(query)) return false
    if (roomPhase.value !== 'all' && room.phase !== roomPhase.value) return false
    return !onlyAttention.value || roomNeedsAttention(room)
  }).sort((left, right) => {
    if (roomSort.value === 'tick') return (right.tick_work_ms ?? 0) - (left.tick_work_ms ?? 0)
    if (roomSort.value === 'queue') return roomPressure(right) - roomPressure(left)
    if (roomSort.value === 'entities') return ((right.monsters ?? 0) + (right.projectiles ?? 0)) - ((left.monsters ?? 0) + (left.projectiles ?? 0))
    return left.room_id - right.room_id
  })
})
const filteredDecisions = computed(() => decisions.value.filter((decision) => directorRoomFilter.value === 'all' || decision.room_id === Number(directorRoomFilter.value)))
const activeAlerts = computed(() => {
  const alerts = []
  if (connectionState.value === 'offline') alerts.push({ id: 'connection-offline', level: 'critical', title: '管理链路中断', detail: '实时与轮询都没有拿到状态，请检查 gameserver Admin 端口。' })
  else if (connectionState.value === 'polling') alerts.push({ id: 'connection-polling', level: 'warning', title: 'WebSocket 已降级', detail: '当前由每 5 秒一次的 HTTP 轮询接替实时链路。' })
  if ((snapshot.value?.match_queue_players ?? 0) > 1) alerts.push({ id: 'match-queue', level: 'warning', title: '匹配队列出现积压', detail: `${snapshot.value.match_queue_players} 名玩家仍在等待匹配。` })
  for (const room of rooms.value) {
    if ((room.tick_work_ms ?? 0) >= thresholds.value.tickMs) alerts.push({ id: `tick-${room.room_id}`, level: 'critical', roomID: room.room_id, title: `Room #${room.room_id} Tick 接近预算`, detail: `${room.tick_work_ms.toFixed(2)}ms，当前阈值 ${thresholds.value.tickMs}ms。` })
    if (roomPressure(room) >= thresholds.value.queueDepth) alerts.push({ id: `queue-${room.room_id}`, level: 'warning', roomID: room.room_id, title: `Room #${room.room_id} 队列积压`, detail: `控制与输入队列合计 ${roomPressure(room)}。` })
    if (roomRejects(room) >= thresholds.value.rejections) alerts.push({ id: `reject-${room.room_id}`, level: 'warning', roomID: room.room_id, title: `Room #${room.room_id} 出现拒绝`, detail: `累计拒绝 ${roomRejects(room)} 次。` })
    if (roomDrops(room) >= thresholds.value.droppedSnapshots) alerts.push({ id: `drop-${room.room_id}`, level: 'warning', roomID: room.room_id, title: `Room #${room.room_id} 遥测发生替换`, detail: `累计替换或丢弃 ${roomDrops(room)} 次。` })
  }
  return alerts
})
const visibleAlerts = computed(() => activeAlerts.value.filter((alert) => showAcknowledged.value || !acknowledgedAlerts.value.has(alert.id)))
const acknowledgedCount = computed(() => activeAlerts.value.filter((alert) => acknowledgedAlerts.value.has(alert.id)).length)

function applySnapshot(next, force = false) {
  lastReceivedAt.value = Date.now()
  if (watchPaused.value && !force) { pendingSnapshot.value = next; return }
  snapshot.value = next
  pendingSnapshot.value = null
  const maximumTick = (next.rooms ?? []).reduce((maximum, room) => Math.max(maximum, room.tick_work_ms ?? 0), 0)
  samples.push({ time: new Date(next.generated_at), tick: maximumTick, control: next.room_queues?.control_depth ?? 0, input: next.room_queues?.input_depth ?? 0, reliable: next.network?.reliable_queue_depth ?? 0 })
  if (samples.length > 600) samples.shift()
  renderChart()
}

async function fetchSnapshot(force = false) {
  try {
    const response = await fetch('/api/status', { cache: 'no-store' })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    applySnapshot(await response.json(), force)
    if (connectionState.value !== 'live') connectionState.value = 'polling'
    lastError.value = ''
  } catch (error) {
    connectionState.value = 'offline'
    lastError.value = `管理接口不可用：${error.message}`
  }
}

function connect() {
  clearTimeout(reconnectTimer)
  const generation = ++socketGeneration
  if (socket) { socket.onclose = null; socket.close() }
  connectionState.value = 'connecting'
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  socket = new WebSocket(`${scheme}://${window.location.host}/ws`)
  socket.addEventListener('open', () => {
    if (generation !== socketGeneration) return
    connectionState.value = 'live'; lastError.value = ''; reconnectDelay = 1000
  })
  socket.addEventListener('message', (event) => {
    if (generation !== socketGeneration) return
    try { applySnapshot(JSON.parse(event.data)) } catch { lastError.value = '收到无法解析的遥测数据' }
  })
  socket.addEventListener('close', () => {
    if (generation !== socketGeneration) return
    connectionState.value = snapshot.value ? 'polling' : 'offline'
    reconnectTimer = setTimeout(connect, reconnectDelay)
    reconnectDelay = Math.min(reconnectDelay * 2, 15000)
  })
  socket.addEventListener('error', () => socket.close())
}

function visibleSamples() { return samples.slice(-sampleWindow.value) }
function renderChart() {
  if (!chart) return
  const current = visibleSamples()
  chart.setOption({
    xAxis: { data: current.map((sample) => sample.time.toLocaleTimeString('zh-CN', { hour12: false })) },
    series: [
      { name: '最慢房间 Tick', data: current.map((sample) => sample.tick) },
      { name: 'Room 队列', data: current.map((sample) => sample.control + sample.input) },
      { name: '网络可靠队列', data: current.map((sample) => sample.reliable) },
    ],
  })
}
function initializeChart() {
  if (!chartElement.value) return
  chart = echarts.init(chartElement.value, null, { renderer: 'canvas' })
  chart.setOption({
    animationDuration: 280, backgroundColor: 'transparent', color: ['#e49a62', '#45c7b6', '#8ca7c8'], grid: { left: 46, right: 20, top: 42, bottom: 32 },
    legend: { top: 2, right: 0, textStyle: { color: '#9eabc0', fontFamily: 'Cascadia Mono, monospace', fontSize: 11 } },
    tooltip: { trigger: 'axis', backgroundColor: '#111d30', borderColor: '#314158', textStyle: { color: '#f2ead8' } },
    xAxis: { type: 'category', boundaryGap: false, axisLine: { lineStyle: { color: '#314158' } }, axisLabel: { color: '#7f8da4', hideOverlap: true } },
    yAxis: [{ type: 'value', name: 'ms', nameTextStyle: { color: '#7f8da4' }, splitLine: { lineStyle: { color: '#233147' } }, axisLabel: { color: '#7f8da4' } }, { type: 'value', show: false }],
    series: [
      { name: '最慢房间 Tick', type: 'line', smooth: 0.22, symbol: 'none', lineStyle: { width: 2 }, markLine: { silent: true, symbol: 'none', label: { formatter: '33.33ms 预算', color: '#e49a62' }, lineStyle: { color: '#e49a62', type: 'dashed' }, data: [{ yAxis: 33.33 }] } },
      { name: 'Room 队列', type: 'line', yAxisIndex: 1, step: 'end', symbol: 'none', lineStyle: { width: 1.5 } },
      { name: '网络可靠队列', type: 'line', yAxisIndex: 1, step: 'end', symbol: 'none', lineStyle: { width: 1.5 } },
    ],
  })
  renderChart()
}

function togglePause() {
  watchPaused.value = !watchPaused.value
  if (!watchPaused.value && pendingSnapshot.value) applySnapshot(pendingSnapshot.value, true)
  showNotice(watchPaused.value ? '画面已冻结，后台仍继续接收数据' : '已恢复实时画面')
}
function clearTrail() { samples.splice(0, samples.length); renderChart(); showNotice('本次浏览器中的航迹已清空') }
function setSampleWindow() { renderChart() }
function openRoom(roomID) { selectedRoomID.value = roomID }
function focusAlertRoom(alert) { if (alert.roomID) selectedRoomID.value = alert.roomID }
function acknowledgeAlert(id) { const next = new Set(acknowledgedAlerts.value); next.add(id); acknowledgedAlerts.value = next }
function restoreAlerts() { acknowledgedAlerts.value = new Set(); showNotice('已恢复全部当前告警') }
function loadThresholds() {
  try {
    const saved = JSON.parse(localStorage.getItem('odyssey-watch-thresholds') ?? 'null')
    if (saved && typeof saved === 'object') thresholds.value = { ...DEFAULT_THRESHOLDS, ...saved }
  } catch { thresholds.value = { ...DEFAULT_THRESHOLDS } }
}
function saveThresholds() {
  thresholds.value.tickMs = Math.max(1, Math.min(33.33, Number(thresholds.value.tickMs) || DEFAULT_THRESHOLDS.tickMs))
  thresholds.value.queueDepth = Math.max(1, Number(thresholds.value.queueDepth) || DEFAULT_THRESHOLDS.queueDepth)
  thresholds.value.rejections = Math.max(1, Number(thresholds.value.rejections) || DEFAULT_THRESHOLDS.rejections)
  thresholds.value.droppedSnapshots = Math.max(1, Number(thresholds.value.droppedSnapshots) || DEFAULT_THRESHOLDS.droppedSnapshots)
  localStorage.setItem('odyssey-watch-thresholds', JSON.stringify(thresholds.value))
  showNotice('告警阈值已保存到当前浏览器')
}
function resetThresholds() { thresholds.value = { ...DEFAULT_THRESHOLDS }; saveThresholds() }
function downloadBlob(name, content, type) {
  const url = URL.createObjectURL(new Blob([content], { type }))
  const anchor = document.createElement('a'); anchor.href = url; anchor.download = name; anchor.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
function exportSnapshot() {
  if (!snapshot.value) return
  const stamp = new Date(snapshot.value.generated_at).toISOString().replaceAll(':', '-').replace(/\.\d{3}Z$/, 'Z')
  downloadBlob(`odyssey-status-${stamp}.json`, JSON.stringify(snapshot.value, null, 2), 'application/json;charset=utf-8')
  showNotice('当前状态 JSON 已导出')
}
function csvCell(value) { return `"${String(value ?? '').replaceAll('"', '""')}"` }
function exportRooms() {
  const headers = ['房间', '阶段', '关卡', 'Seed', 'Tick', '玩家', '怪物', '投射物', '控制队列', '输入队列', 'Tick耗时ms', '拒绝', '丢弃']
  const rows = filteredRooms.value.map((room) => [room.room_id, phaseLabel(room.phase), room.stage_index, room.stage_seed, room.server_tick, room.players, room.monsters, room.projectiles, room.control_queue_depth, room.input_queue_depth, room.tick_work_ms, roomRejects(room), roomDrops(room)])
  const csv = `\uFEFF${[headers, ...rows].map((row) => row.map(csvCell).join(',')).join('\r\n')}`
  downloadBlob('odyssey-rooms.csv', csv, 'text/csv;charset=utf-8')
  showNotice(`已导出 ${rows.length} 个筛选后的房间`)
}
async function copyRoomSummary() {
  if (!selectedRoom.value) return
  const room = selectedRoom.value
  const summary = `Room #${room.room_id} | ${phaseLabel(room.phase)} Stage ${room.stage_index} | Tick ${room.server_tick} | 玩家 ${room.players} | 实体 ${room.monsters}/${room.projectiles} | 队列 ${room.control_queue_depth}/${room.input_queue_depth} | 耗时 ${room.tick_work_ms.toFixed(2)}ms | 拒绝 ${roomRejects(room)} | 丢弃 ${roomDrops(room)}`
  try { await navigator.clipboard.writeText(summary); showNotice('房间诊断摘要已复制') } catch { showNotice('浏览器未允许复制，请在本机 localhost 打开看板') }
}
function toggleDecision(decision) { const key = decisionKey(decision); selectedDecisionKey.value = selectedDecisionKey.value === key ? '' : key }
function decisionKey(decision) { return `${decision.room_id}-${decision.stage_index}-${decision.observed_at}` }
function showNotice(message) { notice.value = message; clearTimeout(noticeTimer); noticeTimer = setTimeout(() => { notice.value = '' }, 2800) }
function formatUptime(seconds) {
  if (!Number.isFinite(seconds)) return '—'
  const total = Math.floor(seconds), hours = Math.floor(total / 3600), minutes = Math.floor((total % 3600) / 60), remaining = total % 60
  return `${hours.toString().padStart(2, '0')}:${minutes.toString().padStart(2, '0')}:${remaining.toString().padStart(2, '0')}`
}
function phaseLabel(phase) { return ({ waiting: '等待', playing: '战斗', stage_clear: '清场', reward: '奖励', preparing_next_stage: '整备', failed: '失败', closed: '关闭' })[phase] ?? phase }
function formatPercent(value) { return Number.isFinite(value) ? `${Math.round(value * 100)}%` : '—' }
function resizeChart() { chart?.resize() }
function handleKeydown(event) { if (event.key === 'Escape') selectedRoomID.value = null }

onMounted(async () => {
  loadThresholds(); await fetchSnapshot(); await nextTick(); initializeChart(); connect()
  pollTimer = setInterval(() => { if (connectionState.value !== 'live') fetchSnapshot() }, 5000)
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
  window.addEventListener('resize', resizeChart); window.addEventListener('keydown', handleKeydown)
})
onBeforeUnmount(() => {
  socketGeneration += 1; clearTimeout(reconnectTimer); clearTimeout(noticeTimer); clearInterval(pollTimer); clearInterval(clockTimer)
  if (socket) { socket.onclose = null; socket.close() }
  chart?.dispose(); window.removeEventListener('resize', resizeChart); window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <main class="shell">
    <header class="masthead">
      <div><p class="kicker">ODYSSEY / SERVER WATCH</p><h1>返航值守台</h1><p class="lede">看见每一个房间的节拍，在异常变成事故之前留下航迹。</p></div>
      <div class="connection" :class="[`is-${connectionState}`, { 'is-paused': watchPaused }]" aria-live="polite"><span class="signal" aria-hidden="true"></span><div><b>{{ connectionLabel }}</b><small>{{ updatedLabel }} · {{ freshnessLabel }}</small></div></div>
    </header>

    <nav class="bridge-rail" aria-label="值守控制">
      <div class="rail-identity"><span>舰桥控制</span><small>只读操作，不修改对局</small></div>
      <div class="rail-actions">
        <button type="button" class="control-button primary" :aria-pressed="watchPaused" @click="togglePause">{{ watchPaused ? '恢复实时' : '冻结画面' }}</button>
        <button type="button" class="control-button" @click="fetchSnapshot(true)">立即取样</button><button type="button" class="control-button" @click="connect">重连链路</button>
        <label class="select-control"><span>航迹窗口</span><select v-model.number="sampleWindow" @change="setSampleWindow"><option :value="30">30秒</option><option :value="60">60秒</option><option :value="120">120秒</option><option :value="300">5分钟</option></select></label>
        <button type="button" class="control-button" @click="clearTrail">清空航迹</button><button type="button" class="control-button" :disabled="!snapshot" @click="exportSnapshot">导出状态</button><button type="button" class="control-button" :disabled="!snapshot" @click="exportRooms">导出房间</button>
      </div>
    </nav>

    <div v-if="lastError" class="alert" role="alert"><span>{{ lastError }}</span><button type="button" @click="fetchSnapshot(true); connect()">重新连接</button></div>

    <template v-if="snapshot">
      <section class="voyage-strip" aria-label="服务器总览">
        <div class="voyage-copy"><span>环境 {{ snapshot.environment }}</span><strong>{{ formatUptime(snapshot.uptime_seconds) }}</strong><small>本次航程运行时长</small></div>
        <dl class="headline-stats"><div><dt>在线玩家</dt><dd>{{ snapshot.online_players }}</dd></div><div><dt>活跃房间</dt><dd>{{ snapshot.active_rooms }}</dd></div><div><dt>等待匹配</dt><dd>{{ snapshot.match_queue_players }}</dd></div><div><dt>敌人 / 投射物</dt><dd>{{ snapshot.active_monsters }} <i>/</i> {{ snapshot.active_projectiles }}</dd></div><div><dt>队列深度</dt><dd>{{ queueDepth }}</dd></div></dl>
      </section>

      <section class="panel cadence-panel">
        <div class="section-heading"><div><p class="eyebrow">TICK BUDGET / 30 HZ</p><h2>单帧航迹</h2></div><div class="budget-readout"><span v-if="watchPaused" class="frozen-tag">FROZEN</span><strong>{{ latestTickWork.toFixed(2) }}</strong><span>ms / 33.33ms</span></div></div>
        <div class="cadence-rail" :aria-label="`当前最慢房间 Tick 使用 ${Math.round(tickBudgetRatio * 100)}% 预算`"><span v-for="index in 30" :key="index" :class="{ active: index <= activeTickSegments, warning: tickBudgetRatio > 0.8 && index <= activeTickSegments }"></span></div>
        <div class="chart-shell"><div ref="chartElement" class="telemetry-chart" role="img" aria-label="最近实时 Tick 耗时与队列深度折线图"></div><p v-if="!rooms.length" class="chart-empty">等待房间启动后记录 Tick 与队列轨迹</p></div>
      </section>

      <section class="panel room-panel">
        <div class="section-heading room-heading"><div><p class="eyebrow">AUTHORITATIVE ROOMS</p><h2>房间值守日志</h2></div><span class="result-count">显示 {{ filteredRooms.length }} / {{ rooms.length }} 个房间</span></div>
        <div class="room-tools" aria-label="房间筛选工具">
          <label class="search-control"><span>搜索</span><input v-model="roomSearch" type="search" placeholder="房间号或 Seed" /></label>
          <label class="select-control"><span>阶段</span><select v-model="roomPhase"><option value="all">全部阶段</option><option v-for="phase in roomPhaseOptions" :key="phase" :value="phase">{{ phaseLabel(phase) }}</option></select></label>
          <label class="select-control"><span>排序</span><select v-model="roomSort"><option value="room">房间编号</option><option value="tick">Tick 耗时</option><option value="queue">队列压力</option><option value="entities">实体数量</option></select></label>
          <label class="check-control"><input v-model="onlyAttention" type="checkbox" /><span>只看需关注</span></label>
        </div>
        <div v-if="filteredRooms.length" class="table-wrap"><table><thead><tr><th>房间</th><th>阶段</th><th>Tick</th><th>成员</th><th>实体</th><th>控制 / 输入队列</th><th>最近耗时</th><th>拒绝 / 丢弃</th><th></th></tr></thead><tbody><tr v-for="room in filteredRooms" :key="room.room_id" :class="{ 'needs-attention': roomNeedsAttention(room) }"><td><b>#{{ room.room_id }}</b><small>seed {{ room.stage_seed }}</small></td><td><span class="phase">{{ phaseLabel(room.phase) }}</span><small>Stage {{ room.stage_index }}</small></td><td class="mono">{{ room.server_tick }}</td><td>{{ room.players }}</td><td>{{ room.monsters }} <i>/</i> {{ room.projectiles }}</td><td class="mono">{{ room.control_queue_depth }} / {{ room.input_queue_depth }}</td><td class="mono">{{ room.tick_work_ms.toFixed(2) }}ms</td><td class="mono">{{ roomRejects(room) }} / {{ roomDrops(room) }}</td><td><button type="button" class="row-action" :aria-label="`查看房间 ${room.room_id} 详情`" @click="openRoom(room.room_id)">详情</button></td></tr></tbody></table></div>
        <div v-else class="empty-state"><b>{{ rooms.length ? '没有符合条件的房间' : '当前没有活跃房间' }}</b><span>{{ rooms.length ? '调整搜索、阶段或异常筛选后再看。' : '两个玩家匹配并进入房间后，权威状态会出现在这里。' }}</span></div>
      </section>

      <section class="operations-grid">
        <article class="panel alert-center">
          <div class="section-heading"><div><p class="eyebrow">WATCH ALERTS</p><h2>值守告警</h2></div><div class="inline-actions"><button type="button" class="text-button" @click="showAcknowledged = !showAcknowledged">{{ showAcknowledged ? '隐藏已确认' : `查看已确认 ${acknowledgedCount}` }}</button><button v-if="acknowledgedCount" type="button" class="text-button" @click="restoreAlerts">全部恢复</button></div></div>
          <ul v-if="visibleAlerts.length" class="alert-list"><li v-for="item in visibleAlerts" :key="item.id" :class="[`severity-${item.level}`, { acknowledged: acknowledgedAlerts.has(item.id) }]"><button v-if="item.roomID" type="button" class="alert-copy" @click="focusAlertRoom(item)"><b>{{ item.title }}</b><span>{{ item.detail }}</span></button><div v-else class="alert-copy"><b>{{ item.title }}</b><span>{{ item.detail }}</span></div><button type="button" class="ack-button" @click="acknowledgeAlert(item.id)">{{ acknowledgedAlerts.has(item.id) ? '已确认' : '确认' }}</button></li></ul>
          <div v-else class="clear-state"><span aria-hidden="true">✓</span><div><b>当前无需介入</b><small>连接、Tick 与队列均在设定范围内。</small></div></div>
        </article>
        <article class="panel guardrail-card">
          <div class="section-heading"><div><p class="eyebrow">LOCAL GUARDRAILS</p><h2>本机告警阈值</h2></div><button type="button" class="text-button" @click="resetThresholds">恢复默认</button></div>
          <div class="threshold-grid"><label><span>Tick 警戒</span><div><input v-model.number="thresholds.tickMs" type="number" min="1" max="33.33" step="0.5" /><em>ms</em></div></label><label><span>房间队列</span><div><input v-model.number="thresholds.queueDepth" type="number" min="1" step="1" /><em>条</em></div></label><label><span>累计拒绝</span><div><input v-model.number="thresholds.rejections" type="number" min="1" step="1" /><em>次</em></div></label><label><span>快照替换</span><div><input v-model.number="thresholds.droppedSnapshots" type="number" min="1" step="1" /><em>次</em></div></label></div>
          <button type="button" class="save-button" @click="saveThresholds">保存到当前浏览器</button>
        </article>
      </section>

      <section class="lower-grid">
        <article class="panel queue-card"><div class="section-heading"><div><p class="eyebrow">PRESSURE</p><h2>队列与背压</h2></div></div><dl class="detail-list"><div><dt>Room 控制 / 输入</dt><dd>{{ snapshot.room_queues.control_depth }} / {{ snapshot.room_queues.input_depth }}</dd></div><div><dt>网络可靠队列</dt><dd>{{ snapshot.network.reliable_queue_depth }} / {{ snapshot.network.reliable_queue_capacity }}</dd></div><div><dt>待发送快照</dt><dd>{{ snapshot.network.snapshots_pending }}</dd></div><div><dt>可靠发送拒绝</dt><dd>{{ snapshot.network.reliable_send_rejections }}</dd></div><div><dt>快照替换</dt><dd>{{ snapshot.network.snapshot_replacements + snapshot.room_queues.dropped_snapshots }}</dd></div><div><dt>输入拒绝</dt><dd>{{ snapshot.room_queues.rejected_inputs }}</dd></div></dl></article>
        <article class="panel director-card">
          <div class="section-heading director-heading"><div><p class="eyebrow">DIRECTOR LOG</p><h2>关卡决策复盘</h2></div><label class="select-control compact-select"><span>房间</span><select v-model="directorRoomFilter"><option value="all">全部</option><option v-for="roomID in directorRoomOptions" :key="roomID" :value="String(roomID)">#{{ roomID }}</option></select></label></div>
          <ol v-if="filteredDecisions.length" class="decision-list"><li v-for="decision in filteredDecisions" :key="decisionKey(decision)" :class="{ expanded: selectedDecisionKey === decisionKey(decision) }"><button type="button" class="decision-summary" :aria-expanded="selectedDecisionKey === decisionKey(decision)" @click="toggleDecision(decision)"><span><b>Room #{{ decision.room_id }} · Stage {{ decision.stage_index }}</b><small>{{ decision.reasons.join('，') }}</small></span><em>{{ decision.previous_difficulty.toFixed(2) }} → {{ decision.new_difficulty.toFixed(2) }}</em></button><dl v-if="selectedDecisionKey === decisionKey(decision)" class="decision-detail"><div><dt>团队生命</dt><dd>{{ formatPercent(decision.team_hp_percent) }}</dd></div><div><dt>平均 DPS</dt><dd>{{ decision.average_dps.toFixed(1) }}</dd></div><div><dt>清关时间</dt><dd>{{ decision.clear_time_seconds.toFixed(1) }}s</dd></div><div><dt>死亡 / 承伤</dt><dd>{{ decision.death_count }} / {{ decision.damage_taken.toFixed(0) }}</dd></div><div><dt>装备强度</dt><dd>{{ decision.equipment_power.toFixed(2) }}</dd></div><div><dt>敌人数 / 决策耗时</dt><dd>{{ decision.monster_count }} / {{ decision.duration_us }}μs</dd></div></dl></li></ol>
          <div v-else class="empty-state compact"><b>尚无匹配的关卡决策</b><span>清场、奖励完成并全员准备后，下一关决策会记入这里。</span></div>
        </article>
      </section>
    </template>

    <section v-else class="loading-state" aria-live="polite"><span></span><p>正在等待 gameserver 遥测……</p></section>
    <div v-if="selectedRoom" class="drawer-backdrop" @click.self="selectedRoomID = null"><aside class="room-drawer" aria-labelledby="room-drawer-title"><div class="drawer-head"><div><p class="eyebrow">ROOM INSPECTOR</p><h2 id="room-drawer-title">Room #{{ selectedRoom.room_id }}</h2></div><button type="button" class="close-button" aria-label="关闭房间详情" @click="selectedRoomID = null">×</button></div><div class="room-stamp"><span :class="{ alerting: roomNeedsAttention(selectedRoom) }">{{ roomNeedsAttention(selectedRoom) ? '需要关注' : '状态正常' }}</span><b>{{ phaseLabel(selectedRoom.phase) }} · Stage {{ selectedRoom.stage_index }}</b><small>Seed {{ selectedRoom.stage_seed }}</small></div><dl class="inspector-list"><div><dt>服务器 Tick</dt><dd>{{ selectedRoom.server_tick }}</dd></div><div><dt>玩家</dt><dd>{{ selectedRoom.players }} / 2</dd></div><div><dt>怪物 / 投射物</dt><dd>{{ selectedRoom.monsters }} / {{ selectedRoom.projectiles }}</dd></div><div><dt>控制 / 输入队列</dt><dd>{{ selectedRoom.control_queue_depth }} / {{ selectedRoom.input_queue_depth }}</dd></div><div><dt>最近 Tick 耗时</dt><dd>{{ selectedRoom.tick_work_ms.toFixed(2) }}ms</dd></div><div><dt>队列及输入拒绝</dt><dd>{{ roomRejects(selectedRoom) }}</dd></div><div><dt>快照及采样丢弃</dt><dd>{{ roomDrops(selectedRoom) }}</dd></div></dl><button type="button" class="save-button" @click="copyRoomSummary">复制诊断摘要</button><p class="drawer-note">值守台保持只读。需要结束房间时，请回到受控的服务端运维流程。</p></aside></div>
    <div v-if="notice" class="toast" role="status">{{ notice }}</div>
  </main>
</template>
