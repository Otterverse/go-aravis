package aravis

// #cgo pkg-config: aravis-0.8
// #include <arv.h>
// #include <stdlib.h>
import "C"

type Device struct {
	device *C.struct__ArvDevice
}
