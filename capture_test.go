package main

import (
	"testing"
	"time"
)

// TestCaptureSmoke opens the first available capture device, lets it run
// briefly, and confirms that samples actually flow into the ring buffer.
func TestCaptureSmoke(t *testing.T) {
	devices, err := enumerateDevices()
	if err != nil {
		t.Fatalf("enumerateDevices: %v", err)
	}
	if len(devices) == 0 {
		t.Skip("no capture devices present")
	}
	d := devices[0]

	eng, err := startCapture(d.ID, 0, func(e error) {
		t.Logf("capture error callback: %v", e)
	})
	if err != nil {
		t.Fatalf("startCapture(%q): %v", d.Name, err)
	}
	defer eng.Stop()

	if eng.SampleRate <= 0 {
		t.Fatalf("invalid sample rate: %d", eng.SampleRate)
	}
	t.Logf("capturing from %q ch0 at %d Hz", d.Name, eng.SampleRate)

	time.Sleep(300 * time.Millisecond)

	buf := make([]float32, 4096)
	if !eng.ring.latest(buf) {
		t.Fatalf("no samples captured after 300ms")
	}
	t.Logf("ring delivered %d samples; level=%.5f", len(buf), rms(buf))
}
