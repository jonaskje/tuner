package main

import "math"

const (
	// displaySeconds is the fixed time span of the captured waveform window.
	// Keeping it constant (rather than a function of the detected pitch) gives
	// the freeze feature a stable time base to compare against. 100 ms covers
	// four full periods down to 40 Hz.
	displaySeconds = 0.10
	// targetPoints is the approximate number of points sent per frame. The
	// frontend scales these horizontally so that four waves fill the pane.
	targetPoints = 1200
	// filterWarmupSeconds of extra signal is captured ahead of the displayed
	// window so the band-pass filter is fully settled before the region we
	// actually show (the IIR has a transient at the start of the buffer).
	filterWarmupSeconds = 0.25
	// bandpassQ controls the filter bandwidth (centre/Q). Two cascaded
	// sections at this Q push the 2nd harmonic down by ~35 dB, so a plucked
	// string is reduced to essentially its fundamental — a clean sine.
	bandpassQ = 5.0
)

// biquad is a direct-form-I second-order IIR section.
type biquad struct {
	b0, b1, b2, a1, a2 float64
}

// bandpass builds an RBJ constant-0 dB-peak band-pass centred at f0.
// At f0 the section has unity gain and zero phase, so the displayed
// waveform's zero crossings stay aligned with the real signal.
func bandpass(f0, fs, q float64) biquad {
	w0 := 2 * math.Pi * f0 / fs
	cosw := math.Cos(w0)
	alpha := math.Sin(w0) / (2 * q)
	a0 := 1 + alpha
	return biquad{
		b0: alpha / a0,
		b1: 0,
		b2: -alpha / a0,
		a1: -2 * cosw / a0,
		a2: (1 - alpha) / a0,
	}
}

// process filters s in place. Previous input/output are kept in locals so
// in-place operation is safe.
func (bq biquad) process(s []float64) {
	var x1, x2, y1, y2 float64
	for i, x := range s {
		y := bq.b0*x + bq.b1*x1 + bq.b2*x2 - bq.a1*y1 - bq.a2*y2
		x2, x1 = x1, x
		y2, y1 = y1, y
		s[i] = y
	}
}

// waveProcessor turns the most recent raw samples into an oscilloscope
// display window: band-pass filtered around the tracked pitch, triggered on
// a positive-going zero crossing, normalized, and decimated. It reuses its
// scratch buffer across frames.
type waveProcessor struct {
	sampleRate    int
	windowSamples int
	searchSpan    int
	scratch       []float64
}

func newWaveProcessor(sampleRate int) *waveProcessor {
	return &waveProcessor{
		sampleRate:    sampleRate,
		windowSamples: int(displaySeconds * float64(sampleRate)),
		searchSpan:    int(float64(sampleRate) / pitchFloorHz),
	}
}

// build produces the display points and their rate (points per second).
// freq is the tracked fundamental used to centre the band-pass filter.
func (w *waveProcessor) build(buf []float32, freq float64) (wave []float64, pointRate float64) {
	n := len(buf)
	if w.windowSamples < 2 || n < w.windowSamples+2 || freq <= 0 {
		return nil, 0
	}

	if cap(w.scratch) < n {
		w.scratch = make([]float64, n)
	}
	f := w.scratch[:n]
	for i, s := range buf {
		f[i] = float64(s)
	}

	// Narrow band-pass around the fundamental: two cascaded sections.
	bq := bandpass(freq, float64(w.sampleRate), bandpassQ)
	bq.process(f)
	bq.process(f)

	// Trigger near the end of the buffer, where the filter has fully settled.
	start := n - w.windowSamples - w.searchSpan
	if start < 0 {
		start = 0
	}
	end := start + w.searchSpan
	if end > n-w.windowSamples {
		end = n - w.windowSamples
	}
	trigger := start
	for i := start; i < end; i++ {
		if f[i] <= 0 && f[i+1] > 0 {
			trigger = i
			break
		}
	}

	win := f[trigger : trigger+w.windowSamples]
	peak := 0.0
	for _, v := range win {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	if peak < 1e-9 {
		return nil, 0
	}

	dec := w.windowSamples / targetPoints
	if dec < 1 {
		dec = 1
	}
	m := w.windowSamples / dec
	wave = make([]float64, m)
	inv := 1.0 / peak
	for i := 0; i < m; i++ {
		var sum float64
		base := i * dec
		for j := 0; j < dec; j++ {
			sum += win[base+j]
		}
		v := (sum / float64(dec)) * inv
		wave[i] = math.Round(v*1000) / 1000
	}
	return wave, float64(w.sampleRate) / float64(dec)
}
