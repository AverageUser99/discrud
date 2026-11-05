// Package nativevideo plays bounded, self-contained MP4/H.264 loops using the
// Windows Media Foundation decoder. It never gives the decoder a network URL.
package nativevideo

import (
	"encoding/binary"
	"errors"
	"time"
)

const MaxBytes = 8 << 20
const MaxPixels = 1024 * 1024
const MemoryReservation = 32 << 20
const MaxDuration = 60 * time.Second

var ErrFormat = errors.New("unsupported MP4; expected a self-contained H.264 clip")
var ErrLimit = errors.New("MP4 exceeds preview limits (8 MiB, 1 megapixel, 60 seconds)")

type Info struct {
	Width, Height int
	Duration      time.Duration
}

func IsMP4(b []byte) bool { return len(b) >= 12 && string(b[4:8]) == "ftyp" }

// Check the container and advertised coded dimensions before invoking native
// codecs. Reject external data references, encrypted tracks and fragmented files.
func Inspect(b []byte) (Info, error) {
	var info Info
	if len(b) > MaxBytes {
		return info, ErrLimit
	}
	if !IsMP4(b) {
		return info, ErrFormat
	}
	boxes, video, moov, mdat := 0, 0, false, false
	var scan func([]byte, int) error
	scan = func(data []byte, depth int) error {
		if depth > 16 {
			return ErrLimit
		}
		for len(data) > 0 {
			boxes++
			if boxes > 4096 || len(data) < 8 {
				return ErrFormat
			}
			size := uint64(binary.BigEndian.Uint32(data))
			header := 8
			if size == 1 {
				if len(data) < 16 {
					return ErrFormat
				}
				size = binary.BigEndian.Uint64(data[8:])
				header = 16
			}
			if size == 0 {
				size = uint64(len(data))
			}
			if size < uint64(header) || size > uint64(len(data)) {
				return ErrFormat
			}
			kind := string(data[4:8])
			body := data[header:int(size)]
			switch kind {
			case "moof", "mvex", "encv", "enca":
				return ErrFormat
			case "moov":
				moov = true
				if err := scan(body, depth+1); err != nil {
					return err
				}
			case "trak", "mdia", "minf", "stbl", "dinf":
				if err := scan(body, depth+1); err != nil {
					return err
				}
			case "mdat":
				mdat = true
			case "mvhd":
				if len(body) < 20 {
					return ErrFormat
				}
				var scale uint32
				var ticks uint64
				switch body[0] {
				case 0:
					scale = binary.BigEndian.Uint32(body[12:])
					ticks = uint64(binary.BigEndian.Uint32(body[16:]))
				case 1:
					if len(body) < 32 {
						return ErrFormat
					}
					scale = binary.BigEndian.Uint32(body[20:])
					ticks = binary.BigEndian.Uint64(body[24:])
				default:
					return ErrFormat
				}
				if scale == 0 || ticks == 0 || ticks > uint64(scale)*60 {
					return ErrLimit
				}
				info.Duration = time.Duration(ticks/uint64(scale))*time.Second + time.Duration(ticks%uint64(scale))*time.Second/time.Duration(scale)
			case "stsd":
				if len(body) < 8 {
					return ErrFormat
				}
				entries := body[8:]
				count := binary.BigEndian.Uint32(body[4:])
				if count > 16 {
					return ErrLimit
				}
				for i := uint32(0); i < count; i++ {
					if len(entries) < 8 {
						return ErrFormat
					}
					n := int(binary.BigEndian.Uint32(entries))
					if n < 8 || n > len(entries) {
						return ErrFormat
					}
					codec := string(entries[4:8])
					if codec == "encv" || codec == "enca" {
						return ErrFormat
					}
					if codec == "avc1" || codec == "avc3" {
						if n < 86 {
							return ErrFormat
						}
						video++
						info.Width = int(binary.BigEndian.Uint16(entries[32:]))
						info.Height = int(binary.BigEndian.Uint16(entries[34:]))
						if info.Width < 1 || info.Height < 1 || info.Width*info.Height > MaxPixels {
							return ErrLimit
						}
					}
					entries = entries[n:]
				}
			case "dref":
				if len(body) < 8 {
					return ErrFormat
				}
				refs := body[8:]
				count := binary.BigEndian.Uint32(body[4:])
				if count > 16 {
					return ErrLimit
				}
				for i := uint32(0); i < count; i++ {
					if len(refs) < 12 {
						return ErrFormat
					}
					n := int(binary.BigEndian.Uint32(refs))
					if n < 12 || n > len(refs) || string(refs[4:8]) != "url " || binary.BigEndian.Uint32(refs[8:12]) != 1 {
						return ErrFormat
					}
					refs = refs[n:]
				}
			}
			data = data[int(size):]
		}
		return nil
	}
	err := scan(b, 0)
	if err != nil {
		return info, err
	}
	if !moov || !mdat || video != 1 || info.Duration <= 0 {
		return info, ErrFormat
	}
	return info, nil
}
