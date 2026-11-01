import { Screen } from './term.48019be56a9f.js';
import { ViewerInput } from './viewer-input.0a90b146d03b.js';

const root = document.querySelector('#live-demo');
const words = JSON.parse(document.querySelector('#strings').textContent);
const t = (key) => words[key] ?? key;
const canvas = root.querySelector('canvas');
const viewport = root.querySelector('.demo-viewport');
const input = root.querySelector('.demo-input');
const loading = root.querySelector('.demo-loading');
const transcript = document.querySelector('#demo-transcript');
const live = root.dataset.mode === 'live';
const connection = root.querySelector('#collector-connect');
const editConnection = root.querySelector('#collector-edit');
const originHelp = root.querySelector('#collector-origin');
if (originHelp) originHelp.textContent = `--web-origin ${location.origin}`;
// The window title bar, shown on wide screens, follows the TUI's own title.
const windowTitle = root.querySelector('.live-title');
const windowSize = root.querySelector('.live-size');
const idleTitle = windowTitle?.textContent ?? '';
const cellAspect = Number(getComputedStyle(root).getPropertyValue('--demo-cell-aspect'));
const phone = matchMedia('(max-width: 600px)');
const cellWidth = () => phone.matches ? 7 : 8;
// On a phone the TUI lays itself out for the screen (topo --device fit):
// the page's own width in cells of its device layouts' shape, 7 px by 14,
// worked by a finger. Elsewhere it is the terminal, 80 columns or more.
const fitAspect = 2;
let fit = false;
const aspect = () => fit ? fitAspect : cellAspect;
const deviceKeys = () => [fit ? 'device:fit' : 'device:', `cell:${Math.round(aspect() * 1000)}:1000`];
let screen;
let worker;
let ready = false;
let closed = false;
let searching = false;
let pending = false;
let cols = 120;
let rows = 36;
let previous = 0;
let paintPending = false;
let composing = false;
let inputs = new ViewerInput();
let startupTimer;

function paint() {
  if (paintPending) return;
  paintPending = true;
  requestAnimationFrame(() => {
    paintPending = false;
    screen?.paint();
  });
}

function send(action, extra = {}) {
  pending = true;
  worker.postMessage({ action, mode: live ? 'live' : 'preview', reconnectHint: live ? t('connect.reconnect') : '', language: root.dataset.language, cols, rows, cellAspect: aspect(), ...extra });
}

function stop() {
  clearTimeout(startupTimer);
  worker?.terminate();
  worker = null;
  pending = false;
  inputs = new ViewerInput();
}

function quit() {
  stop();
  if (live) { showConnection(); return; }
  ready = false; closed = true;
  root.setAttribute('aria-busy', 'false');
  showOverlay(t('demo.ended'), true);
  loading.focus({ preventScroll: true });
}

function focusTerminal() {
  (searching ? input : canvas).focus({ preventScroll: true });
}

function key(value) {
  if (!ready || closed) return;
  if (value === 'ctrl-c') { quit(); return; }
  inputs.key(value);
}

function layout() {
  // Keep a readable cell size on phones. The terminal's layouts scroll
  // horizontally there instead of squeezing a 200-column screen into tiny
  // text; a fitted page is the viewport's own width.
  const cell = cellWidth();
  if (fit !== phone.matches) {
    fit = phone.matches;
    root.classList.toggle('is-fit', fit);
    // A running session turns with the page; start() tells a new one.
    for (const k of deviceKeys()) inputs.key(k);
  }
  const box = getComputedStyle(viewport);
  const innerW = viewport.clientWidth - parseFloat(box.paddingLeft) - parseFloat(box.paddingRight);
  const innerH = viewport.clientHeight - parseFloat(box.paddingTop) - parseFloat(box.paddingBottom);
  const width = fit ? innerW : Math.max(80 * cell, innerW);
  const height = Math.max(10 * cell * aspect(), innerH);
  const nextCols = Math.min(240, Math.floor(width / cell));
  const nextRows = Math.min(90, Math.max(10, Math.floor(height / (cell * aspect()))));
  if (cols !== nextCols || rows !== nextRows) {
    cols = nextCols; rows = nextRows;
    if (ready) inputs.resized = true;
  } else if (screen?.resize()) paint();
}

function showOverlay(message, canRestart = false) {
  loading.hidden = false;
  loading.textContent = message;
  loading.setAttribute('role', canRestart ? 'button' : 'status');
  loading.tabIndex = canRestart ? 0 : -1;
  loading.classList.toggle('can-restart', canRestart);
}

function restart() {
  // The loading overlay is also an immediate cancel control.
  if (!ready && worker) { quit(); return; }
  if (live) { showConnection(); return; }
  try { start(); } catch (error) { fail(error); }
}

function fail(error) {
  console.error('Topophany preview:', error);
  stop();
  ready = false;
  if (live) { showConnection(t('connect.failed')); return; }
  root.classList.remove('is-ready');
  root.setAttribute('aria-busy', 'false');
  showOverlay(t('demo.failed'), true);
  loading.focus({ preventScroll: true });
}

function showConnection(error = '') {
  stop();
  ready = false; closed = false; pending = false; searching = false;
  root.classList.remove('is-ready');
  root.setAttribute('aria-busy', 'false');
  loading.hidden = true;
  connection.hidden = false;
  canvas.setAttribute('aria-hidden', 'true');
  canvas.tabIndex = -1;
  input.disabled = true;
  if (editConnection) editConnection.hidden = true;
  if (error) connection.querySelector('.connect-help').open = true;
  connection.querySelector('[type="submit"]').disabled = false;
  connection.querySelector('#collector-error').textContent = error;
  connection.querySelector('#collector-token').value = '';
  transcript.textContent = '';
  if (windowTitle) { windowTitle.textContent = idleTitle; windowSize.textContent = ''; }
  screen = null;
  canvas.width = canvas.width;
  connection.querySelector('#collector-server').focus({ preventScroll: true });
}

function start(profile = {}) {
  stop();
  ready = false; closed = false; pending = false; searching = false;
  root.classList.remove('is-ready');
  root.setAttribute('aria-busy', 'true');
  if (connection) connection.hidden = true;
  canvas.removeAttribute('aria-hidden');
  canvas.tabIndex = 0;
  input.disabled = false;
  if (editConnection) editConnection.hidden = false;
  showOverlay(t('demo.loading'), true);
  worker = new Worker(root.dataset.worker, { type: 'module' });
  const current = worker;
  // Runs on the page so stalled downloads, compilation and a wedged runtime
  // can all be stopped, even when the worker cannot process another message.
  startupTimer = setTimeout(() => {
    if (worker === current) fail('viewer startup timed out');
  }, 30000);
  worker.onerror = (event) => { if (worker === current) fail(event.message); };
  worker.onmessage = ({ data }) => {
    if (worker !== current) return;
    pending = false;
    if (data.result) data = JSON.parse(data.result);
    if (data.error) { fail(data.error); return; }
    if (!data.frame) return;
    clearTimeout(startupTimer);
    const frame = data.frame;
    if (live && frame.closed) { showConnection(); return; }
    const cell = cellWidth();
    // Cells take the shape of the layout the frame was drawn in.
    const shape = frame.device ? fitAspect : cellAspect;
    canvas.style.width = `${frame.size[0] * cell}px`;
    canvas.style.height = `${frame.size[1] * cell * shape}px`;
    if (!screen) screen = new Screen(canvas, ...frame.size, [], shape);
    else if (screen.cellAspect !== shape) { screen.cellAspect = shape; screen.cw = 0; }
    screen.patch(frame);
    screen.resize();
    paint();
    canvas.setAttribute('aria-label', frame.title);
    if (windowTitle) { windowTitle.textContent = frame.title; windowSize.textContent = `${frame.size[0]}×${frame.size[1]}`; }
    if (frame.text) transcript.textContent = frame.text;
    searching = frame.searching;
    closed = frame.closed;
    document.title = `${t(live ? 'connect.title' : 'demo.title')} · Topophany`;
    const first = !ready;
    ready = true;
    root.classList.add('is-ready');
    root.setAttribute('aria-busy', 'false');
    if (closed) {
      showOverlay(t('demo.ended'), true);
      loading.focus({ preventScroll: true });
    } else {
      const refocus = first || document.activeElement === loading;
      loading.hidden = true;
      if (first) layout();
      if (refocus) focusTerminal();
    }
    if (searching && document.activeElement === canvas) input.focus({ preventScroll: true });
    else if (!searching && document.activeElement === input) canvas.focus({ preventScroll: true });
  };
  layout();
  send('init', { wasm: root.dataset.wasm, keys: fit ? ['device:fit'] : [], ...profile });
}

editConnection?.addEventListener('click', () => {
  showConnection();
  connection.querySelector('.connect-help').open = true;
});

connection?.addEventListener('submit', (event) => {
  event.preventDefault();
  const profile = {
    server: connection.querySelector('#collector-server').value.trim(),
    project: connection.querySelector('#collector-project').value.trim(),
    token: connection.querySelector('#collector-token').value.trim(),
  };
  connection.querySelector('#collector-token').value = '';
  try { start(profile); } catch (error) { fail(error); }
});

const special = {
  ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right',
  Enter: 'enter', Escape: 'esc', Backspace: 'backspace', Delete: 'delete',
  Home: 'home', End: 'end', PageUp: 'pgup', PageDown: 'pgdown',
};
function keyboard(event) {
  if (event.isComposing || composing || event.metaKey || event.altKey) return;
  // Tab remains available to leave the terminal. 1–9 select native tabs.
  if (event.key === 'Tab') return;
  let value = special[event.key];
  if (event.ctrlKey) {
    if (event.key.toLowerCase() === 'c') value = 'ctrl-c';
    else return;
  } else if (!value && [...event.key].length === 1) {
    value = event.key;
  }
  if (value) { event.preventDefault(); key(value); }
}
canvas.addEventListener('keydown', keyboard);
input.addEventListener('keydown', keyboard);
input.addEventListener('compositionstart', () => { composing = true; });
input.addEventListener('compositionend', (event) => {
  composing = false;
  if (event.data) key(`paste:${event.data}`);
  input.value = '';
});
input.addEventListener('beforeinput', (event) => {
  if (composing || event.isComposing || event.inputType === 'insertFromPaste') return;
  if (event.inputType === 'insertText' && event.data) {
    event.preventDefault();
    key([...event.data].length === 1 ? event.data : `paste:${event.data}`);
  }
});
input.addEventListener('input', () => { if (!composing) input.value = ''; });
for (const element of [canvas, input]) element.addEventListener('paste', (event) => {
  const text = event.clipboardData?.getData('text/plain');
  if (text) { event.preventDefault(); key(`paste:${text.slice(0, 8192)}`); }
});

// The cell under a pointer, counted from 1 as a terminal reports it.
function cellAt({ clientX, clientY }) {
  const rect = canvas.getBoundingClientRect();
  const [c, r] = screen ? [screen.cols, screen.rows] : [cols, rows];
  return [Math.floor((clientX - rect.left) / rect.width * c) + 1, Math.floor((clientY - rect.top) / rect.height * r) + 1];
}

canvas.addEventListener('click', (event) => {
  if (fit) return; // a fitted page reads presses and lifts instead
  const [x, y] = cellAt(event);
  key(`mouse:0:${x}:${y}:M`);
  focusTerminal();
});

// A finger on a fitted page, as topo's device simulator reads a mouse: a
// press and a lift on one cell is a tap, apart a swipe, and the wheel over
// the map a pinch. Two fingers pinch, a step for each quarter they spread.
const fingers = new Map();
let pinchFrom = 0;
const spread = () => {
  const [a, b] = fingers.values();
  return Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
};
canvas.addEventListener('pointerdown', (event) => {
  if (!fit || event.button > 0 || !event.isPrimary && fingers.size !== 1) return;
  fingers.set(event.pointerId, event);
  try { canvas.setPointerCapture(event.pointerId); } catch { /* a pointer already gone */ }
  if (fingers.size === 1) {
    const [x, y] = cellAt(event);
    key(`mouse:0:${x}:${y}:M`);
  } else pinchFrom = spread();
});
canvas.addEventListener('pointermove', (event) => {
  if (!fingers.has(event.pointerId)) return;
  fingers.set(event.pointerId, event);
  if (fingers.size !== 2 || !pinchFrom) return;
  const ratio = spread() / pinchFrom;
  if (ratio < 1.25 && ratio > 0.8) return;
  const [a, b] = fingers.values();
  const [x, y] = cellAt({ clientX: (a.clientX + b.clientX) / 2, clientY: (a.clientY + b.clientY) / 2 });
  key(`mouse:${ratio > 1 ? 64 : 65}:${x}:${y}:M`);
  pinchFrom = spread();
});
function lift(event) {
  if (!fingers.has(event.pointerId)) return;
  const pinched = fingers.size > 1 || pinchFrom > 0;
  fingers.delete(event.pointerId);
  if (!pinched && event.type === 'pointerup') {
    const [x, y] = cellAt(event);
    key(`mouse:0:${x}:${y}:m`);
  }
  if (!fingers.size) {
    pinchFrom = 0;
    focusTerminal();
  }
}
canvas.addEventListener('pointerup', lift);
canvas.addEventListener('pointercancel', lift);

let wheeled = 0;
canvas.addEventListener('wheel', (event) => {
  if (fit) {
    // A wheel over a fitted page pinches the map and scrolls the lists, a
    // step for each notch or each 40 px a trackpad moves.
    if (!ready || closed || event.ctrlKey) return;
    event.preventDefault();
    wheeled += event.deltaMode === 0 ? event.deltaY : event.deltaY * 40;
    if (Math.abs(wheeled) < 40) return;
    const [x, y] = cellAt(event);
    key(`mouse:${wheeled < 0 ? 64 : 65}:${x}:${y}:M`);
    wheeled = 0;
    return;
  }
  // Horizontal scrolling is useful on phones and small windows.
  if (!ready || closed || event.ctrlKey || Math.abs(event.deltaX) > Math.abs(event.deltaY)) return;
  event.preventDefault();
  key(event.deltaY < 0 ? 'wheel-up' : 'wheel-down');
}, { passive: false });

loading.addEventListener('click', () => {
  if (loading.classList.contains('can-restart')) restart();
});
loading.addEventListener('keydown', (event) => {
  if (loading.classList.contains('can-restart') && (event.key === 'Enter' || event.key === ' ')) {
    event.preventDefault();
    restart();
  }
});
new ResizeObserver(layout).observe(viewport);
document.addEventListener('visibilitychange', () => {
  if (!document.hidden && ready) inputs.resumed = true;
});
addEventListener('pagehide', () => { stop(); ready = false; });
addEventListener('pageshow', (event) => {
  if (event.persisted) { if (live) showConnection(); else restart(); }
});
document.fonts.ready.then(() => { if (screen) { screen.cw = 0; screen.resize(); paint(); } });

function tick(now) {
  requestAnimationFrame(tick);
  if (!ready || closed || pending || document.hidden || now - previous < 70) return;
  previous = now;
  const next = inputs.next();
  send(next.action, next);
}

// Startup and retry messages stay inside the terminal frame.
try { if (live) { layout(); showConnection(); } else start(); requestAnimationFrame(tick); } catch (error) { fail(error); }
