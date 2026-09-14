<script setup>
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, MarkLineComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

echarts.use([LineChart, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent, CanvasRenderer])

const snapshot = ref(null)
const connectionState = ref('connecting')
const lastError = ref('')
const chartElement = ref(null)
const samples = []
let chart
let socket
let reconnectTimer
let pollTimer
let reconnectDelay = 1000

const rooms = computed(() => snapshot.value?.rooms ?? [])
const decisions = computed(() => [...(snapshot.value?.recent_director_decisions ?? [])].reverse())
const latestTickWork = computed(() => rooms.value.reduce((maximum, room) => Math.max(maximum, room.tick_work_ms ?? 0), 0))
const tickBudgetRatio = computed(() => Math.min(1, latestTickWork.value / 33.33))
const activeTickSegments = computed(() => Math.ceil(tickBudgetRatio.value * 30))
const queueDepth = computed(() => (snapshot.value?.room_queues?.control_depth ?? 0) + (snapshot.value?.room_queues?.input_depth ?? 0) + (snapshot.value?.network?.reliable_queue_depth ?? 0))
const updatedLabel = computed(() => {
  if (!snapshot.value?.generated_at) return '等待第一份遥测'
  return new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(snapshot.value.generated_at))
})

function applySnapshot(next) {
  snapshot.value = next
  const maximumTick = (next.rooms ?? []).reduce((maximum, room) => Math.max(maximum, room.tick_work_ms ?? 0), 0)
  samples.push({
    time: new Date(next.generated_at),
    tick: maximumTick,
    control: next.room_queues?.control_depth ?? 0,
    input: next.room_queues?.input_depth ?? 0,
    reliable: next.network?.reliable_queue_depth ?? 0,
  })
  if (samples.length > 120) samples.shift()
  renderChart()
}

async function fetchSnapshot() {
  try {
    const response = await fetch('/api/status', { cache: 'no-store' })
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    applySnapshot(await response.json())
    if (connectionState.value !== 'live') connectionState.value = 'polling'
    lastError.value = ''
  } catch (error) {
    connectionState.value = 'offline'
    lastError.value = `管理接口不可用：${error.message}`
  }
}

function connect() {
  clearTimeout(reconnectTimer)
  socket?.close()
  connectionState.value = 'connecting'
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  socket = new WebSocket(`${scheme}://${window.location.host}/ws`)
  socket.addEventListener('open', () => {
    connectionState.value = 'live'
    lastError.value = ''
    reconnectDelay = 1000
  })
  socket.addEventListener('message', (event) => {
    try {
      applySnapshot(JSON.parse(event.data))
    } catch {
      lastError.value = '收到无法解析的遥测数据'
    }
  })
  socket.addEventListener('close', () => {
    connectionState.value = snapshot.value ? 'polling' : 'offline'
    reconnectTimer = setTimeout(connect, reconnectDelay)
    reconnectDelay = Math.min(reconnectDelay * 2, 15000)
  })
  socket.addEventListener('error', () => socket.close())
}

function renderChart() {
  if (!chart) return
  chart.setOption({
    xAxis: { data: samples.map((sample) => sample.time.toLocaleTimeString('zh-CN', { hour12: false })) },
    series: [
      { name: '最慢房间 Tick', data: samples.map((sample) => sample.tick) },
      { name: 'Room 队列', data: samples.map((sample) => sample.control + sample.input) },
      { name: '网络可靠队列', data: samples.map((sample) => sample.reliable) },
    ],
  })
}

function initializeChart() {
  if (!chartElement.value) return
  chart = echarts.init(chartElement.value, null, { renderer: 'canvas' })
  chart.setOption({
    animationDuration: 280,
    backgroundColor: 'transparent',
    color: ['#d98c5f', '#3bb7a5', '#8ca7c8'],
    grid: { left: 46, right: 20, top: 42, bottom: 32 },
    legend: { top: 2, right: 0, textStyle: { color: '#9eabc0', fontFamily: 'Cascadia Mono, monospace', fontSize: 11 } },
    tooltip: { trigger: 'axis', backgroundColor: '#111d30', borderColor: '#314158', textStyle: { color: '#f2ead8' } },
    xAxis: { type: 'category', boundaryGap: false, axisLine: { lineStyle: { color: '#314158' } }, axisLabel: { color: '#7f8da4', hideOverlap: true } },
    yAxis: [
      { type: 'value', name: 'ms', nameTextStyle: { color: '#7f8da4' }, splitLine: { lineStyle: { color: '#233147' } }, axisLabel: { color: '#7f8da4' } },
      { type: 'value', show: false },
    ],
    series: [
      { name: '最慢房间 Tick', type: 'line', smooth: 0.22, symbol: 'none', lineStyle: { width: 2 }, markLine: { silent: true, symbol: 'none', label: { formatter: '33.33ms 预算', color: '#d98c5f' }, lineStyle: { color: '#d98c5f', type: 'dashed' }, data: [{ yAxis: 33.33 }] } },
      { name: 'Room 队列', type: 'line', yAxisIndex: 1, step: 'end', symbol: 'none', lineStyle: { width: 1.5 } },
      { name: '网络可靠队列', type: 'line', yAxisIndex: 1, step: 'end', symbol: 'none', lineStyle: { width: 1.5 } },
    ],
  })
  renderChart()
}

function formatUptime(seconds) {
  if (!Number.isFinite(seconds)) return '—'
  const total = Math.floor(seconds)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const remaining = total % 60
  return `${hours.toString().padStart(2, '0')}:${minutes.toString().padStart(2, '0')}:${remaining.toString().padStart(2, '0')}`
}

function phaseLabel(phase) {
  return ({ waiting: '等待', playing: '战斗', stage_clear: '清场', reward: '奖励', preparing_next_stage: '整备', failed: '失败', closed: '关闭' })[phase] ?? phase
}

function formatPercent(value) {
  return Number.isFinite(value) ? `${Math.round(value * 100)}%` : '—'
}

function resizeChart() {
  chart?.resize()
}

onMounted(async () => {
  await fetchSnapshot()
  await nextTick()
  initializeChart()
  connect()
  pollTimer = setInterval(() => {
    if (connectionState.value !== 'live') fetchSnapshot()
  }, 5000)
  window.addEventListener('resize', resizeChart)
})

onBeforeUnmount(() => {
  clearTimeout(reconnectTimer)
  clearInterval(pollTimer)
  socket?.close()
  chart?.dispose()
  window.removeEventListener('resize', resizeChart)
})
</script>

<template>
  <main class="shell">
    <header class="masthead">
      <div>
        <p class="kicker">ODYSSEY / SERVER WATCH</p>
        <h1>返航值守台</h1>
        <p class="lede">盯住每一个房间的节拍、队列与下一关决策。</p>
      </div>
      <div class="connection" :class="`is-${connectionState}`" aria-live="polite">
        <span class="signal" aria-hidden="true"></span>
        <div>
          <b>{{ connectionState === 'live' ? '实时链路' : connectionState === 'polling' ? '轮询接替' : connectionState === 'connecting' ? '正在接入' : '链路中断' }}</b>
          <small>{{ updatedLabel }}</small>
        </div>
      </div>
    </header>

    <div v-if="lastError" class="alert" role="alert">
      <span>{{ lastError }}</span>
      <button type="button" @click="fetchSnapshot(); connect()">重新连接</button>
    </div>

    <template v-if="snapshot">
      <section class="voyage-strip" aria-label="服务器总览">
        <div class="voyage-copy">
          <span>环境 {{ snapshot.environment }}</span>
          <strong>{{ formatUptime(snapshot.uptime_seconds) }}</strong>
          <small>本次航程运行时长</small>
        </div>
        <dl class="headline-stats">
          <div><dt>在线玩家</dt><dd>{{ snapshot.online_players }}</dd></div>
          <div><dt>活跃房间</dt><dd>{{ snapshot.active_rooms }}</dd></div>
          <div><dt>等待匹配</dt><dd>{{ snapshot.match_queue_players }}</dd></div>
          <div><dt>敌人 / 投射物</dt><dd>{{ snapshot.active_monsters }} <i>/</i> {{ snapshot.active_projectiles }}</dd></div>
          <div><dt>队列深度</dt><dd>{{ queueDepth }}</dd></div>
        </dl>
      </section>

      <section class="panel cadence-panel">
        <div class="section-heading">
          <div><p class="eyebrow">TICK BUDGET / 30 HZ</p><h2>单帧航迹</h2></div>
          <div class="budget-readout"><strong>{{ latestTickWork.toFixed(2) }}</strong><span>ms / 33.33ms</span></div>
        </div>
        <div class="cadence-rail" :aria-label="`当前最慢房间 Tick 使用 ${Math.round(tickBudgetRatio * 100)}% 预算`">
          <span v-for="index in 30" :key="index" :class="{ active: index <= activeTickSegments, warning: tickBudgetRatio > 0.8 && index <= activeTickSegments }"></span>
        </div>
        <div class="chart-shell">
          <div ref="chartElement" class="telemetry-chart" role="img" aria-label="最近实时 Tick 耗时与队列深度折线图"></div>
          <p v-if="!rooms.length" class="chart-empty">等待房间启动后记录 Tick 与队列轨迹</p>
        </div>
      </section>

      <section class="panel room-panel">
        <div class="section-heading">
          <div><p class="eyebrow">AUTHORITATIVE ROOMS</p><h2>房间值守日志</h2></div>
          <span class="quiet">按 Room ID 排序 · 数据来自权威快照</span>
        </div>
        <div v-if="rooms.length" class="table-wrap">
          <table>
            <thead><tr><th>房间</th><th>阶段</th><th>Tick</th><th>成员</th><th>实体</th><th>控制 / 输入队列</th><th>最近耗时</th><th>拒绝 / 丢弃</th></tr></thead>
            <tbody>
              <tr v-for="room in rooms" :key="room.room_id">
                <td><b>#{{ room.room_id }}</b><small>seed {{ room.stage_seed }}</small></td>
                <td><span class="phase">{{ phaseLabel(room.phase) }}</span><small>Stage {{ room.stage_index }}</small></td>
                <td class="mono">{{ room.server_tick }}</td>
                <td>{{ room.players }}</td>
                <td>{{ room.monsters }} <i>/</i> {{ room.projectiles }}</td>
                <td class="mono">{{ room.control_queue_depth }} / {{ room.input_queue_depth }}</td>
                <td class="mono">{{ room.tick_work_ms.toFixed(2) }}ms</td>
                <td class="mono">{{ room.queue_rejections + room.rejected_inputs }} / {{ room.dropped_snapshots + room.dropped_tick_samples }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="empty-state"><b>当前没有活跃房间</b><span>两个玩家匹配并进入房间后，权威状态会出现在这里。</span></div>
      </section>

      <section class="lower-grid">
        <article class="panel queue-card">
          <div class="section-heading"><div><p class="eyebrow">PRESSURE</p><h2>队列与背压</h2></div></div>
          <dl class="detail-list">
            <div><dt>Room 控制 / 输入</dt><dd>{{ snapshot.room_queues.control_depth }} / {{ snapshot.room_queues.input_depth }}</dd></div>
            <div><dt>网络可靠队列</dt><dd>{{ snapshot.network.reliable_queue_depth }} / {{ snapshot.network.reliable_queue_capacity }}</dd></div>
            <div><dt>可靠发送拒绝</dt><dd>{{ snapshot.network.reliable_send_rejections }}</dd></div>
            <div><dt>快照替换</dt><dd>{{ snapshot.network.snapshot_replacements + snapshot.room_queues.dropped_snapshots }}</dd></div>
            <div><dt>输入拒绝</dt><dd>{{ snapshot.room_queues.rejected_inputs }}</dd></div>
          </dl>
        </article>

        <article class="panel director-card">
          <div class="section-heading"><div><p class="eyebrow">DIRECTOR LOG</p><h2>最近关卡决策</h2></div></div>
          <ol v-if="decisions.length" class="decision-list">
            <li v-for="decision in decisions.slice(0, 5)" :key="`${decision.room_id}-${decision.stage_index}-${decision.observed_at}`">
              <div><b>Room #{{ decision.room_id }} · Stage {{ decision.stage_index }}</b><span>{{ decision.previous_difficulty.toFixed(2) }} → {{ decision.new_difficulty.toFixed(2) }}</span></div>
              <p>{{ decision.reasons.join('，') }}</p>
              <small>HP {{ formatPercent(decision.team_hp_percent) }} · DPS {{ decision.average_dps.toFixed(1) }} · {{ decision.monster_count }} 个敌人 · {{ decision.duration_us }}μs</small>
            </li>
          </ol>
          <div v-else class="empty-state compact"><b>尚无已应用决策</b><span>清场、奖励完成并全员准备后，下一关决策会记入这里。</span></div>
        </article>
      </section>
    </template>

    <section v-else class="loading-state" aria-live="polite"><span></span><p>正在等待 gameserver 遥测……</p></section>
  </main>
</template>
