package main

import (
	"context"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	// levelGate is the minimum RMS for the signal to be considered present.
	levelGate = 0.004
	// confGate is the minimum YIN confidence (1 - aperiodicity) to accept a
	// pitch estimate.
	confGate = 0.45
	// frameInterval is the analysis/emit cadence (~60 fps).
	frameInterval = 16 * time.Millisecond
)

// Frame is one analysis result pushed to the frontend via the "frame" event.
type Frame struct {
	Has       bool      `json:"has"`       // a pitch was detected this frame
	Freq      float64   `json:"freq"`      // detected fundamental, Hz
	Conf      float64   `json:"conf"`      // 0..1 confidence
	Level     float64   `json:"level"`     // RMS level
	Wave      []float64 `json:"wave"`      // oscilloscope display points
	PointRate float64   `json:"pointRate"` // points per second in Wave
}

// BootstrapInfo is returned to the frontend on launch so it can decide whether
// to open the input-selection dialog.
type BootstrapInfo struct {
	Devices       []DeviceInfo `json:"devices"`
	Config        Config       `json:"config"`
	HasConfig     bool         `json:"hasConfig"`
	Capturing     bool         `json:"capturing"`
	DeviceMissing bool         `json:"deviceMissing"`
	Error         string       `json:"error"`
}

// App is the Wails-bound application object.
type App struct {
	ctx context.Context

	mu          sync.Mutex
	engine      *captureEngine
	detector    *pitchDetector
	wave        *waveProcessor
	cfg         Config
	hasCfg      bool
	lastFreq    float64
	analysisBuf []float32

	stopLoop chan struct{}
}

func NewApp() *App {
	return &App{}
}

// startup is called by Wails once the runtime is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.stopLoop = make(chan struct{})
	go a.analysisLoop()

	if cfg, ok := loadConfig(); ok {
		a.mu.Lock()
		a.cfg = cfg
		a.hasCfg = true
		a.mu.Unlock()
		// Best effort: if the saved device is gone, Bootstrap reports it and
		// the frontend opens the picker.
		_ = a.applyInput(cfg)
	}
}

// shutdown stops the analysis loop and releases the audio device.
func (a *App) shutdown(ctx context.Context) {
	if a.stopLoop != nil {
		close(a.stopLoop)
	}
	a.mu.Lock()
	eng := a.engine
	a.engine = nil
	a.mu.Unlock()
	if eng != nil {
		eng.Stop()
	}
}

// Bootstrap returns the initial state the frontend needs on load.
func (a *App) Bootstrap() BootstrapInfo {
	devices, derr := enumerateDevices()

	a.mu.Lock()
	cfg := a.cfg
	hasCfg := a.hasCfg
	capturing := a.engine != nil
	a.mu.Unlock()

	info := BootstrapInfo{
		Devices:   devices,
		Config:    cfg,
		HasConfig: hasCfg,
		Capturing: capturing,
	}
	if derr != nil {
		info.Error = derr.Error()
	}
	if hasCfg && !capturing {
		info.DeviceMissing = true
	}
	return info
}

// Devices lists the currently available capture endpoints.
func (a *App) Devices() ([]DeviceInfo, error) {
	return enumerateDevices()
}

// SelectInput switches to the given device/channel, starts capturing, and
// persists the selection. Returns an error if the device cannot be opened.
func (a *App) SelectInput(deviceID, deviceName string, channel int) error {
	cfg := Config{DeviceID: deviceID, DeviceName: deviceName, Channel: channel}
	if err := a.applyInput(cfg); err != nil {
		return err
	}
	return saveConfig(cfg)
}

// applyInput opens the device and swaps it in as the active capture engine.
func (a *App) applyInput(cfg Config) error {
	eng, err := startCapture(cfg.DeviceID, cfg.Channel, a.onCaptureError)
	if err != nil {
		return err
	}
	sr := eng.SampleRate
	det := newPitchDetector(sr)
	wp := newWaveProcessor(sr)

	// Enough samples for the displayed window plus the filter warm-up and the
	// trigger search span.
	readN := int((filterWarmupSeconds+displaySeconds)*float64(sr)) + int(float64(sr)/pitchFloorHz) + 1
	if m := det.minInput(); m > readN {
		readN = m
	}

	a.mu.Lock()
	old := a.engine
	a.engine = eng
	a.detector = det
	a.wave = wp
	a.analysisBuf = make([]float32, readN)
	a.cfg = cfg
	a.hasCfg = true
	a.lastFreq = 0
	a.mu.Unlock()

	if old != nil {
		old.Stop()
	}
	return nil
}

// onCaptureError is invoked when a running stream dies (e.g. device unplugged).
func (a *App) onCaptureError(err error) {
	a.mu.Lock()
	a.engine = nil
	a.mu.Unlock()
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, "input-lost", err.Error())
	}
}

func (a *App) analysisLoop() {
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopLoop:
			return
		case <-ticker.C:
		}

		a.mu.Lock()
		eng := a.engine
		det := a.detector
		wp := a.wave
		buf := a.analysisBuf
		last := a.lastFreq
		a.mu.Unlock()

		if eng == nil || det == nil || wp == nil || buf == nil {
			continue
		}
		if !eng.ring.latest(buf) {
			continue
		}

		level := rms(buf)
		frame := Frame{Level: level}
		if level >= levelGate {
			if freq, conf, ok := det.detect(buf); ok && conf >= confGate {
				sm := smoothFreq(last, freq)
				wave, pr := wp.build(buf, sm)
				frame.Has = true
				frame.Freq = sm
				frame.Conf = conf
				frame.Wave = wave
				frame.PointRate = pr
				a.mu.Lock()
				a.lastFreq = sm
				a.mu.Unlock()
			}
		}
		if a.ctx != nil {
			wruntime.EventsEmit(a.ctx, "frame", frame)
		}
	}
}

// smoothFreq lightly low-pass filters the pitch while it stays on the same
// note (within ~6%), but snaps immediately on a larger jump so changing
// strings is responsive.
func smoothFreq(last, cur float64) float64 {
	if last > 0 {
		r := cur / last
		if r < 1 {
			r = 1 / r
		}
		if r < 1.06 {
			return last*0.75 + cur*0.25
		}
	}
	return cur
}
