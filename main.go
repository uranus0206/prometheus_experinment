package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/exp/rand"
)

type Device struct {
	ID       int    `json:"id"`
	Mac      string `json:"mac"`
	Firmware string `json:"firmware"`
}

type deviceMetrics struct {
	devices      prometheus.Gauge
	info         *prometheus.GaugeVec
	upgrades     *prometheus.CounterVec
	duration     *prometheus.HistogramVec
	durationSumy *prometheus.SummaryVec
}

func RegisterDeviceMetrics(reg prometheus.Registerer) *deviceMetrics {
	d := &deviceMetrics{
		devices: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "prometheus_app",
			Name:      "device_count",
			Help:      "Number of devices",
		}),
		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "prometheus_app",
			Name:      "version_info",
			Help:      "Version information",
		},
			[]string{"version"}),
		upgrades: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "prometheus_app",
			Name:      "device_upgrades_total",
			Help:      "Number of device upgrades",
		}, []string{"type"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "prometheus_app",
			Name:      "request_duration_seconds",
			Help:      "Request duration in seconds",
			Buckets:   []float64{0.1, 0.15, 0.2, 0.25, 0.3},
		}, []string{"status", "method", "path"}),
		durationSumy: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Namespace:  "prometheus_app",
			Name:       "request_duration_seconds_summary",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		}, []string{"status", "method", "path"}),
	}

	reg.MustRegister(d.devices, d.info, d.upgrades, d.duration, d.durationSumy)

	return d
}

var (
	devices []Device
	version = "1.0.0"
)

func init() {
	devices = append(devices, Device{
		ID:       1,
		Mac:      "AA:BB:CC:DD:EE:01",
		Firmware: "1.0.0",
	}, Device{
		ID:       2,
		Mac:      "AA:BB:CC:DD:EE:02",
		Firmware: "1.0.0",
	})
}

func main() {
	println("hello world")

	r := prometheus.NewRegistry() // registrer is also a prometheus.Gatherer
	r.MustRegister(collectors.NewGoCollector())
	m := RegisterDeviceMetrics(r)
	m.devices.Set(float64(len(devices)))
	m.info.With(prometheus.Labels{"version": version}).Set(1)

	promHandler := promhttp.HandlerFor(r, promhttp.HandlerOpts{})

	// http.Handle("/metrics", promHandler)
	// // http.Handle("/metrics", promhttp.Handler())
	//
	// http.HandleFunc("/devices", getDevices)
	// http.ListenAndServe(":8081", nil)
	//
	pMux := http.NewServeMux()
	pMux.Handle("/metrics", promHandler)

	dMux := http.NewServeMux()
	// dMux.HandleFunc("/devices", getDevices)
	// dMux.HandleFunc("/devices", registerDevice)
	rdh := registerDeviceHandler{metrics: m}
	mrdh := middleware(rdh, m)

	dMux.Handle("/devices", mrdh)

	udh := upgradeDeviceHandler{m}
	mudh := middleware(udh, m)
	dMux.Handle("/devices/{id}", mudh)

	go func() {
		log.Fatal(http.ListenAndServe(":8080", dMux))
	}()

	go func() {
		log.Fatal(http.ListenAndServe(":8081", pMux))
	}()

	select {}
}

// get device handler
func getDevices(w http.ResponseWriter, r *http.Request, m *deviceMetrics) {
	now := time.Now()

	b, err := json.Marshal(devices)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sleep(200)

	m.duration.
		With(prometheus.Labels{"status": "200", "method": r.Method, "path": r.URL.Path}).
		Observe(time.Since(now).Seconds())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

func createDevices(w http.ResponseWriter, r *http.Request, m *deviceMetrics) {
	var device Device

	err := json.NewDecoder(r.Body).Decode(&device)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	devices = append(devices, device)

	m.devices.Set(float64(len(devices)))
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("Devcie created."))
}

//	func registerDevice(w http.ResponseWriter, r *http.Request) {
//		switch r.Method {
//		case "GET":
//			getDevices(w, r)
//		case "POST":
//			createDevices(w, r)
//		default:
//			w.Header().Set("Allow", "GET, POST")
//			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
//		}
//	}
type registerDeviceHandler struct {
	metrics *deviceMetrics
}

func (rdh registerDeviceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		getDevices(w, r, rdh.metrics)
	case "POST":
		createDevices(w, r, rdh.metrics)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func upgradeDevice(w http.ResponseWriter, r *http.Request, m *deviceMetrics) {
	path := strings.TrimPrefix(r.URL.Path, "/devices/")

	id, err := strconv.Atoi(path)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}

	var device Device
	err = json.NewDecoder(r.Body).Decode(&device)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for i := range devices {
		if devices[i].ID == id {
			devices[i].Firmware = device.Firmware
		}
	}

	sleep(1000)

	m.upgrades.With(prometheus.Labels{"type": "router"}).Inc()

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("Device upgraded."))
}

type upgradeDeviceHandler struct {
	metrics *deviceMetrics
}

func (udh upgradeDeviceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "PUT":
		upgradeDevice(w, r, udh.metrics)
	default:
		w.Header().Set("Only Allow", "PUT")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func sleep(ms int) {
	rand.Seed(uint64(time.Now().Nanosecond()))
	now := time.Now()
	n := rand.Intn(ms + now.Second())
	time.Sleep(time.Duration(n) * time.Millisecond)
}

func middleware(next http.Handler, m *deviceMetrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		next.ServeHTTP(w, r)
		m.durationSumy.With(prometheus.Labels{
			"status": w.Header().Get("statusCode"),
			"method": r.Method,
			"path":   r.URL.Path,
		}).
			Observe(time.Since(now).Seconds())
	})
}
