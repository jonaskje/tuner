package main

import "testing"

func TestEnumerateDevices(t *testing.T) {
	devices, err := enumerateDevices()
	if err != nil {
		t.Fatalf("enumerateDevices: %v", err)
	}
	t.Logf("found %d capture device(s)", len(devices))
	for i, d := range devices {
		t.Logf("[%d] %q  channels=%d  sampleRate=%d  id=%s", i, d.Name, d.Channels, d.SampleRate, d.ID)
	}
	if len(devices) == 0 {
		t.Skip("no capture devices present on this machine")
	}
}
