//go:build windows && amd64

package nativevideo

import (
	"fmt"
	"golang.org/x/sys/windows"
	"image"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var mfplat = windows.NewLazySystemDLL("mfplat.dll")
var mfread = windows.NewLazySystemDLL("mfreadwrite.dll")
var ole32 = windows.NewLazySystemDLL("ole32.dll")
var shlwapi = windows.NewLazySystemDLL("shlwapi.dll")
var startup = mfplat.NewProc("MFStartup")
var shutdown = mfplat.NewProc("MFShutdown")
var createAttributes = mfplat.NewProc("MFCreateAttributes")
var createMediaType = mfplat.NewProc("MFCreateMediaType")
var wrapStream = mfplat.NewProc("MFCreateMFByteStreamOnStream")
var createReader = mfread.NewProc("MFCreateSourceReaderFromByteStream")
var memStream = shlwapi.NewProc("SHCreateMemStream")
var coInit = ole32.NewProc("CoInitializeEx")
var coUninit = ole32.NewProc("CoUninitialize")

func guid(s string) windows.GUID {
	v, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return v
}

var majorType = guid("{48eba18e-f8c9-4687-bf11-0a74c9f96a8f}")
var subtype = guid("{f7e34c9a-42e8-4714-b74b-cb29d72c35e5}")
var videoType = guid("{73646976-0000-0010-8000-00aa00389b71}")
var rgb32 = guid("{00000016-0000-0010-8000-00aa00389b71}")
var frameSize = guid("{1652c33d-d6b2-4012-b834-72030849a37d}")
var frameRate = guid("{c459a2e8-3d2c-4e44-b132-fee5156c7bb0}")
var strideKey = guid("{644b4e48-1e02-4516-b0eb-c01ca9d4a9c6}")
var processing = guid("{fb394f3d-ccf1-42ee-bbb3-f9b845d5681d}")
var disableDXVA = guid("{aa456cfd-3943-4a1e-a77d-1838c0ea2e35}")
var h264 = guid("{34363248-0000-0010-8000-00aa00389b71}")

const videoStream = 0xfffffffc
const allStreams = 0xfffffffe

type com struct{ vtbl *[64]uintptr }

//go:uintptrescapes
func invoke(o *com, index int, args ...uintptr) uintptr {
	a := make([]uintptr, 1, len(args)+1)
	a[0] = uintptr(unsafe.Pointer(o))
	a = append(a, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[index], a...)
	return r
}
func check(r uintptr, stage string) error {
	if int32(r) < 0 {
		return fmt.Errorf("Media Foundation %s failed (0x%08x); the Windows media codec may be unavailable", stage, uint32(r))
	}
	return nil
}
func release(o *com) {
	if o != nil {
		invoke(o, 2)
	}
}
func set32(o *com, k *windows.GUID, v uint32) error {
	return check(invoke(o, 21, uintptr(unsafe.Pointer(k)), uintptr(v)), "SetUINT32")
}
func setGUID(o *com, k, v *windows.GUID) error {
	return check(invoke(o, 24, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v))), "SetGUID")
}
func get64(o *com, k *windows.GUID) (uint64, error) {
	var v uint64
	err := check(invoke(o, 8, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(&v))), "GetUINT64")
	return v, err
}
func get32(o *com, k *windows.GUID) (uint32, error) {
	var v uint32
	err := check(invoke(o, 7, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(&v))), "GetUINT32")
	return v, err
}

// All decoder operations and releases stay on the worker's locked MTA thread.
type decoder struct {
	reader               *com
	started, initialized bool
	w, h, stride         int
	delay                time.Duration
	loops                uint64
}

func openDecoder(data []byte) (d *decoder, err error) {
	d = &decoder{}
	defer func() {
		if err != nil {
			d.close()
		}
	}()
	for _, p := range []*windows.LazyProc{startup, shutdown, createAttributes, createMediaType, wrapStream, createReader, memStream, coInit, coUninit} {
		if e := p.Find(); e != nil {
			return d, fmt.Errorf("Windows Media Foundation is unavailable: %w", e)
		}
	}
	r, _, _ := coInit.Call(0, 0)
	if err = check(r, "COM initialization"); err != nil {
		return
	}
	d.initialized = true
	r, _, _ = startup.Call(0x20070, 1)
	if err = check(r, "startup"); err != nil {
		return
	}
	d.started = true
	var attrs, stream, bytes *com
	defer func() { release(attrs); release(bytes); release(stream) }()
	r, _, _ = createAttributes.Call(uintptr(unsafe.Pointer(&attrs)), 2)
	if err = check(r, "attributes"); err != nil {
		return
	}
	if err = set32(attrs, &processing, 1); err != nil {
		return
	}
	if err = set32(attrs, &disableDXVA, 1); err != nil {
		return
	}
	r, _, _ = memStream.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	runtime.KeepAlive(data)
	if r == 0 {
		return d, fmt.Errorf("could not create in-memory MP4 stream")
	}
	stream = (*com)(unsafe.Pointer(r))
	r, _, _ = wrapStream.Call(uintptr(unsafe.Pointer(stream)), uintptr(unsafe.Pointer(&bytes)))
	if err = check(r, "byte stream"); err != nil {
		return
	}
	r, _, _ = createReader.Call(uintptr(unsafe.Pointer(bytes)), uintptr(unsafe.Pointer(attrs)), uintptr(unsafe.Pointer(&d.reader)))
	if err = check(r, "source reader"); err != nil {
		return
	}
	if err = check(invoke(d.reader, 4, allStreams, 0), "disable audio/other streams"); err != nil {
		return
	}
	if err = check(invoke(d.reader, 4, videoStream, 1), "select video"); err != nil {
		return
	}
	var native *com
	if err = check(invoke(d.reader, 5, videoStream, 0, uintptr(unsafe.Pointer(&native))), "native type"); err != nil {
		return
	}
	var codec windows.GUID
	err = check(invoke(native, 10, uintptr(unsafe.Pointer(&subtype)), uintptr(unsafe.Pointer(&codec))), "native codec")
	release(native)
	if err != nil {
		return
	}
	if codec != h264 {
		return d, ErrFormat
	}
	var typ *com
	r, _, _ = createMediaType.Call(uintptr(unsafe.Pointer(&typ)))
	if err = check(r, "output type"); err != nil {
		return
	}
	defer release(typ)
	if err = setGUID(typ, &majorType, &videoType); err != nil {
		return
	}
	if err = setGUID(typ, &subtype, &rgb32); err != nil {
		return
	}
	if err = check(invoke(d.reader, 7, videoStream, 0, uintptr(unsafe.Pointer(typ))), "RGB32 conversion"); err != nil {
		return
	}
	err = d.format()
	return
}
func (d *decoder) format() error {
	var typ *com
	if err := check(invoke(d.reader, 6, videoStream, uintptr(unsafe.Pointer(&typ))), "current type"); err != nil {
		return err
	}
	defer release(typ)
	size, err := get64(typ, &frameSize)
	if err != nil {
		return err
	}
	d.w, d.h = int(size>>32), int(uint32(size))
	if d.w < 1 || d.h < 1 || d.w*d.h > MaxPixels {
		return ErrLimit
	}
	stride, err := get32(typ, &strideKey)
	if err != nil {
		stride = uint32(d.w * 4)
	}
	d.stride = int(int32(stride))
	if abs(d.stride) < d.w*4 || abs(d.stride) > d.w*4+4096 {
		return ErrLimit
	}
	d.delay = 100 * time.Millisecond
	if rate, e := get64(typ, &frameRate); e == nil && uint32(rate) > 0 && rate>>32 > 0 {
		d.delay = time.Second * time.Duration(uint32(rate)) / time.Duration(rate>>32)
	}
	d.delay = min(time.Second, max(20*time.Millisecond, d.delay))
	return nil
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func (d *decoder) close() {
	release(d.reader)
	d.reader = nil
	if d.started {
		shutdown.Call()
		d.started = false
	}
	if d.initialized {
		coUninit.Call()
		d.initialized = false
	}
}
func (d *decoder) rewind() error {
	// PROPVARIANT VT_I8=20, timestamp zero, GUID_NULL time format.
	var format windows.GUID
	position := struct {
		Type     uint16
		Reserved [3]uint16
		Value    int64
		Tail     uint64
	}{Type: 20}
	if err := check(invoke(d.reader, 8, uintptr(unsafe.Pointer(&format)), uintptr(unsafe.Pointer(&position))), "loop seek"); err != nil {
		return err
	}
	d.loops++
	return nil
}
func (d *decoder) next(reuse *image.RGBA) (Frame, error) {
	for skipped := 0; skipped < 32; skipped++ {
		var actual, flags uint32
		var timestamp int64
		var sample *com
		err := check(invoke(d.reader, 9, videoStream, 0, uintptr(unsafe.Pointer(&actual)), uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&timestamp)), uintptr(unsafe.Pointer(&sample))), "read frame")
		if err != nil {
			release(sample)
			return Frame{}, err
		}
		if flags&1 != 0 {
			release(sample)
			return Frame{}, ErrFormat
		}
		if flags&0x30 != 0 {
			if err = d.format(); err != nil {
				release(sample)
				return Frame{}, err
			}
		}
		if sample != nil {
			f, err := d.copyFrame(sample, reuse)
			release(sample)
			f.PTS = time.Duration(timestamp) * 100
			f.Loops = d.loops
			return f, err
		}
		if flags&2 != 0 {
			if d.loops > 0 && skipped > 1 {
				return Frame{}, ErrFormat
			}
			if err = d.rewind(); err != nil {
				return Frame{}, err
			}
		}
	}
	return Frame{}, ErrFormat
}
func (d *decoder) copyFrame(sample *com, reuse *image.RGBA) (Frame, error) {
	var buffer *com
	if err := check(invoke(sample, 41, uintptr(unsafe.Pointer(&buffer))), "sample buffer"); err != nil {
		return Frame{}, err
	}
	defer release(buffer)
	var bytes *byte
	var length uint32
	if err := check(invoke(buffer, 3, uintptr(unsafe.Pointer(&bytes)), 0, uintptr(unsafe.Pointer(&length))), "buffer lock"); err != nil {
		return Frame{}, err
	}
	defer invoke(buffer, 4)
	if bytes == nil || int(length) < abs(d.stride)*(d.h-1)+d.w*4 || length > MaxPixels*8 {
		return Frame{}, ErrLimit
	}
	src := unsafe.Slice(bytes, int(length))
	w, h := d.w, d.h
	if w > 360 {
		h = max(1, h*360/w)
		w = 360
	}
	if h > 240 {
		w = max(1, w*240/h)
		h = 240
	}
	if reuse == nil || reuse.Bounds().Dx() != w || reuse.Bounds().Dy() != h {
		reuse = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		sy := y * d.h / h
		if d.stride < 0 {
			sy = d.h - 1 - sy
		}
		row := src[sy*abs(d.stride):]
		dst := reuse.Pix[y*reuse.Stride:]
		for x := 0; x < w; x++ {
			s := row[(x*d.w/w)*4:]
			p := dst[x*4 : x*4+4]
			p[0], p[1], p[2], p[3] = s[2], s[1], s[0], 255
		}
	}
	delay := d.delay
	var ticks int64
	if int32(invoke(sample, 37, uintptr(unsafe.Pointer(&ticks)))) >= 0 && ticks > 0 && ticks <= int64(MaxDuration/(100*time.Nanosecond)) {
		delay = max(20*time.Millisecond, time.Duration(ticks)*100)
	}
	return Frame{Image: reuse, Delay: delay}, nil
}
