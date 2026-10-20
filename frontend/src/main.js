import './style.css'
import {
  CancelProcessing,
  GetDefaults,
  GeneratePreview,
  InspectInputs,
  LoadConfig,
  OpenOutputDirectory,
  Process,
  SaveConfig,
  SelectFiles,
  SelectFolder,
  SelectFont
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'

const state = {
  paths: [],
  running: false,
  previewing: false,
  previewTimer: null,
  previewRequest: 0,
  saveTimer: null,
  defaults: null
}

document.querySelector('#app').innerHTML = `
  <main class="app-shell">
    <header class="hero">
      <div>
        <h1>图片水印工具</h1>
        <p>图片仅在本机处理。拖入图片或文件夹，配置水印后批量输出。</p>
      </div>
      <div id="magickStatus" class="status-pill">检查 ImageMagick…</div>
    </header>

    <section class="workspace">
      <div class="left-column">
        <section id="dropZone" class="drop-zone" style="--wails-drop-target: drop">
          <div class="drop-icon" aria-hidden="true">
            <svg viewBox="0 0 48 48"><path d="M24 5v25m0-25-9 9m9-9 9 9M9 27v11a5 5 0 0 0 5 5h20a5 5 0 0 0 5-5V27"/></svg>
          </div>
          <h2>拖入图片或文件夹</h2>
          <p>支持 JPG、PNG、WebP、BMP、TIFF、HEIC、PDF（逐页输出PNG）</p>
          <div class="drop-actions">
            <button id="chooseFiles" class="secondary-button">选择图片</button>
            <button id="chooseFolder" class="secondary-button">选择文件夹</button>
          </div>
        </section>

        <section class="input-card">
          <div class="section-heading">
            <div>
              <span class="step">01</span>
              <h2>待处理内容</h2>
            </div>
            <button id="clearInputs" class="text-button">清空</button>
          </div>
          <div id="inputSummary" class="input-summary empty">尚未添加图片或文件夹</div>
          <div id="pathList" class="path-list"></div>
        </section>

        <section class="preview-card">
          <div class="section-heading compact">
            <div>
              <span class="step">03</span>
              <h2>效果预览</h2>
            </div>
            <span id="previewStatus" class="preview-status">等待图片</span>
          </div>
          <div id="previewStage" class="preview-stage empty">
            <div id="previewPlaceholder" class="preview-placeholder">
              <svg viewBox="0 0 48 48"><path d="M8 10h32v28H8zM13 32l8-9 6 6 4-4 5 7M18 18h.01"/></svg>
              <span>添加图片后自动生成预览</span>
            </div>
            <img id="previewImage" class="hidden" alt="水印效果预览">
            <div id="previewLoader" class="preview-loader hidden"><i></i><span>正在更新预览…</span></div>
          </div>
          <div id="previewMeta" class="preview-meta">预览使用第一张图片或PDF第一页，正式导出仍保留最高精度。</div>
        </section>

        <section class="output-card">
          <div class="section-heading compact">
            <div>
              <span class="step">04</span>
              <h2>输出位置</h2>
            </div>
            <button id="openOutput" class="text-button">打开文件夹</button>
          </div>
          <code id="outputPath">/Users/Admin/Desktop/ImageMagick/output</code>
          <p>不会覆盖原图；重名文件会自动添加序号。</p>
        </section>
      </div>

      <section class="config-card">
        <div class="section-heading">
          <div>
            <span class="step">02</span>
            <h2>水印配置</h2>
          </div>
          <button id="resetConfig" class="text-button">恢复默认</button>
        </div>

        <label class="field full-width">
          <span>水印文字</span>
          <textarea id="text" rows="3" maxlength="160">仅供共比邻小程序酒店资质展示使用 严禁他用</textarea>
        </label>

        <div class="field-grid">
          <label class="field">
            <span>水印模式</span>
            <select id="mode">
              <option value="tile">满屏平铺</option>
              <option value="center">居中单个</option>
            </select>
          </label>
          <label class="field">
            <span>输出格式</span>
            <select id="outputFormat">
              <option value="png">PNG（推荐）</option>
              <option value="jpg">JPEG</option>
              <option value="webp">WebP</option>
            </select>
          </label>
          <label class="field">
            <span>字号 <b id="fontSizeValue">155</b></span>
            <input id="fontSize" type="range" min="16" max="240" value="155">
          </label>
          <label class="field">
            <span>透明度 <b id="opacityValue">22%</b></span>
            <input id="opacity" type="range" min="5" max="80" value="22">
          </label>
          <label class="field">
            <span>倾斜角度 <b id="angleValue">-30°</b></span>
            <input id="angle" type="range" min="-90" max="90" value="-30">
          </label>
          <label class="field color-field">
            <span>水印颜色</span>
            <div class="color-control">
              <input id="color" type="color" value="#555555">
              <input id="colorText" type="text" value="#555555" maxlength="7">
            </div>
          </label>
          <label class="field tile-setting">
            <span>同一行文字间隔</span>
            <input id="tileWidth" type="number" min="20" max="3000" value="80">
          </label>
          <label class="field tile-setting">
            <span>上下行间隔</span>
            <input id="tileHeight" type="number" min="20" max="2000" value="280">
          </label>
          <label class="field">
            <span>原图先旋转</span>
            <select id="rotateOutput">
              <option value="0">不旋转</option>
              <option value="-90">向左旋转1次（90°）</option>
              <option value="-180">向左旋转2次（180°）</option>
              <option value="-270">向左旋转3次（270°）</option>
              <option value="90">向右旋转1次（90°）</option>
              <option value="180">向右旋转2次（180°）</option>
              <option value="270">向右旋转3次（270°）</option>
            </select>
          </label>
          <label class="field jpeg-setting hidden">
            <span>JPEG质量</span>
            <input id="jpegQuality" type="number" min="1" max="100" value="95">
          </label>
        </div>

        <label class="field full-width">
          <span>中文字体</span>
          <div class="inline-control">
            <input id="fontPath" type="text" value="/System/Library/Fonts/Hiragino Sans GB.ttc">
            <button id="chooseFont" class="small-button">选择</button>
          </div>
        </label>

        <div class="switch-grid">
          <label class="switch-row">
            <input id="stripMetadata" type="checkbox" checked>
            <span class="switch"></span>
            <span><b>删除元数据</b><small>移除 EXIF、GPS、拍摄设备等信息</small></span>
          </label>
          <label class="switch-row">
            <input id="recursive" type="checkbox" checked>
            <span class="switch"></span>
            <span><b>递归子文件夹</b><small>处理文件夹中的所有下级目录</small></span>
          </label>
        </div>

        <div class="run-panel">
          <div id="progressBlock" class="progress-block hidden">
            <div class="progress-meta">
              <span id="progressLabel">准备处理</span>
              <span id="progressValue">0%</span>
            </div>
            <div class="progress-track"><div id="progressBar"></div></div>
            <small id="progressDetail"></small>
          </div>
          <div class="run-actions">
            <button id="cancelButton" class="cancel-button hidden">取消</button>
            <button id="processButton" class="primary-button" disabled>开始处理</button>
          </div>
          <div id="resultMessage" class="result-message"></div>
        </div>
      </section>
    </section>
  </main>
`

const $ = (id) => document.getElementById(id)
const defaultConfig = {
  text: '仅供共比邻小程序酒店资质展示使用 严禁他用',
  fontSize: 155,
  opacity: 22,
  angle: -30,
  color: '#555555',
  mode: 'tile',
  tileWidth: 80,
  tileHeight: 280,
  rotateOutput: 0,
  outputFormat: 'png',
  jpegQuality: 95,
  stripMetadata: true,
  recursive: true
}

function applyConfig(config) {
  $('text').value = config.text
  $('fontSize').value = config.fontSize
  $('opacity').value = Math.round(config.opacity * 100)
  $('angle').value = config.angle
  $('color').value = config.color
  $('colorText').value = config.color
  $('mode').value = config.mode
  $('tileWidth').value = config.tileWidth
  $('tileHeight').value = config.tileHeight
  $('rotateOutput').value = config.rotateOutput
  $('outputFormat').value = config.outputFormat
  $('jpegQuality').value = config.jpegQuality
  $('stripMetadata').checked = config.stripMetadata
  $('recursive').checked = config.recursive
  if (config.fontPath) $('fontPath').value = config.fontPath
  else if (state.defaults?.fontPath) $('fontPath').value = state.defaults.fontPath
  syncControls()
  refreshInputSummary()
}

function setDefaultConfig() {
  applyConfig({ ...defaultConfig, opacity: defaultConfig.opacity / 100 })
}

function saveConfigDebounced() {
  clearTimeout(state.saveTimer)
  state.saveTimer = setTimeout(() => {
    SaveConfig(getConfig()).catch(() => {})
  }, 400)
}

function getConfig() {
  return {
    text: $('text').value.trim(),
    fontPath: $('fontPath').value.trim(),
    fontSize: Number($('fontSize').value),
    opacity: Number($('opacity').value) / 100,
    angle: Number($('angle').value),
    color: $('colorText').value.trim(),
    mode: $('mode').value,
    tileWidth: Number($('tileWidth').value),
    tileHeight: Number($('tileHeight').value),
    rotateOutput: Number($('rotateOutput').value),
    outputFormat: $('outputFormat').value,
    jpegQuality: Number($('jpegQuality').value),
    stripMetadata: $('stripMetadata').checked,
    recursive: $('recursive').checked
  }
}

function syncControls() {
  $('fontSizeValue').textContent = $('fontSize').value
  $('opacityValue').textContent = `${$('opacity').value}%`
  $('angleValue').textContent = `${$('angle').value}°`
  document.querySelectorAll('.tile-setting').forEach((element) => {
    element.classList.toggle('hidden', $('mode').value !== 'tile')
  })
  document.querySelectorAll('.jpeg-setting').forEach((element) => {
    element.classList.toggle('hidden', $('outputFormat').value !== 'jpg')
  })
}

function basename(path) {
  return path.replaceAll('\\', '/').split('/').filter(Boolean).pop() || path
}

async function addPaths(paths) {
  if (!paths?.length || state.running) return
  state.paths = [...new Set([...state.paths, ...paths])]
  await refreshInputSummary()
  schedulePreview(80)
}

async function refreshInputSummary() {
  const summary = $('inputSummary')
  const list = $('pathList')
  list.innerHTML = ''
  if (!state.paths.length) {
    summary.className = 'input-summary empty'
    summary.textContent = '尚未添加图片或文件夹'
    $('processButton').disabled = true
    return
  }

  try {
    const info = await InspectInputs(state.paths, $('recursive').checked)
    summary.className = 'input-summary'
    summary.innerHTML = `<strong>${info.imageCount}</strong> 张支持的图片${info.skipped ? ` · 跳过 ${info.skipped} 项` : ''}`
    $('processButton').disabled = info.imageCount === 0 || state.running || !state.defaults?.magickInstalled
  } catch (error) {
    summary.className = 'input-summary error'
    summary.textContent = String(error)
    $('processButton').disabled = true
  }

  state.paths.forEach((path, index) => {
    const row = document.createElement('div')
    row.className = 'path-row'
    row.innerHTML = `<span title="${escapeHtml(path)}">${escapeHtml(basename(path))}</span><button aria-label="移除">×</button>`
    row.querySelector('button').addEventListener('click', async () => {
      state.paths.splice(index, 1)
      await refreshInputSummary()
      if (state.paths.length) schedulePreview(80)
      else clearPreview()
    })
    list.appendChild(row)
  })
}

function escapeHtml(value) {
  return String(value).replace(/[&<>'"]/g, (char) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'
  }[char]))
}

function clearPreview() {
  clearTimeout(state.previewTimer)
  state.previewRequest += 1
  state.previewing = false
  $('previewImage').classList.add('hidden')
  $('previewImage').removeAttribute('src')
  $('previewLoader').classList.add('hidden')
  $('previewPlaceholder').classList.remove('hidden')
  $('previewStage').classList.add('empty')
  $('previewStatus').className = 'preview-status'
  $('previewStatus').textContent = '等待图片'
  $('previewMeta').textContent = '预览使用第一张图片或PDF第一页，正式导出仍保留最高精度。'
}

function schedulePreview(delay = 260) {
  clearTimeout(state.previewTimer)
  if (!state.paths.length || state.running || !state.defaults?.magickInstalled) {
    if (!state.paths.length) clearPreview()
    return
  }
  state.previewTimer = setTimeout(generatePreview, delay)
}

async function generatePreview() {
  if (!state.paths.length || state.running) return
  const request = ++state.previewRequest
  state.previewing = true
  $('previewLoader').classList.remove('hidden')
  $('previewStatus').className = 'preview-status loading'
  $('previewStatus').textContent = '更新中'
  try {
    const result = await GeneratePreview(state.paths, getConfig())
    if (request !== state.previewRequest) return
    $('previewImage').src = result.dataURL
    $('previewImage').classList.remove('hidden')
    $('previewPlaceholder').classList.add('hidden')
    $('previewStage').classList.remove('empty')
    $('previewStatus').className = 'preview-status ready'
    $('previewStatus').textContent = '实时预览'
    $('previewMeta').textContent = `${result.file} · 预览 ${result.width} × ${result.height} · 正式导出使用原始最高精度`
  } catch (error) {
    if (request !== state.previewRequest || String(error).includes('预览已更新')) return
    $('previewStatus').className = 'preview-status error'
    $('previewStatus').textContent = '预览失败'
    $('previewMeta').textContent = String(error)
  } finally {
    if (request === state.previewRequest) {
      state.previewing = false
      $('previewLoader').classList.add('hidden')
    }
  }
}

function configChanged() {
  syncControls()
  saveConfigDebounced()
  schedulePreview()
}

function setRunning(running) {
  state.running = running
  $('processButton').disabled = running || !state.paths.length || !state.defaults?.magickInstalled
  $('processButton').textContent = running ? '处理中…' : '开始处理'
  $('cancelButton').classList.toggle('hidden', !running)
  $('progressBlock').classList.toggle('hidden', !running)
  document.querySelectorAll('input, textarea, select, button').forEach((element) => {
    if (['cancelButton', 'openOutput'].includes(element.id)) return
    if (element.id === 'processButton') return
    element.disabled = running
  })
  if (!running) {
    refreshInputSummary()
    schedulePreview(80)
  }
}

async function startProcessing() {
  if (!state.paths.length || state.running) return
  $('resultMessage').textContent = ''
  $('progressBar').style.width = '0%'
  $('progressValue').textContent = '0%'
  $('progressDetail').textContent = ''
  setRunning(true)
  try {
    const result = await Process(state.paths, getConfig())
    if (result.cancelled) {
      $('resultMessage').className = 'result-message warning'
      $('resultMessage').textContent = `已取消。成功 ${result.success} 张，失败 ${result.failed} 张。`
    } else if (result.failed) {
      $('resultMessage').className = 'result-message warning'
      const details = result.errors?.slice(0, 3).join('；') || ''
      $('resultMessage').textContent = `完成：成功 ${result.success} 张，失败 ${result.failed} 张。${details}`
    } else {
      $('resultMessage').className = 'result-message success'
      $('resultMessage').textContent = `处理完成，共输出 ${result.success} 张图片。`
    }
  } catch (error) {
    $('resultMessage').className = 'result-message error'
    $('resultMessage').textContent = String(error)
  } finally {
    setRunning(false)
  }
}

function bindEvents() {
  EventsOn('inputs:dropped', addPaths)
  EventsOn('process:progress', (progress) => {
    const percentage = progress.total ? Math.round((progress.current / progress.total) * 100) : 0
    $('progressBar').style.width = `${percentage}%`
    $('progressValue').textContent = `${percentage}%`
    $('progressLabel').textContent = progress.message
    $('progressDetail').textContent = `${progress.current}/${progress.total} · ${progress.file} · 成功 ${progress.success} · 失败 ${progress.failed}`
  })

  $('chooseFiles').addEventListener('click', async () => addPaths(await SelectFiles()))
  $('chooseFolder').addEventListener('click', async () => addPaths(await SelectFolder()))
  $('chooseFont').addEventListener('click', async () => {
    const path = await SelectFont()
    if (path) {
      $('fontPath').value = path
      schedulePreview(80)
    }
  })
  $('clearInputs').addEventListener('click', async () => {
    state.paths = []
    await refreshInputSummary()
    clearPreview()
  })
  $('resetConfig').addEventListener('click', () => {
    setDefaultConfig()
    saveConfigDebounced()
    schedulePreview(80)
  })
  $('openOutput').addEventListener('click', () => OpenOutputDirectory())
  $('processButton').addEventListener('click', startProcessing)
  $('cancelButton').addEventListener('click', () => CancelProcessing())
  $('recursive').addEventListener('change', async () => {
    saveConfigDebounced()
    await refreshInputSummary()
    schedulePreview(80)
  })

  const instantControls = ['fontSize', 'opacity', 'angle']
  instantControls.forEach((id) => $(id).addEventListener('input', configChanged))
  const changeControls = ['text', 'mode', 'outputFormat', 'tileWidth', 'tileHeight', 'rotateOutput', 'jpegQuality', 'fontPath', 'stripMetadata']
  changeControls.forEach((id) => {
    $(id).addEventListener(id === 'text' || id === 'fontPath' || id.startsWith('tile') ? 'input' : 'change', configChanged)
  })

  $('color').addEventListener('input', () => {
    $('colorText').value = $('color').value.toUpperCase()
    saveConfigDebounced()
    schedulePreview()
  })
  $('colorText').addEventListener('input', () => {
    if (/^#[0-9a-f]{6}$/i.test($('colorText').value)) {
      $('color').value = $('colorText').value
      saveConfigDebounced()
      schedulePreview()
    }
  })

  window.addEventListener('dragover', (event) => event.preventDefault())
  window.addEventListener('drop', (event) => event.preventDefault())
}

async function init() {
  bindEvents()
  state.defaults = await GetDefaults()
  $('outputPath').textContent = state.defaults.outputDirectory
  $('fontPath').value = state.defaults.fontPath
  const status = $('magickStatus')
  if (state.defaults.magickInstalled) {
    status.className = 'status-pill ready'
    status.textContent = 'ImageMagick 已就绪'
  } else {
    status.className = 'status-pill error'
    status.textContent = '未安装 ImageMagick'
  }
  setDefaultConfig()
  try {
    const saved = await LoadConfig()
    if (saved && saved.text && saved.fontSize > 0) {
      applyConfig(saved)
    }
  } catch {
    // 无已保存配置（首次使用）时保持默认值
  }
}

init().catch((error) => {
  $('resultMessage').className = 'result-message error'
  $('resultMessage').textContent = `应用初始化失败：${error}`
})
