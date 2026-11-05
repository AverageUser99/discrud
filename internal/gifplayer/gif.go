// Package gifplayer provides bounded GIF decoding and allocation-free playback.
// It has no platform or third-party dependencies. A Clip is immutable; Players
// own their compositing state and are driven by the host's shared scheduler.
package gifplayer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"sync"
	"time"
)

const (
	MaxBytes       = 8 << 20
	MaxPixels      = 1024 * 1024
	MaxFrames      = 256
	MaxFramePixels = 24 << 20
	MaxMemory      = 32 << 20
	MinDelay       = 20 * time.Millisecond
)

var ErrLimit = errors.New("GIF exceeds preview limits")
var ErrFormat = errors.New("invalid GIF")

// Inspect walks the GIF block structure BEFORE DecodeAll can allocate any frame
// pixels. Logical screen dimensions alone do not bound an animated GIF's memory.
// Memory conservatively includes palettes/metadata, canvas, disposal backup and
// the native 360x240 display bitmap, as well as the compressed input.
func Inspect(data []byte) (memory int, err error) {
	if len(data) > MaxBytes {
		return 0, ErrLimit
	}
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return 0, ErrFormat
	}
	u16 := func(at int) int { return int(binary.LittleEndian.Uint16(data[at : at+2])) }
	w, h := u16(6), u16(8)
	if w == 0 || h == 0 || w*h > MaxPixels {
		return 0, ErrLimit
	}
	pos := 13
	if data[10]&128 != 0 {
		pos += 3 * (1 << ((data[10] & 7) + 1))
	}
	skipBlocks := func() bool {
		for pos < len(data) {
			n := int(data[pos])
			pos++
			if n == 0 {
				return true
			}
			if n > len(data)-pos {
				return false
			}
			pos += n
		}
		return false
	}
	frames, pixels := 0, 0
	for pos < len(data) {
		kind := data[pos]
		pos++
		switch kind {
		case 0x21: // extension label, then bounded sub-blocks
			if pos >= len(data) {
				return 0, ErrFormat
			}
			pos++
			if !skipBlocks() {
				return 0, ErrFormat
			}
		case 0x2c:
			if len(data)-pos < 9 {
				return 0, ErrFormat
			}
			x, y, fw, fh := u16(pos), u16(pos+2), u16(pos+4), u16(pos+6)
			if fw == 0 || fh == 0 || x+fw > w || y+fh > h {
				return 0, ErrFormat
			}
			frames++
			pixels += fw * fh
			if frames > MaxFrames || pixels > MaxFramePixels {
				return 0, ErrLimit
			}
			flags := data[pos+8]
			pos += 9
			if flags&128 != 0 {
				pos += 3 * (1 << ((flags & 7) + 1))
			}
			if pos >= len(data) {
				return 0, ErrFormat
			}
			pos++ // LZW minimum code size
			if !skipBlocks() {
				return 0, ErrFormat
			}
		case 0x3b:
			if frames == 0 {
				return 0, ErrFormat
			}
			memory = len(data) + pixels + frames*8192 + w*h*8 + 360*240*4
			if memory > MaxMemory {
				return 0, ErrLimit
			}
			return memory, nil
		default:
			return 0, ErrFormat
		}
	}
	return 0, ErrFormat
}

type Clip struct {
	g      *gif.GIF
	memory int
}

func Decode(data []byte) (*Clip, error) {
	memory, err := Inspect(data)
	if err != nil {
		return nil, err
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return &Clip{g: g, memory: memory}, nil
}
func (c *Clip) Size() image.Point { return image.Pt(c.g.Config.Width, c.g.Config.Height) }
func (c *Clip) Memory() int       { return c.memory }
func (c *Clip) Frames() int       { return len(c.g.Image) }

type Player struct {
	clip           *Clip
	canvas         *image.RGBA
	backup         []byte
	palettes       [][256]uint32 // packed RGBA; GIF alpha is binary
	background     *image.Uniform
	index, repeats int
	done           bool
}

func New(c *Clip) *Player {
	p := &Player{clip: c, canvas: image.NewRGBA(image.Rectangle{Max: c.Size()}), background: image.NewUniform(color.Transparent)}
	// Transparent animations clear to transparency, opaque ones to the GIF's
	// global background index. The native widget flattens transparency to the UI.
	transparent := false
	for _, v := range c.g.Image[0].Palette {
		_, _, _, a := v.RGBA()
		if a == 0 {
			transparent = true
			break
		}
	}
	if pal, ok := c.g.Config.ColorModel.(color.Palette); ok && !transparent && int(c.g.BackgroundIndex) < len(pal) {
		p.background = image.NewUniform(pal[c.g.BackgroundIndex])
	}
	for _, disposal := range c.g.Disposal {
		if disposal == gif.DisposalPrevious {
			p.backup = make([]byte, len(p.canvas.Pix))
			break
		}
	}
	p.palettes = make([][256]uint32, c.Frames())
	for i, f := range c.g.Image {
		for j, v := range f.Palette {
			r, g, b, a := v.RGBA()
			if a != 0 {
				p.palettes[i][j] = uint32(r>>8) | uint32(g>>8)<<8 | uint32(b>>8)<<16 | 0xff000000
			}
		}
	}
	p.clear(p.canvas.Bounds())
	p.render()
	return p
}
func (p *Player) clear(r image.Rectangle) {
	draw.Draw(p.canvas, r, p.background, image.Point{}, draw.Src)
}
func (p *Player) disposal() byte {
	if len(p.clip.g.Disposal) > p.index {
		return p.clip.g.Disposal[p.index]
	}
	return 0
}
func (p *Player) render() {
	if p.disposal() == gif.DisposalPrevious {
		copy(p.backup, p.canvas.Pix)
	}
	f := p.clip.g.Image[p.index]
	palette := &p.palettes[p.index]
	// GIF palettes contain only opaque colors and a transparent index. A
	// precomputed lookup avoids generic per-pixel color interface conversions.
	r := f.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := f.Pix[(y-r.Min.Y)*f.Stride:][:r.Dx()]
		dst := p.canvas.Pix[y*p.canvas.Stride+r.Min.X*4:][:r.Dx()*4]
		for x, index := range src {
			if packed := palette[index]; packed != 0 {
				binary.LittleEndian.PutUint32(dst[x*4:], packed)
			}
		}
	}
}
func (p *Player) Image() *image.RGBA { return p.canvas }
func (p *Player) Index() int         { return p.index }
func (p *Player) Done() bool         { return p.done || p.clip.Frames() < 2 }
func (p *Player) Delay() time.Duration {
	return max(MinDelay, time.Duration(p.clip.g.Delay[p.index])*10*time.Millisecond)
}

// Step composites at most one frame; it never allocates pixel buffers. The host
// pauses deadlines when hidden instead of running an unbounded catch-up loop.
func (p *Player) Step() bool {
	if p.Done() {
		return false
	}
	next := p.index + 1
	if next == p.clip.Frames() {
		loops := p.clip.g.LoopCount
		if loops < 0 || loops > 0 && p.repeats >= loops {
			p.done = true
			return false
		}
		p.repeats++
		next = 0
	}
	switch p.disposal() {
	case gif.DisposalBackground:
		p.clear(p.clip.g.Image[p.index].Bounds())
	case gif.DisposalPrevious:
		copy(p.canvas.Pix, p.backup)
	}
	if next == 0 {
		p.clear(p.canvas.Bounds())
	}
	p.index = next
	p.render()
	return true
}

// Budget accounts for both decoding jobs and retained players across channels.
// Reserve before decoding; release only when the player/native bitmap is gone.
type Budget struct {
	mu    sync.Mutex
	used  int
	Limit int
}

func (b *Budget) Reserve(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > b.Limit-b.used {
		return false
	}
	b.used += n
	return true
}
func (b *Budget) Release(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.used < 0 {
		panic("gifplayer: unbalanced budget")
	}
}
func (b *Budget) Used() int      { b.mu.Lock(); defer b.mu.Unlock(); return b.used }
func (b *Budget) Available() int { b.mu.Lock(); defer b.mu.Unlock(); return b.Limit - b.used }
