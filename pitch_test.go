package main

import (
	"math"
	"testing"
)

func synthSine(freq float64, sampleRate, n int) []float32 {
	buf := make([]float32, n)
	for i := range buf {
		// add a couple of harmonics so it resembles a plucked string
		t := float64(i) / float64(sampleRate)
		v := math.Sin(2*math.Pi*freq*t) +
			0.4*math.Sin(2*math.Pi*2*freq*t) +
			0.2*math.Sin(2*math.Pi*3*freq*t)
		buf[i] = float32(v * 0.3)
	}
	return buf
}

func TestPitchDetectGuitarStrings(t *testing.T) {
	const sr = 44100
	det := newPitchDetector(sr)
	targets := []float64{82.41, 110.0, 146.83, 196.0, 246.94, 329.63}
	for _, f := range targets {
		buf := synthSine(f, sr, det.minInput()+1000)
		got, conf, ok := det.detect(buf)
		if !ok {
			t.Errorf("%.2f Hz: no detection", f)
			continue
		}
		cents := 1200 * math.Log2(got/f)
		if math.Abs(cents) > 5 {
			t.Errorf("%.2f Hz: got %.2f Hz (%.1f cents off), conf %.2f", f, got, cents, conf)
		} else {
			t.Logf("%.2f Hz -> %.2f Hz (%.2f cents, conf %.2f)", f, got, cents, conf)
		}
	}
}

func waveTestBuf(sr int, fn func(t float64) float64) []float32 {
	n := int((filterWarmupSeconds+displaySeconds)*float64(sr)) + sr/int(pitchFloorHz) + 16
	buf := make([]float32, n)
	for i := range buf {
		buf[i] = float32(fn(float64(i) / float64(sr)))
	}
	return buf
}

func TestWaveformTriggersPositiveGoing(t *testing.T) {
	const sr = 44100
	const f0 = 146.83
	// Start the sine at phase pi so the buffer begins negative; the trigger
	// must skip forward to the first positive-going zero crossing.
	buf := waveTestBuf(sr, func(t float64) float64 {
		return math.Sin(2*math.Pi*f0*t + math.Pi)
	})
	wp := newWaveProcessor(sr)
	wave, pr := wp.build(buf, f0)
	if len(wave) == 0 || pr <= 0 {
		t.Fatalf("empty waveform")
	}
	if wave[0] > 0.15 {
		t.Errorf("waveform does not start near a zero crossing: wave[0]=%.3f", wave[0])
	}
	if wave[2] <= wave[0] {
		t.Errorf("waveform does not start in the positive direction: %.3f -> %.3f", wave[0], wave[2])
	}
}

// TestWaveformFiltersToSine feeds a signal rich in harmonics and checks that
// the band-pass leaves an essentially sinusoidal trace: the spacing between
// successive positive-going zero crossings should be near-constant.
func TestWaveformFiltersToSine(t *testing.T) {
	const sr = 44100
	const f0 = 110.0
	buf := waveTestBuf(sr, func(t float64) float64 {
		return 0.3 * (math.Sin(2*math.Pi*f0*t) +
			0.6*math.Sin(2*math.Pi*2*f0*t) +
			0.4*math.Sin(2*math.Pi*3*f0*t))
	})
	wp := newWaveProcessor(sr)
	wave, pr := wp.build(buf, f0)
	if len(wave) == 0 {
		t.Fatalf("empty waveform")
	}

	var crossings []int
	for i := 1; i < len(wave); i++ {
		if wave[i-1] <= 0 && wave[i] > 0 {
			crossings = append(crossings, i)
		}
	}
	if len(crossings) < 3 {
		t.Fatalf("expected several wave starts, got %d", len(crossings))
	}
	// Gaps between crossings should match one period and vary little.
	expected := pr / f0
	var sum, sumSq float64
	for i := 1; i < len(crossings); i++ {
		g := float64(crossings[i] - crossings[i-1])
		sum += g
		sumSq += g * g
	}
	k := float64(len(crossings) - 1)
	mean := sum / k
	std := math.Sqrt(sumSq/k - mean*mean)
	t.Logf("crossing gap mean=%.1f pts (expected %.1f), std=%.2f", mean, expected, std)
	if math.Abs(mean-expected)/expected > 0.05 {
		t.Errorf("crossing period off: mean=%.1f expected=%.1f", mean, expected)
	}
	if std/mean > 0.05 {
		t.Errorf("waveform not sinusoidal: gap std/mean=%.3f", std/mean)
	}
}
