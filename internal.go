package aravis

// #cgo pkg-config: aravis-0.8
// #include <arv.h>
import "C"

func toBool(x C.gboolean) bool {
	if int(x) != 0 {
		return true
	} else {
		return false
	}
}
