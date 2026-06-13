import './style.css';

const go = window.go && window.go.main && window.go.main.App;
const rt = window.runtime;

// Standard tuning, top to bottom as shown in the strings column: e B G D A E.
const STRINGS = [
    { name: 'E', oct: 4, freq: 329.63 },
    { name: 'B', oct: 3, freq: 246.94 },
    { name: 'G', oct: 3, freq: 196.00 },
    { name: 'D', oct: 3, freq: 146.83 },
    { name: 'A', oct: 2, freq: 110.00 },
    { name: 'E', oct: 2, freq: 82.41 },
];

const HIGHLIGHT_CENTS = 150; // how near a string must be to highlight it
const IN_TUNE_CENTS = 5;     // tolerance for the green "in tune" bloom
const CENTS_RANGE = 50;      // full-scale deflection of the tuning pane
const HOLD_MS = 250;         // keep showing the last pitch briefly to avoid flicker

// ---------- live state ----------
const state = {
    freq: 0,
    cents: 0,
    index: -1,
    wave: [],
    pointRate: 0,
    lastGoodAt: -1e9,
};

let frozen = null;          // { wave, pointRate, freq, pxPerPoint }
let currentCfg = null;      // active { deviceId, deviceName, channel }
let devicesCache = [];

// ---------- elements ----------
const el = (id) => document.getElementById(id);
const stringsEl = el('strings');
const tuningCanvas = el('tuningCanvas');
const scopeCanvas = el('scopeCanvas');
const tuningCtx = tuningCanvas.getContext('2d');
const scopeCtx = scopeCanvas.getContext('2d');

const rdNote = el('rdNote');
const rdCents = el('rdCents');
const rdFreq = el('rdFreq');
const frozenBadge = el('frozenBadge');
const freezeBtn = el('freezeBtn');
const inputBtn = el('inputBtn');
const inputLabel = el('inputLabel');
const inputDot = el('inputDot');

// ---------- build strings column ----------
const rowEls = STRINGS.map((s, i) => {
    const row = document.createElement('div');
    row.className = 'string-row';
    row.dataset.i = i;
    row.innerHTML =
        `<span class="letter">${s.name}</span>` +
        `<span class="freq">${s.freq.toFixed(2)}</span>`;
    stringsEl.appendChild(row);
    return row;
});

// ---------- pitch helpers ----------
function nearestString(freq) {
    let index = -1;
    let bestAbs = Infinity;
    let cents = 0;
    for (let i = 0; i < STRINGS.length; i++) {
        const c = 1200 * Math.log2(freq / STRINGS[i].freq);
        if (Math.abs(c) < bestAbs) {
            bestAbs = Math.abs(c);
            index = i;
            cents = c;
        }
    }
    return { index, cents, abs: bestAbs };
}

const NOTE_NAMES = ['C', 'C#', 'D', 'D#', 'E', 'F', 'F#', 'G', 'G#', 'A', 'A#', 'B'];

// nearestNote returns the closest equal-tempered note (A4 = 440 Hz) as a name,
// octave, and signed cents deviation — e.g. { name: 'C', octave: 4, cents: -3.2 }.
function nearestNote(freq) {
    const midi = 69 + 12 * Math.log2(freq / 440);
    const m = Math.round(midi);
    return {
        name: NOTE_NAMES[((m % 12) + 12) % 12],
        octave: Math.floor(m / 12) - 1,
        cents: (midi - m) * 100,
    };
}

// ---------- canvas sizing (HiDPI) ----------
const sizes = { tuning: { w: 0, h: 0 }, scope: { w: 0, h: 0 } };

function sizeCanvas(canvas, ctx, store) {
    const dpr = window.devicePixelRatio || 1;
    const w = canvas.clientWidth;
    const h = canvas.clientHeight;
    canvas.width = Math.max(1, Math.round(w * dpr));
    canvas.height = Math.max(1, Math.round(h * dpr));
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    store.w = w;
    store.h = h;
}

function resizeAll() {
    sizeCanvas(tuningCanvas, tuningCtx, sizes.tuning);
    sizeCanvas(scopeCanvas, scopeCtx, sizes.scope);
}

window.addEventListener('resize', resizeAll);
if (window.ResizeObserver) {
    const ro = new ResizeObserver(resizeAll);
    ro.observe(tuningCanvas);
    ro.observe(scopeCanvas);
}

// ---------- frame intake ----------
function onFrame(f) {
    if (f && f.has && f.wave && f.wave.length) {
        const { index, cents, abs } = nearestString(f.freq);
        state.freq = f.freq;
        state.cents = cents;
        state.index = abs <= HIGHLIGHT_CENTS ? index : -1;
        state.wave = f.wave;
        state.pointRate = f.pointRate;
        state.lastGoodAt = performance.now();
    }
}

// ---------- render loop ----------
function render(now) {
    const live = (now - state.lastGoodAt) < HOLD_MS;
    updateStrings(live);
    updateReadout(live);
    drawTuning(live);
    drawScope(live);
    requestAnimationFrame(render);
}

function updateStrings(live) {
    const activeIdx = live ? state.index : -1;
    const inTune = live && Math.abs(state.cents) <= IN_TUNE_CENTS;
    for (let i = 0; i < rowEls.length; i++) {
        const row = rowEls[i];
        const on = i === activeIdx;
        row.classList.toggle('active', on);
        row.classList.toggle('intune', on && inTune);
    }
}

function updateReadout(live) {
    if (live && state.freq > 0) {
        // Closest actual note in equal temperament (not limited to the strings).
        const n = nearestNote(state.freq);
        rdNote.textContent = `${n.name}${n.octave}`;
        rdNote.classList.remove('dim');
        rdFreq.textContent = `${state.freq.toFixed(1)} Hz`;

        const c = n.cents;
        const inTune = Math.abs(c) <= IN_TUNE_CENTS;
        rdCents.textContent = `${c >= 0 ? '+' : '−'}${Math.abs(c).toFixed(1)}¢`;
        rdCents.className = 'rd-cents ' + (inTune ? 'intune' : (c > 0 ? 'sharp' : 'flat'));
    } else {
        rdNote.textContent = '—';
        rdNote.classList.add('dim');
        rdCents.textContent = '';
        rdCents.className = 'rd-cents';
        rdFreq.textContent = 'no signal';
    }
}

// ---------- tuning pane ----------
function drawTuning(live) {
    const { w, h } = sizes.tuning;
    const ctx = tuningCtx;
    ctx.clearRect(0, 0, w, h);

    const cy = h / 2;
    const span = h * 0.42; // pixels for full-scale (±CENTS_RANGE)

    // faint scale ticks
    ctx.strokeStyle = 'rgba(255,255,255,0.05)';
    ctx.lineWidth = 1;
    for (const frac of [-1, -0.5, 0.5, 1]) {
        const y = cy - frac * span;
        ctx.beginPath();
        ctx.moveTo(w * 0.28, y);
        ctx.lineTo(w * 0.72, y);
        ctx.stroke();
    }

    const active = live && state.index >= 0;
    const inTune = active && Math.abs(state.cents) <= IN_TUNE_CENTS;

    // center "perfect tune" bar
    ctx.lineWidth = inTune ? 4 : 2.5;
    if (inTune) {
        ctx.strokeStyle = '#5df0a8';
        ctx.shadowColor = 'rgba(93,240,168,0.9)';
        ctx.shadowBlur = 22;
    } else {
        ctx.strokeStyle = 'rgba(255,255,255,0.18)';
        ctx.shadowBlur = 0;
    }
    ctx.beginPath();
    ctx.moveTo(w * 0.12, cy);
    ctx.lineTo(w * 0.88, cy);
    ctx.stroke();
    ctx.shadowBlur = 0;

    // deviation line
    if (active) {
        let c = state.cents;
        if (c > CENTS_RANGE) c = CENTS_RANGE;
        if (c < -CENTS_RANGE) c = -CENTS_RANGE;
        const y = cy - (c / CENTS_RANGE) * span;

        let color, glow;
        if (inTune) {
            color = '#5df0a8';
            glow = 'rgba(93,240,168,0.85)';
        } else {
            const t = Math.min(1, Math.abs(c) / CENTS_RANGE);
            const hue = 140 * (1 - t); // green -> red
            color = `hsl(${hue}, 85%, 58%)`;
            glow = `hsla(${hue}, 85%, 58%, 0.5)`;
        }
        ctx.strokeStyle = color;
        ctx.shadowColor = glow;
        ctx.shadowBlur = inTune ? 20 : 10;
        ctx.lineWidth = 3.5;
        ctx.beginPath();
        ctx.moveTo(w * 0.16, y);
        ctx.lineTo(w * 0.84, y);
        ctx.stroke();
        ctx.shadowBlur = 0;

        // small arrow caps hinting tune direction
        ctx.fillStyle = color;
        const dir = c > IN_TUNE_CENTS ? -1 : (c < -IN_TUNE_CENTS ? 1 : 0);
        if (dir !== 0) {
            const ax = w / 2;
            ctx.beginPath();
            ctx.moveTo(ax, y + dir * 9);
            ctx.lineTo(ax - 5, y + dir * 2);
            ctx.lineTo(ax + 5, y + dir * 2);
            ctx.closePath();
            ctx.fill();
        }
    }
}

// ---------- waveform pane ----------
function drawScope(live) {
    const { w, h } = sizes.scope;
    const ctx = scopeCtx;
    ctx.clearRect(0, 0, w, h);

    // Reserve a band at the top for the readout text so the waveform never
    // draws over it; centre the trace in the remaining area.
    const header = 52;
    const y0 = header + (h - header) / 2;
    const amp = ((h - header) / 2) * 0.86;

    // center axis
    ctx.strokeStyle = 'rgba(255,255,255,0.07)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, y0);
    ctx.lineTo(w, y0);
    ctx.stroke();

    // Scale: in normal mode the pane fits exactly four waves of the tracked
    // note. When frozen the horizontal scale is LOCKED to the frozen pixels-
    // per-second, so the live trace keeps the frozen note's time base — play
    // an octave up and it draws twice as densely over the same width.
    let pxPerPoint = 0;
    if (frozen) {
        // Lock pixels-per-second; convert to this frame's point spacing.
        const lockedPxPerSec = frozen.pxPerPoint * frozen.pointRate;
        if (state.pointRate > 0) pxPerPoint = lockedPxPerSec / state.pointRate;
    } else if (state.freq > 0 && state.pointRate > 0) {
        const pointsPerWave = state.pointRate / state.freq;
        pxPerPoint = w / (4 * pointsPerWave);
    }

    // frozen reference (dim, behind)
    if (frozen) {
        drawWave(ctx, frozen.wave, frozen.pxPerPoint, w, y0, amp, 'rgba(111,183,255,0.45)', 1.5, 0);
        drawStarts(ctx, frozen.wave, frozen.pxPerPoint, w, y0, 'rgba(111,183,255,0.5)');
    }

    // live trace (bright, on top)
    if (live && state.wave.length && pxPerPoint > 0) {
        drawWave(ctx, state.wave, pxPerPoint, w, y0, amp, '#4be39a', 2, 9);
        drawStarts(ctx, state.wave, pxPerPoint, w, y0, 'rgba(75,227,154,0.9)');
    }
}

function drawWave(ctx, wave, pxPerPoint, w, y0, amp, color, lineWidth, glow) {
    ctx.beginPath();
    let started = false;
    for (let i = 0; i < wave.length; i++) {
        const x = i * pxPerPoint;
        if (x > w) break;
        const y = y0 - wave[i] * amp;
        if (!started) {
            ctx.moveTo(x, y);
            started = true;
        } else {
            ctx.lineTo(x, y);
        }
    }
    ctx.strokeStyle = color;
    ctx.lineWidth = lineWidth;
    ctx.lineJoin = 'round';
    if (glow) {
        ctx.shadowColor = color;
        ctx.shadowBlur = glow;
    }
    ctx.stroke();
    ctx.shadowBlur = 0;
}

// vertical ticks at every positive-going zero crossing (start of each wave)
function drawStarts(ctx, wave, pxPerPoint, w, y0, color) {
    ctx.strokeStyle = color;
    ctx.lineWidth = 1.5;
    for (let i = 1; i < wave.length; i++) {
        if (wave[i - 1] <= 0 && wave[i] > 0) {
            const x = i * pxPerPoint;
            if (x > w) break;
            ctx.beginPath();
            ctx.moveTo(x, y0 - 28);
            ctx.lineTo(x, y0 + 28);
            ctx.stroke();
        }
    }
}

// ---------- freeze ----------
function toggleFreeze() {
    if (frozen) {
        frozen = null;
        freezeBtn.classList.remove('on');
        frozenBadge.hidden = true;
        return;
    }
    if (!state.wave.length || !state.freq || !state.pointRate) return;
    const pointsPerWave = state.pointRate / state.freq;
    const pxPerPoint = sizes.scope.w / (4 * pointsPerWave);
    frozen = {
        wave: state.wave.slice(),
        pointRate: state.pointRate,
        freq: state.freq,
        pxPerPoint,
    };
    freezeBtn.classList.add('on');
    frozenBadge.hidden = false;
}

freezeBtn.addEventListener('click', toggleFreeze);

// Spacebar toggles freeze (ignored while the input dialog is open).
window.addEventListener('keydown', (e) => {
    if (e.code === 'Space' && modal.hidden) {
        e.preventDefault(); // also suppresses activating a focused button
        toggleFreeze();
    }
});

// ---------- input device dialog ----------
const modal = el('modal');
const modalSub = el('modalSub');
const modalError = el('modalError');
const deviceList = el('deviceList');
const channelBlock = el('channelBlock');
const channelRow = el('channelRow');
const useBtn = el('useBtn');
const cancelBtn = el('cancelBtn');

let pickDeviceId = null;
let pickChannel = 0;

function setInputLabel(cfg) {
    if (cfg && cfg.deviceId) {
        inputLabel.textContent = `${cfg.deviceName} · Ch ${cfg.channel + 1}`;
        inputDot.classList.add('live');
    } else {
        inputLabel.textContent = 'No input';
        inputDot.classList.remove('live');
    }
}

function renderChannels(device) {
    channelRow.innerHTML = '';
    if (!device || device.channels <= 0) {
        channelBlock.hidden = true;
        return;
    }
    channelBlock.hidden = false;
    for (let ch = 0; ch < device.channels; ch++) {
        const chip = document.createElement('button');
        chip.className = 'chip' + (ch === pickChannel ? ' selected' : '');
        chip.textContent = `Input ${ch + 1}`;
        chip.addEventListener('click', () => {
            pickChannel = ch;
            renderChannels(device);
        });
        channelRow.appendChild(chip);
    }
}

function renderDevices() {
    deviceList.innerHTML = '';
    if (!devicesCache.length) {
        const empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = 'No input devices found. Connect an interface and reopen this dialog.';
        deviceList.appendChild(empty);
        channelBlock.hidden = true;
        useBtn.disabled = true;
        return;
    }
    for (const d of devicesCache) {
        const item = document.createElement('button');
        item.className = 'device' + (d.id === pickDeviceId ? ' selected' : '');
        const khz = d.sampleRate ? `${(d.sampleRate / 1000).toFixed(d.sampleRate % 1000 ? 1 : 0)} kHz` : '';
        item.innerHTML =
            `<div class="dev-name">${escapeHtml(d.name)}</div>` +
            `<div class="dev-meta">${d.channels} input${d.channels === 1 ? '' : 's'}${khz ? ' · ' + khz : ''}</div>`;
        item.addEventListener('click', () => {
            pickDeviceId = d.id;
            if (pickChannel >= d.channels) pickChannel = 0;
            renderDevices();
            renderChannels(d);
            useBtn.disabled = false;
        });
        deviceList.appendChild(item);
    }
}

function openDialog(cfg, message) {
    modalError.hidden = true;
    modalSub.textContent = message || 'Choose a sound input device and channel.';

    pickDeviceId = cfg && cfg.deviceId ? cfg.deviceId : null;
    pickChannel = cfg && cfg.channel ? cfg.channel : 0;

    const selected = devicesCache.find((d) => d.id === pickDeviceId);
    if (!selected) pickDeviceId = null;

    renderDevices();
    renderChannels(selected || null);
    useBtn.disabled = !pickDeviceId;
    modal.hidden = false;
}

async function refreshDevices() {
    try {
        devicesCache = (await go.Devices()) || [];
    } catch (e) {
        devicesCache = [];
    }
}

inputBtn.addEventListener('click', async () => {
    await refreshDevices();
    openDialog(currentCfg, '');
});

cancelBtn.addEventListener('click', () => {
    modal.hidden = true;
});

useBtn.addEventListener('click', async () => {
    if (!pickDeviceId) return;
    const dev = devicesCache.find((d) => d.id === pickDeviceId);
    if (!dev) return;
    useBtn.disabled = true;
    try {
        await go.SelectInput(dev.id, dev.name, pickChannel);
        currentCfg = { deviceId: dev.id, deviceName: dev.name, channel: pickChannel };
        setInputLabel(currentCfg);
        modal.hidden = true;
    } catch (e) {
        modalError.hidden = false;
        modalError.textContent = `Could not open input: ${e}`;
        useBtn.disabled = false;
    }
});

function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => (
        { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
    ));
}

// ---------- boot ----------
async function boot() {
    resizeAll();
    requestAnimationFrame(render);

    if (rt && rt.EventsOn) {
        rt.EventsOn('frame', onFrame);
        rt.EventsOn('input-lost', (msg) => {
            setInputLabel(null);
            openDialog(currentCfg, `Input device was lost: ${msg}`);
        });
    }

    if (!go) return; // not running under Wails

    try {
        const info = await go.Bootstrap();
        devicesCache = info.devices || [];
        if (info.hasConfig) currentCfg = info.config;

        if (info.capturing) {
            setInputLabel(info.config);
        } else {
            const msg = info.deviceMissing
                ? 'The saved input is unavailable — choose another.'
                : 'Choose a sound input device and channel.';
            openDialog(info.config, msg);
        }
    } catch (e) {
        openDialog(null, `Could not query audio devices: ${e}`);
    }
}

boot();
