//go:build windows && amd64

package nativevideo

import (
	"context"
	"image"
	"runtime"
	"sync"
	"time"
)

type Frame struct {
	Image      *image.RGBA
	Delay, PTS time.Duration
	Loops      uint64
	Err        error
}
type Stream struct {
	First    Frame
	requests chan *image.RGBA
	frames   chan Frame
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	notify   func()
}

// Open decodes one poster frame only. Further decoding is explicitly requested
// by the hover scheduler, one frame at a time, never on the UI thread. Recycle
// the previous display frame after upload to keep only two small RGBA buffers.
func Open(ctx context.Context, data []byte) (*Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := Inspect(data); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Stream{requests: make(chan *image.RGBA, 1), frames: make(chan Frame, 1), done: make(chan struct{}), cancel: cancel}
	ready := make(chan Frame, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(s.done)
		defer close(s.frames)
		d, err := openDecoder(data)
		data = nil
		if err != nil {
			ready <- Frame{Err: err}
			return
		}
		defer d.close()
		first, err := d.next(nil)
		first.Err = err
		ready <- first
		if err != nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case reuse := <-s.requests:
				if ctx.Err() != nil {
					return
				}
				frame, err := d.next(reuse)
				frame.Err = err
				select {
				case s.frames <- frame:
				case <-ctx.Done():
					return
				}
				s.mu.Lock()
				notify := s.notify
				s.mu.Unlock()
				if notify != nil {
					notify()
				}
				if err != nil {
					return
				}
			}
		}
	}()
	select {
	case first := <-ready:
		if first.Err != nil {
			cancel()
			<-s.done
			return nil, first.Err
		}
		s.First = first
		return s, nil
	case <-ctx.Done():
		cancel()
		<-s.done
		return nil, ctx.Err()
	}
}
func (s *Stream) SetNotify(fn func()) { s.mu.Lock(); s.notify = fn; s.mu.Unlock() }
func (s *Stream) Request(reuse *image.RGBA) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.requests <- reuse:
		return true
	default:
		return false
	}
}
func (s *Stream) Take() (Frame, bool) {
	select {
	case f, ok := <-s.frames:
		return f, ok
	default:
		return Frame{}, false
	}
}
func (s *Stream) Close()                { s.cancel() }
func (s *Stream) Done() <-chan struct{} { return s.done }
