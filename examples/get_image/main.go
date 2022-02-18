package main

import (
	"bytes"
	"context"
	"flag"
	"image"
	"image/png"
	"log"
	"os"
	"os/signal"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"time"

	aravis "github.com/thinkski/go-aravis"
)

var logger *log.Logger

var defaultExposure float64
var defaultGain float64
var mu sync.Mutex

// Lock is only needed when swapping buffers
type outputBuffer struct {
	front []byte
	back *bytes.Buffer
	mu sync.Mutex
}

type imageSource struct {
	camera aravis.Camera
	mu sync.Mutex
	exposure float64
	gain float64
	width int
	height int
	payloadSize uint
	compression png.CompressionLevel
	out outputBuffer
	activeBackgroundWorkers sync.WaitGroup
}

type pool struct {
	b *png.EncoderBuffer
}

func (p *pool) Get() *png.EncoderBuffer {
	return p.b
}

func (p *pool) Put(b *png.EncoderBuffer) {
	p.b = b
}


func cameraThread(ctx context.Context, is *imageSource) error {
	// Create a stream
	stream, err := is.camera.CreateStream()
	if err != nil {
		return err
	}
	defer stream.Close()

	// Add a couple buffers
	for i := 0; i < 2; i++ {
		buffer, err := aravis.NewBuffer(is.payloadSize)
		if err != nil {
			return err
		}
		stream.PushBuffer(buffer)
	}

	encoder := &png.Encoder{
		png.BestSpeed,
		&pool{},
	}

	// Start acquisition
	is.camera.StartAcquisition()
	defer is.camera.StopAcquisition()

	time.Sleep(time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			//time.Sleep(time.Second * 10)
		}

		//logger.Print("Start image")

		buffer, err := stream.TimeoutPopBuffer(time.Second)
		if s, _ := buffer.GetStatus(); s != aravis.BUFFER_STATUS_SUCCESS {
			//logger.Printf("bad buffer: %d, %+v", s, buffer)
			stream.PushBuffer(buffer)
			continue
		}
		data, err := buffer.GetData()

		img := image.NewGray(
			image.Rectangle{image.Point{0, 0}, image.Point{is.width, is.height}},
		)
		img.Pix = data

		// Write PNG to outputBuffer
		err = encoder.Encode(is.out.back, img)
		if err != nil {
			logger.Println(err)
			logger.Printf("encode error buffer: %+v", buffer)
			stream.PushBuffer(buffer)
			continue
		}
		stream.PushBuffer(buffer)

		is.out.mu.Lock()
		is.out.front = make([]byte, is.out.back.Len())
		copy(is.out.front, is.out.back.Bytes())
		is.out.back.Reset()
		is.out.mu.Unlock()
		//logger.Print("Stop image")
	}
}

func servePNG(is *imageSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		select {
		case <-r.Context().Done():
			logger.Println("request canceled")
			return
		default:
		}

		gainStr := r.FormValue("gain");
		if gainStr != "" {
			gain, _ := strconv.ParseFloat(gainStr, 64)
			if gain != is.gain {
				is.mu.Lock()
				is.gain = gain
				is.camera.SetGain(gain)
				logger.Printf("Gain: %f", gain)
				is.mu.Unlock()
			}
		}

		exposureStr := r.FormValue("exposure")
		if exposureStr != "" {
			exposure, _ := strconv.ParseFloat(exposureStr, 64)
			if exposure != is.exposure {
				is.mu.Lock()
				is.camera.SetExposureTime(exposure)
				is.exposure = exposure
				logger.Printf("Exposure: %f", exposure)
				is.mu.Unlock()
			}
		}

		is.out.mu.Lock()
		defer is.out.mu.Unlock()
		_, err := w.Write(is.out.front)
		if err != nil {
			logger.Print(err)
		}
	})
}

func init() {
	flag.Float64Var(&defaultExposure, "e", 103656.0, "Exposure time (in us)")
	flag.Float64Var(&defaultGain, "g", 10.00000015, "Gain (in dB)")
}

func main() {
	logger = log.Default()
	logger.SetFlags(log.Ltime |	log.Lmicroseconds)
	var err error
	var numDevices uint

	flag.Parse()

	// Get devices
	aravis.UpdateDeviceList()
	if numDevices, err = aravis.GetNumDevices(); err != nil {
		logger.Fatal(err)
	}

	// Must find at least one device
	if numDevices == 0 {
		logger.Fatal("No devices found. Exiting.")
		return
	}

	name, _ := aravis.GetDeviceId(0)
	camera, _ := aravis.NewCamera(name)
	defer camera.Close()

	maxWidth, maxHeight, _ := camera.GetSensorSize()
	camera.UVSetUSBMode(aravis.USB_MODE_ASYNC)
	camera.SetRegion(0, 0, maxWidth, maxHeight)
	camera.SetExposureTimeAuto(aravis.AUTO_OFF)
	camera.SetExposureTime(defaultExposure)
	camera.SetGain(defaultGain)
	camera.SetFrameRate(0)
	camera.SetAcquisitionMode(aravis.ACQUISITION_MODE_CONTINUOUS)
	size, _ := camera.GetPayloadSize()
	_, _, width, height, _ := camera.GetRegion()


	logger.Printf("Found camera: %s Exposure: %f, Gain: %f", name, defaultExposure, defaultGain)

	is := imageSource{
		camera: camera,
		exposure: defaultExposure,
		gain: defaultGain,
		width: width,
		height: height,
		payloadSize: size,
		compression: png.BestSpeed,
	}

	is.out.back = new(bytes.Buffer)

	ctx, cancelFunc := context.WithCancel(context.Background())

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	mux := http.NewServeMux()
	mux.Handle("/0.png", servePNG(&is))
	webServer := &http.Server{
		Addr: ":8000",
		Handler: mux,
	}
	
	is.activeBackgroundWorkers.Add(2)
	go func() {
		defer is.activeBackgroundWorkers.Done()
		cameraThread(ctx, &is)
	}()
	go func() {
		defer is.activeBackgroundWorkers.Done()
		logger.Print("Listening...")
		logger.Print(webServer.ListenAndServe())
	}()

	select {
	case sig := <-signalChan:
        logger.Printf("Recieved signal: %s", sig)
	}

	cancelFunc()
	ctx, _ = context.WithTimeout(context.Background(), time.Duration(time.Second * 5))
	webServer.Shutdown(ctx)
	is.activeBackgroundWorkers.Wait()
	logger.Print("Quitting.")
}
