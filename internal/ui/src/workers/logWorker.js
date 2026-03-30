// Log rendering worker — OffscreenCanvas path
// Receives batched log lines from the main thread and draws them
// directly to an OffscreenCanvas at 60fps without blocking the UI.

let canvas = null
let ctx    = null
let dpr    = 1
let logicalW = 0
let logicalH = 0

const ALL     = []   // { text, timeStr, level }
let   VISIBLE = []   // filtered/all — pointer or filtered copy
let   filterLower = ''
let   scrollTop   = 0

// Layout constants (logical px — multiplied by dpr for canvas coords)
const LINE_H  = 19
const PAD_L   = 10
const TIME_W  = 74   // reserved width for timestamp column
const FS      = 11   // font size

// Colours matching index.css tokens exactly
const C = {
  bg:          '#0d1117',
  bgHover:     'rgba(255,255,255,0.035)',
  text:        '#e6edf3',
  time:        '#6e7681',
  error:       '#ff7b72',
  warn:        '#e3b341',
  debug:       '#79c0ff',
  borderError: '#f85149',
  borderWarn:  '#d29922',
}

function classify(t) {
  const l = t.toLowerCase()
  if (l.includes('error') || l.includes('fatal') || l.includes('panic') || l.includes('critical')) return 'error'
  if (l.includes('warn'))                                                                           return 'warn'
  if (l.includes('debug') || l.includes('trace'))                                                  return 'debug'
  return ''
}

function refilter() {
  VISIBLE = filterLower
    ? ALL.filter(l => l.text.toLowerCase().includes(filterLower))
    : ALL
}

function totalH() { return VISIBLE.length * LINE_H }

function clampScroll() {
  const max = Math.max(0, totalH() - logicalH)
  if (scrollTop > max) scrollTop = max
  if (scrollTop < 0)   scrollTop = 0
}

function draw() {
  if (!ctx) return

  // Fill background
  ctx.fillStyle = C.bg
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  if (VISIBLE.length === 0) {
    postMetrics()
    return
  }

  ctx.textBaseline = 'middle'
  ctx.textAlign    = 'left'
  ctx.font         = `${FS * dpr}px monospace`

  const first = Math.max(0, Math.floor(scrollTop / LINE_H) - 1)
  const last  = Math.min(VISIBLE.length - 1, Math.ceil((scrollTop + logicalH) / LINE_H) + 1)

  for (let i = first; i <= last; i++) {
    const line = VISIBLE[i]
    if (!line) continue

    const y   = (i * LINE_H - scrollTop) * dpr   // canvas-pixel top of row
    const mid = y + (LINE_H * dpr) / 2            // vertical midpoint

    // Coloured left border for error/warn rows
    if (line.level === 'error') {
      ctx.fillStyle = C.borderError
      ctx.fillRect(0, y, 2 * dpr, LINE_H * dpr)
    } else if (line.level === 'warn') {
      ctx.fillStyle = C.borderWarn
      ctx.fillRect(0, y, 2 * dpr, LINE_H * dpr)
    }

    // Timestamp
    ctx.fillStyle = C.time
    ctx.fillText(line.timeStr, (PAD_L + 4) * dpr, mid)

    // Log text
    ctx.fillStyle = line.level === 'error' ? C.error
                  : line.level === 'warn'  ? C.warn
                  : line.level === 'debug' ? C.debug
                  : C.text
    ctx.fillText(line.text, (PAD_L + TIME_W) * dpr, mid)
  }

  postMetrics()
}

function postMetrics() {
  self.postMessage({
    type:         'metrics',
    totalHeight:  totalH(),
    lineCount:    ALL.length,
    visibleCount: VISIBLE.length,
    scrollTop,
  })
}

self.onmessage = ({ data }) => {
  switch (data.type) {

    case 'init': {
      canvas   = data.canvas
      dpr      = data.dpr || 1
      logicalW = data.width
      logicalH = data.height
      canvas.width  = Math.floor(logicalW * dpr)
      canvas.height = Math.floor(logicalH * dpr)
      ctx = canvas.getContext('2d')
      draw()
      break
    }

    case 'resize': {
      logicalW = data.width
      logicalH = data.height
      canvas.width  = Math.floor(logicalW * dpr)
      canvas.height = Math.floor(logicalH * dpr)
      clampScroll()
      draw()
      break
    }

    case 'lines': {
      for (const line of data.lines) {
        ALL.push({ ...line, level: classify(line.text) })
      }
      refilter()
      draw()
      self.postMessage({ type: 'count', total: ALL.length, visible: VISIBLE.length })
      break
    }

    case 'clear': {
      ALL.length = 0
      VISIBLE    = ALL
      scrollTop  = 0
      filterLower = ''
      draw()
      self.postMessage({ type: 'count', total: 0, visible: 0 })
      break
    }

    case 'scroll': {
      scrollTop = data.scrollTop
      clampScroll()
      draw()
      break
    }

    case 'filter': {
      filterLower = data.text.toLowerCase()
      refilter()
      scrollTop = 0
      draw()
      self.postMessage({ type: 'count', total: ALL.length, visible: VISIBLE.length })
      break
    }

    case 'getLines': {
      const text = ALL.map(l => `${l.timeStr}  ${l.text}`).join('\n')
      self.postMessage({ type: 'allLines', text })
      break
    }
  }
}
