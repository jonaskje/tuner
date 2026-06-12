package main

import "math"

// pitchDetector implements the YIN algorithm (de Cheveigné & Kawahara,
// 2002): squared-difference function, cumulative mean normalization,
// absolute threshold and parabolic interpolation. It is allocation-free
// after construction so it can run at display rate.
type pitchDetector struct {
	sampleRate int
	window     int // integration window W
	tauMin     int // highest detectable pitch = sampleRate/tauMin
	tauMax     int // lowest detectable pitch = sampleRate/tauMax
	diff       []float64
	cmnd       []float64
}

const (
	pitchFloorHz   = 40.0   // generous room below low E (82.4 Hz), covers drop tunings
	pitchCeilHz    = 1000.0 // well above high E (329.6 Hz) and its nearby harmonics
	yinThreshold   = 0.15
	yinAcceptCmnd  = 0.35 // fallback acceptance when no dip crosses the threshold
)

func newPitchDetector(sampleRate int) *pitchDetector {
	tauMax := int(float64(sampleRate) / pitchFloorHz)
	tauMin := int(float64(sampleRate) / pitchCeilHz)
	if tauMin < 2 {
		tauMin = 2
	}
	return &pitchDetector{
		sampleRate: sampleRate,
		window:     2048 * sampleRate / 48000,
		tauMin:     tauMin,
		tauMax:     tauMax,
		diff:       make([]float64, tauMax+1),
		cmnd:       make([]float64, tauMax+1),
	}
}

// minInput is the number of samples detect needs.
func (p *pitchDetector) minInput() int {
	return p.window + p.tauMax
}

// detect estimates the dominant fundamental frequency from the most
// recent minInput() samples of buf.
func (p *pitchDetector) detect(buf []float32) (freq, confidence float64, ok bool) {
	need := p.minInput()
	if len(buf) < need {
		return 0, 0, false
	}
	x := buf[len(buf)-need:]

	// Squared-difference function d(tau).
	for tau := p.tauMin; tau <= p.tauMax; tau++ {
		var sum float64
		for i := 0; i < p.window; i++ {
			d := float64(x[i]) - float64(x[i+tau])
			sum += d * d
		}
		p.diff[tau] = sum
	}

	// Cumulative mean normalized difference d'(tau). Lags below tauMin
	// contribute to the running mean as well, so compute d there too —
	// without them the normalization is skewed for small tau.
	var running float64
	for tau := 1; tau < p.tauMin; tau++ {
		var sum float64
		for i := 0; i < p.window; i++ {
			d := float64(x[i]) - float64(x[i+tau])
			sum += d * d
		}
		running += sum
	}
	p.cmnd[0] = 1
	for tau := p.tauMin; tau <= p.tauMax; tau++ {
		running += p.diff[tau]
		if running == 0 {
			p.cmnd[tau] = 1
		} else {
			p.cmnd[tau] = p.diff[tau] * float64(tau) / running
		}
	}

	// First dip below threshold, refined to its local minimum.
	best := -1
	for tau := p.tauMin; tau <= p.tauMax; tau++ {
		if p.cmnd[tau] < yinThreshold {
			for tau+1 <= p.tauMax && p.cmnd[tau+1] < p.cmnd[tau] {
				tau++
			}
			best = tau
			break
		}
	}
	if best < 0 {
		// No confident dip: fall back to the global minimum if decent.
		min := math.Inf(1)
		for tau := p.tauMin; tau <= p.tauMax; tau++ {
			if p.cmnd[tau] < min {
				min = p.cmnd[tau]
				best = tau
			}
		}
		if best < 0 || min > yinAcceptCmnd {
			return 0, 0, false
		}
	}

	// Parabolic interpolation around the chosen lag for sub-sample
	// period accuracy.
	tauF := float64(best)
	if best > p.tauMin && best < p.tauMax {
		a, b, c := p.cmnd[best-1], p.cmnd[best], p.cmnd[best+1]
		denom := a - 2*b + c
		if denom != 0 {
			delta := 0.5 * (a - c) / denom
			if delta > -1 && delta < 1 {
				tauF += delta
			}
		}
	}

	freq = float64(p.sampleRate) / tauF
	confidence = 1 - p.cmnd[best]
	if confidence < 0 {
		confidence = 0
	}
	return freq, confidence, true
}

// rms returns the root mean square level of buf.
func rms(buf []float32) float64 {
	if len(buf) == 0 {
		return 0
	}
	var sum float64
	for _, s := range buf {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(buf)))
}
