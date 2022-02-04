package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"net/http"
	"time"

	aravis "github.com/thinkski/go-aravis"
)

var exposureTime float64
var gain float64

func servePNG(camera aravis.Camera) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxWidth, maxHeight, _ := camera.GetSensorSize()
		camera.UVSetUSBMode(aravis.USB_MODE_ASYNC)
		camera.SetRegion(0, 0, maxWidth, maxHeight)
		camera.SetExposureTimeAuto(aravis.AUTO_OFF)
		camera.SetExposureTime(exposureTime)
		camera.SetGain(gain)
		//camera.SetFrameRate(3.75)
		camera.SetAcquisitionMode(aravis.ACQUISITION_MODE_SINGLE_FRAME)
		size, _ := camera.GetPayloadSize()
		_, _, width, height, _ := camera.GetRegion()

		// Create a stream
		stream, err := camera.CreateStream()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		defer stream.Close()

		// Add a buffer
		buffer, err := aravis.NewBuffer(size)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		stream.PushBuffer(buffer)

		// Start acquisition
		camera.StartAcquisition()
		defer camera.StopAcquisition()

		buffer, err = stream.TimeoutPopBuffer(time.Second)
		if s, _ := buffer.GetStatus(); s != aravis.BUFFER_STATUS_SUCCESS {
			http.Error(w, "buffer error", http.StatusInternalServerError)
			return
		}
		data, err := buffer.GetData()

		img := image.NewGray(
			image.Rectangle{image.Point{0, 0}, image.Point{width, height}},
		)
		img.Pix = data

		// Write PNG to client
		err = png.Encode(w, img)
		if err != nil {
			log.Println(err)
		}
	})
}

func init() {
	flag.Float64Var(&exposureTime, "e", 10000, "Exposure time (in us)")
	flag.Float64Var(&gain, "g", 16, "Gain (in dB)")
}

func main() {
	var err error
	var numDevices uint

	flag.Parse()

	// Get devices
	aravis.UpdateDeviceList()
	if numDevices, err = aravis.GetNumDevices(); err != nil {
		log.Fatal(err)
	}

	// Must find at least one device
	if numDevices == 0 {
		log.Fatal("No devices found. Exiting.")
		return
	}

	for i := uint(0); i < numDevices; i++ {
		name, _ := aravis.GetDeviceId(i)
		camera, _ := aravis.NewCamera(name)
		defer camera.Close()

		http.Handle(fmt.Sprintf("/%d.png", i), servePNG(camera))
	}

	log.Println("Listening...")
	log.Fatal(http.ListenAndServe(":8000", nil))
}
