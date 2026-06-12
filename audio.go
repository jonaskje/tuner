package main

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const (
	waveFormatPCM        = 0x0001
	waveFormatIEEEFloat  = 0x0003
	waveFormatExtensible = 0xFFFE
)

var errDeviceNotFound = errors.New("input device not found")

// DeviceInfo describes one capture endpoint as shown in the input picker.
type DeviceInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sampleRate"`
}

// withCOM runs fn on a dedicated OS thread with COM initialized. WASAPI
// calls must stay on the thread that called CoInitializeEx.
func withCOM(fn func() error) error {
	ch := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
			ch <- fmt.Errorf("CoInitializeEx: %w", err)
			return
		}
		defer ole.CoUninitialize()
		ch <- fn()
	}()
	return <-ch
}

func enumerateDevices() ([]DeviceInfo, error) {
	var devices []DeviceInfo
	err := withCOM(func() error {
		var e error
		devices, e = listDevicesOnThread()
		return e
	})
	return devices, err
}

func newDeviceEnumerator() (*wca.IMMDeviceEnumerator, error) {
	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		return nil, fmt.Errorf("create device enumerator: %w", err)
	}
	return mmde, nil
}

func listDevicesOnThread() ([]DeviceInfo, error) {
	mmde, err := newDeviceEnumerator()
	if err != nil {
		return nil, err
	}
	defer mmde.Release()

	var mmdc *wca.IMMDeviceCollection
	if err := mmde.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &mmdc); err != nil {
		return nil, fmt.Errorf("enumerate capture endpoints: %w", err)
	}
	defer mmdc.Release()

	var count uint32
	if err := mmdc.GetCount(&count); err != nil {
		return nil, err
	}
	devices := make([]DeviceInfo, 0, count)
	for i := uint32(0); i < count; i++ {
		var mmd *wca.IMMDevice
		if err := mmdc.Item(i, &mmd); err != nil {
			continue
		}
		info, err := describeDevice(mmd)
		mmd.Release()
		if err == nil {
			devices = append(devices, info)
		}
	}
	return devices, nil
}

func describeDevice(mmd *wca.IMMDevice) (DeviceInfo, error) {
	var info DeviceInfo
	if err := mmd.GetId(&info.ID); err != nil {
		return info, err
	}
	var ps *wca.IPropertyStore
	if err := mmd.OpenPropertyStore(wca.STGM_READ, &ps); err == nil {
		var pv wca.PROPVARIANT
		if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err == nil {
			info.Name = pv.String()
		}
		ps.Release()
	}
	if info.Name == "" {
		info.Name = "Unknown device"
	}

	// The shared-mode mix format tells us how many channels the endpoint
	// exposes and at what rate it will be captured.
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return info, err
	}
	defer ac.Release()
	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		return info, err
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))
	info.Channels = int(wfx.NChannels)
	info.SampleRate = int(wfx.NSamplesPerSec)
	return info, nil
}

// findDeviceByID enumerates active capture endpoints and returns the one
// whose endpoint ID matches. Caller must Release the returned device.
// (go-wca's IMMDeviceEnumerator.GetDevice binding is a stub, so we match
// by enumeration instead.)
func findDeviceByID(id string) (*wca.IMMDevice, error) {
	mmde, err := newDeviceEnumerator()
	if err != nil {
		return nil, err
	}
	defer mmde.Release()

	var mmdc *wca.IMMDeviceCollection
	if err := mmde.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &mmdc); err != nil {
		return nil, err
	}
	defer mmdc.Release()

	var count uint32
	if err := mmdc.GetCount(&count); err != nil {
		return nil, err
	}
	for i := uint32(0); i < count; i++ {
		var mmd *wca.IMMDevice
		if err := mmdc.Item(i, &mmd); err != nil {
			continue
		}
		var devID string
		if err := mmd.GetId(&devID); err == nil && devID == id {
			return mmd, nil
		}
		mmd.Release()
	}
	return nil, errDeviceNotFound
}

// sampleEncoding inspects a mix format and reports how to decode samples.
func sampleEncoding(wfx *wca.WAVEFORMATEX) (isFloat bool, err error) {
	tag := wfx.WFormatTag
	if tag == waveFormatExtensible {
		// SubFormat GUID sits at byte offset 24 of WAVEFORMATEXTENSIBLE;
		// its Data1 field is the plain format tag.
		base := unsafe.Pointer(wfx)
		tag = uint16(*(*uint32)(unsafe.Pointer(uintptr(base) + 24)))
	}
	switch tag {
	case waveFormatIEEEFloat:
		if wfx.WBitsPerSample != 32 {
			return false, fmt.Errorf("unsupported float sample size: %d bits", wfx.WBitsPerSample)
		}
		return true, nil
	case waveFormatPCM:
		if wfx.WBitsPerSample != 16 {
			return false, fmt.Errorf("unsupported PCM sample size: %d bits", wfx.WBitsPerSample)
		}
		return false, nil
	default:
		return false, fmt.Errorf("unsupported sample format tag: %#x", tag)
	}
}

// captureEngine continuously captures one channel of a WASAPI endpoint
// into a ring buffer.
type captureEngine struct {
	DeviceID   string
	Channel    int
	SampleRate int // valid after startCapture returns

	ring     *ringBuffer
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	onError  func(error)
}

// startCapture opens the device and starts the capture loop. It returns
// once audio is flowing (or with the setup error). onError is invoked if
// the stream dies later, e.g. when the device is unplugged.
func startCapture(deviceID string, channel int, onError func(error)) (*captureEngine, error) {
	e := &captureEngine{
		DeviceID: deviceID,
		Channel:  channel,
		ring:     newRingBuffer(1 << 18),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		onError:  onError,
	}
	ready := make(chan error, 1)
	go e.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return e, nil
}

// Stop terminates the capture loop and waits for it to exit.
func (e *captureEngine) Stop() {
	e.stopOnce.Do(func() { close(e.stop) })
	<-e.done
}

func (e *captureEngine) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(e.done)

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		ready <- fmt.Errorf("CoInitializeEx: %w", err)
		return
	}
	defer ole.CoUninitialize()

	started, err := e.captureLoop(ready)
	if err != nil && started && e.onError != nil {
		e.onError(err)
	}
}

// captureLoop does device setup, signals ready, then polls for packets
// until stopped. The started return tells run whether ready was already
// signalled successfully (later errors then go to onError).
func (e *captureEngine) captureLoop(ready chan<- error) (started bool, err error) {
	mmd, err := findDeviceByID(e.DeviceID)
	if err != nil {
		ready <- err
		return false, err
	}
	defer mmd.Release()

	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		err = fmt.Errorf("activate audio client: %w", err)
		ready <- err
		return false, err
	}
	defer ac.Release()

	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		err = fmt.Errorf("get mix format: %w", err)
		ready <- err
		return false, err
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))

	channels := int(wfx.NChannels)
	blockAlign := int(wfx.NBlockAlign)
	bytesPerSample := int(wfx.WBitsPerSample) / 8
	isFloat, err := sampleEncoding(wfx)
	if err != nil {
		ready <- err
		return false, err
	}
	if e.Channel < 0 || e.Channel >= channels {
		err = fmt.Errorf("channel %d not available (device has %d)", e.Channel+1, channels)
		ready <- err
		return false, err
	}

	// 100 ms buffer (REFERENCE_TIME is in 100 ns units), polled every few ms.
	const bufferDuration = 1_000_000
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, 0, bufferDuration, 0, wfx, nil); err != nil {
		err = fmt.Errorf("initialize audio client: %w", err)
		ready <- err
		return false, err
	}

	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		err = fmt.Errorf("get capture client: %w", err)
		ready <- err
		return false, err
	}
	defer acc.Release()

	if err := ac.Start(); err != nil {
		err = fmt.Errorf("start capture: %w", err)
		ready <- err
		return false, err
	}
	defer ac.Stop()

	e.SampleRate = int(wfx.NSamplesPerSec)
	ready <- nil

	chunk := make([]float32, 0, 4800)
	for {
		select {
		case <-e.stop:
			return true, nil
		default:
		}
		for {
			var frames uint32
			if err := acc.GetNextPacketSize(&frames); err != nil {
				return true, fmt.Errorf("capture stream lost: %w", err)
			}
			if frames == 0 {
				break
			}
			var data *byte
			var got, flags uint32
			if err := acc.GetBuffer(&data, &got, &flags, nil, nil); err != nil {
				return true, fmt.Errorf("capture stream lost: %w", err)
			}
			if got > 0 && data != nil {
				raw := unsafe.Slice(data, int(got)*blockAlign)
				silent := flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0
				chunk = chunk[:0]
				for f := 0; f < int(got); f++ {
					var s float32
					if !silent {
						off := f*blockAlign + e.Channel*bytesPerSample
						if isFloat {
							s = *(*float32)(unsafe.Pointer(&raw[off]))
						} else {
							s = float32(int16(uint16(raw[off])|uint16(raw[off+1])<<8)) / 32768
						}
					}
					chunk = append(chunk, s)
				}
				e.ring.write(chunk)
			}
			if err := acc.ReleaseBuffer(got); err != nil {
				return true, fmt.Errorf("capture stream lost: %w", err)
			}
		}
		time.Sleep(4 * time.Millisecond)
	}
}

// ringBuffer is a fixed-size, thread-safe sample buffer that keeps the
// most recent samples.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []float32
	pos   int // next write index
	count int // valid samples, capped at len(buf)
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]float32, size)}
}

func (r *ringBuffer) write(samples []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(samples) > 0 {
		n := copy(r.buf[r.pos:], samples)
		r.pos = (r.pos + n) % len(r.buf)
		samples = samples[n:]
		r.count += n
	}
	if r.count > len(r.buf) {
		r.count = len(r.buf)
	}
}

// latest copies the most recent len(out) samples in chronological order.
// It returns false if not enough samples have been captured yet.
func (r *ringBuffer) latest(out []float32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(out)
	if n == 0 || n > r.count {
		return false
	}
	start := (r.pos - n + len(r.buf)) % len(r.buf)
	first := copy(out, r.buf[start:])
	if first < n {
		copy(out[first:], r.buf[:n-first])
	}
	return true
}
