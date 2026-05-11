package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

type WorkerMode string

const (
	ModeCPU WorkerMode = "cpu"
	ModeGPU WorkerMode = "gpu"
)

type StartCmd struct {
	Cmd        string `json:"cmd"`
	Challenge  string `json:"challenge"`
	Difficulty string `json:"difficulty"`
	Prefix     string `json:"prefix"`
	Batch      uint64 `json:"batch"`
	Mode       string `json:"mode"`
	GPUDevice  *int   `json:"gpu_device,omitempty"`
	CPUThreads *int   `json:"cpu_threads,omitempty"`
}

type RetargetCmd struct {
	Cmd        string `json:"cmd"`
	Challenge  string `json:"challenge"`
	Difficulty string `json:"difficulty"`
}

type StopCmd struct {
	Cmd string `json:"cmd"`
}

type GPUDevice struct {
	Index            int    `json:"index"`
	Platform         string `json:"platform"`
	Name             string `json:"name"`
	ComputeUnits     uint32 `json:"compute_units"`
	MaxWorkGroupSize int    `json:"max_work_group_size"`
}

type WorkerEvent struct {
	Event      string      `json:"event"`
	Version    string      `json:"version,omitempty"`
	CPUThreads int         `json:"cpu_threads,omitempty"`
	GPUDevices []GPUDevice `json:"gpu_devices,omitempty"`
	GPU        []GPUDevice `json:"gpu,omitempty"`
	Hashes     uint64      `json:"hashes,omitempty"`
	Hashrate   float64     `json:"hashrate,omitempty"`
	ElapsedMs  uint64      `json:"elapsed_ms,omitempty"`
	Nonce      string      `json:"nonce,omitempty"`
	Result     string      `json:"result,omitempty"`
	Message    string      `json:"message,omitempty"`
}

type Worker struct {
	Path string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr io.ReadCloser

	eventsMu sync.Mutex
	events   chan WorkerEvent
	closed   bool

	wg sync.WaitGroup
}

func NewWorker(path string) *Worker {
	return &Worker{Path: path, events: make(chan WorkerEvent, 256)}
}

func (w *Worker) Start(ctx context.Context) error {
	w.cmd = exec.CommandContext(ctx, w.Path)
	in, err := w.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	out, err := w.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	errpipe, err := w.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := w.cmd.Start(); err != nil {
		return fmt.Errorf("start worker %s: %w", w.Path, err)
	}
	w.stdin = in
	w.stdout = bufio.NewScanner(out)
	w.stdout.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	w.stderr = errpipe

	w.wg.Add(2)
	go w.readEvents()
	go w.drainStderr()

	return nil
}

func (w *Worker) readEvents() {
	defer w.wg.Done()
	for w.stdout.Scan() {
		line := w.stdout.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev WorkerEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			ev = WorkerEvent{Event: "error", Message: fmt.Sprintf("parse worker line: %v; raw=%s", err, string(line))}
		}
		w.eventsMu.Lock()
		if !w.closed {
			select {
			case w.events <- ev:
			default:
			}
		}
		w.eventsMu.Unlock()
	}
	w.eventsMu.Lock()
	if !w.closed {
		close(w.events)
		w.closed = true
	}
	w.eventsMu.Unlock()
}

func (w *Worker) drainStderr() {
	defer w.wg.Done()
	_, _ = io.Copy(io.Discard, w.stderr)
}

func (w *Worker) Events() <-chan WorkerEvent {
	return w.events
}

func (w *Worker) Send(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal cmd: %w", err)
	}
	raw = append(raw, '\n')
	if _, err := w.stdin.Write(raw); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}

func (w *Worker) Stop() error {
	if w.stdin != nil {
		_ = w.stdin.Close()
	}
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	w.wg.Wait()
	return nil
}
