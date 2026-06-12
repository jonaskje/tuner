# Guitar Tuner

A sleek desktop guitar tuner built with **Go + [Wails](https://wails.io)**. It captures
audio from a chosen input device/channel via WASAPI, tracks the dominant pitch with the
**YIN** algorithm, and visualizes tuning and the live waveform on an oscilloscope-style display.

## Features

- **Input selection** — pick any capture device *and* a specific input channel (e.g. Input 1 / Input 2
  on a Focusrite Scarlett). The choice is saved and reused on the next launch.
- **Auto re-prompt** — if the saved device is unavailable on startup (or is unplugged while running),
  the input dialog opens automatically.
- **Strings column** — `E B G D A E` in large, dimmed-green letters with their target frequencies.
  The matching string lights up and blooms green when in tune.
- **Tuning pane** — a center "perfect" bar with a moving deviation line; within ±5 cents the bar
  glows green.
- **Waveform pane** — an oscilloscope triggered on a positive-going zero crossing, scaled to show
  exactly four waves of the tracked note, with a tick at the start of each wave.
- **Freeze** — locks the current waveform as a dim reference and the horizontal scale, so a live note
  can be slid against it (e.g. matching an octave by aligning crossings).

## Architecture

| File | Responsibility |
|------|----------------|
| `audio.go` | WASAPI capture (go-wca/go-ole): device enumeration, channel extraction, ring buffer |
| `pitch.go` | YIN pitch detection (difference function, CMND, threshold, parabolic interpolation) |
| `waveform.go` | Oscilloscope window extraction (trigger, normalize, decimate) |
| `config.go` | Persisted device/channel selection under the user config dir |
| `app.go` | Wails-bound API + the ~60 fps analysis loop that emits `frame` events |
| `frontend/` | Vanilla JS + Canvas UI (strings, tuning pane, waveform, device dialog) |

Config is stored at `%AppData%\GuitarTuner\config.json` on Windows.

## Develop

```bash
wails dev      # hot-reload dev server
```

## Build

```bash
wails build    # produces build/bin/tuner.exe
```

## Test

```bash
go test ./...  # pitch accuracy, waveform trigger, device enumeration, capture smoke test
```

> Note: WASAPI capture is Windows-only; the audio backend builds and runs on Windows.
