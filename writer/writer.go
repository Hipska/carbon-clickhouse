package writer

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lomik/carbon-clickhouse/helper/RowBinary"
	"github.com/lomik/stop"
	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
)

// Writer dumps all received data in prepared for clickhouse format
type Writer struct {
	stop.Struct
	sync.RWMutex
	stat struct {
		writtenBytes uint32
	}
	inputChan    chan *RowBinary.WriteBuffer
	path         string
	fileInterval time.Duration
	inProgress   map[string]bool // current writing files
	logger       *zap.Logger
	uploaders    []string
	onFinish     func(string) error
}

func New(in chan *RowBinary.WriteBuffer, path string, fileInterval time.Duration, uploaders []string, onFinish func(string) error) *Writer {
	finishCallback := func(fn string) error {
		if err := Link(fn, uploaders); err != nil {
			return err
		}

		if onFinish != nil {
			return onFinish(fn)
		}

		return nil
	}

	return &Writer{
		inputChan:    in,
		path:         path,
		fileInterval: fileInterval,
		inProgress:   make(map[string]bool),
		logger:       zapwriter.Logger("writer"),
		uploaders:    uploaders,
		onFinish:     finishCallback,
	}
}

func (w *Writer) Start() error {
	return w.StartFunc(func() error {
		// link pre-existing files
		if err := w.LinkAll(); err != nil {
			return err
		}
		if err := w.Cleanup(); err != nil {
			return err
		}
		w.Go(w.worker)
		w.Go(w.cleaner)
		return nil
	})
}

func (w *Writer) Stat(send func(metric string, value float64)) {
	writtenBytes := atomic.LoadUint32(&w.stat.writtenBytes)
	atomic.AddUint32(&w.stat.writtenBytes, -writtenBytes)
	send("writtenBytes", float64(writtenBytes))
}

func (w *Writer) IsInProgress(filename string) bool {
	w.RLock()
	v := w.inProgress[filename]
	w.RUnlock()
	return v
}

func (w *Writer) worker(exit chan struct{}) {
	var out *os.File
	var outBuf *bufio.Writer
	var fn string // current filename

	defer func() {
		if out != nil {
			out.Close()
		}
	}()

	// close old file, open new
	rotate := func() {
		if out != nil {
			outBuf.Flush()
			out.Close()
			out = nil
			outBuf = nil
		}

		var err error

	OpenLoop:
		for {
			go func(filename string) {
				if filename == "" || w.onFinish == nil {
					return
				}

				err = w.onFinish(filename)
				if err != nil {
					w.logger.Error("onFinish callback failed", zap.String("filename", filename), zap.Error(err))
				}
			}(fn)

			// replace fn in inProgress
			w.Lock()
			delete(w.inProgress, fn)
			fn = path.Join(w.path, fmt.Sprintf("default.%d", time.Now().UnixNano()))
			w.inProgress[fn] = true
			w.Unlock()

			out, err = os.OpenFile(fn, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)

			if err != nil {
				w.logger.Error("create failed", zap.String("filename", fn), zap.Error(err))

				// check exit channel
				select {
				case <-exit:
					break OpenLoop
				default:
				}

				// try and spam to error log every second
				time.Sleep(time.Second)

				continue OpenLoop
			}

			outBuf = bufio.NewWriterSize(out, 1024*1024)
			break OpenLoop
		}
	}

	// open first file
	rotate()

	ticker := time.NewTicker(w.fileInterval)
	defer ticker.Stop()

	for {
		select {
		case b := <-w.inputChan:
			_, err := outBuf.Write(b.Body[:b.Used])
			if b.ConfirmRequired() {
				if err != nil {
					b.Fail(err)
				} else {
					err := outBuf.Flush()
					if err != nil {
						b.Fail(err)
					} else {
						b.Confirm()
					}
				}
			}
			// @TODO: log error?
			atomic.AddUint32(&w.stat.writtenBytes, uint32(b.Used))
			b.Release()
		case <-ticker.C:
			rotate()
		case <-exit:
			return
		}
	}
}

func (w *Writer) cleaner(exit chan struct{}) {
	ticker := time.NewTicker(w.fileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-exit:
			return
		case <-ticker.C:
			w.Cleanup()
		}
	}
}
