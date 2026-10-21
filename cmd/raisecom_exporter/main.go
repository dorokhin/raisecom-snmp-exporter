// Command raisecom_exporter is a multi-target Prometheus exporter for Raisecom
// ROS switches (ISCOM2600G series), focused on PoE.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	promslogflag "github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"

	"raisecom-snmp-exporter/internal/collector"
	"raisecom-snmp-exporter/internal/config"
	"raisecom-snmp-exporter/internal/snmp"
)

const exporterName = "raisecom_exporter"

var (
	probesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: exporterName + "_probes_total",
		Help: "Probes handled, by module and result.",
	}, []string{"module", "result"})
	configReloadSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: exporterName + "_config_last_reload_successful",
		Help: "Whether the last configuration reload succeeded.",
	})
	configReloadSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: exporterName + "_config_last_reload_success_timestamp_seconds",
		Help: "Timestamp of the last successful configuration reload.",
	})
	probeSuccessDesc = prometheus.NewDesc("raisecom_probe_success",
		"Whether every collector succeeded against the target.", nil, nil)
	probeDurationDesc = prometheus.NewDesc("raisecom_probe_duration_seconds",
		"Time the probe took.", nil, nil)
)

// safeConfig allows reloading the configuration while probes are running.
type safeConfig struct {
	mu   sync.RWMutex
	path string
	cfg  *config.Config
}

func (s *safeConfig) get() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *safeConfig) reload() error {
	cfg, err := config.Load(s.path)
	if err != nil {
		configReloadSuccess.Set(0)
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	configReloadSuccess.Set(1)
	configReloadSeconds.SetToCurrentTime()
	return nil
}

func main() {
	var (
		configFile    = kingpin.Flag("config.file", "Exporter configuration file.").Default("config.yml").String()
		timeoutOffset = kingpin.Flag("scrape.timeout-offset", "Subtracted from Prometheus' scrape timeout to leave time for the response.").Default("500ms").Duration()
		checkConfig   = kingpin.Flag("config.check", "Validate the configuration and exit.").Bool()
		toolkitFlags  = webflag.AddFlags(kingpin.CommandLine, ":9840")
	)
	logCfg := &promslog.Config{}
	promslogflag.AddFlags(kingpin.CommandLine, logCfg)
	kingpin.Version(version.Print(exporterName))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger := promslog.New(logCfg)

	sc := &safeConfig{path: *configFile}
	if err := sc.reload(); err != nil {
		logger.Error("loading config", "file", *configFile, "err", err)
		os.Exit(1)
	}
	if *checkConfig {
		fmt.Println("config OK")
		return
	}
	logger.Info("starting "+exporterName, "version", version.Info(), "config", *configFile)

	prometheus.MustRegister(versioncollector.NewCollector(exporterName), probesTotal, configReloadSuccess, configReloadSeconds)

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			if err := sc.reload(); err != nil {
				logger.Error("reloading config", "err", err)
			} else {
				logger.Info("config reloaded")
			}
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}))
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		probeHandler(w, r, sc.get(), *timeoutOffset, logger)
	})
	mux.HandleFunc("/-/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		if err := sc.reload(); err != nil {
			logger.Error("reloading config", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		logger.Info("config reloaded")
		fmt.Fprintln(w, "OK")
	})
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "OK") })
	landing, err := web.NewLandingPage(web.LandingConfig{
		Name:        "Raisecom Exporter",
		Description: "Prometheus exporter for Raisecom ROS switches (PoE, interfaces, system, SFP DDM)",
		Version:     version.Info(),
		Form: web.LandingForm{
			Action: "/probe",
			Inputs: []web.LandingFormInput{
				{Label: "Target", Type: "text", Name: "target", Placeholder: "192.0.2.10"},
				{Label: "Auth", Type: "text", Name: "auth", Placeholder: "public_v2"},
				{Label: "Module", Type: "text", Name: "module", Value: "default"},
			},
		},
		Links: []web.LandingLinks{{Address: "/metrics", Text: "Exporter metrics"}},
	})
	if err != nil {
		logger.Error("building landing page", "err", err)
		os.Exit(1)
	}
	mux.Handle("/", landing)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := web.ListenAndServe(srv, toolkitFlags, logger); err != nil {
		logger.Error("http server", "err", err)
		os.Exit(1)
	}
}

func probeHandler(w http.ResponseWriter, r *http.Request, cfg *config.Config, offset time.Duration, logger *slog.Logger) {
	q := r.URL.Query()
	target := q.Get("target")
	if target == "" {
		http.Error(w, "'target' parameter is required", http.StatusBadRequest)
		return
	}
	authName := q.Get("auth")
	if authName == "" && len(cfg.Auths) == 1 {
		for name := range cfg.Auths {
			authName = name
		}
	}
	auth, ok := cfg.Auths[authName]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown auth %q", authName), http.StatusBadRequest)
		return
	}
	moduleName := q.Get("module")
	if moduleName == "" {
		moduleName = "default"
	}
	module, ok := cfg.Modules[moduleName]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown module %q", moduleName), http.StatusBadRequest)
		return
	}
	host, port, err := splitTarget(target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	params, _ := auth.Params() // validated at load time
	params.Target = host
	if port != 0 {
		params.Port = port
	}
	params.Timeout = module.Timeout
	params.Retries = *module.Retries
	params.MaxRepetitions = module.MaxRepetitions

	ctx, cancel := context.WithTimeout(r.Context(), scrapeTimeout(r, offset))
	defer cancel()
	logger = logger.With("target", target, "module", moduleName)

	start := time.Now()
	reg := prometheus.NewRegistry()
	client, err := snmp.Dial(ctx, params)
	if err != nil {
		logger.Warn("probe failed", "err", err)
		reg.MustRegister(constCollector{
			prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0),
			prometheus.MustNewConstMetric(probeDurationDesc, prometheus.GaugeValue, time.Since(start).Seconds()),
		})
		probesTotal.WithLabelValues(moduleName, "failure").Inc()
	} else {
		defer client.Close()
		reg.MustRegister(&probeCollector{
			probe: collector.Probe{Ctx: ctx, Target: &collector.Target{Client: client, Module: module, Logger: logger}},
			start: start, module: moduleName,
		})
	}
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

// probeCollector appends probe_success/duration after the collectors ran.
type probeCollector struct {
	probe  collector.Probe
	start  time.Time
	module string
}

func (p *probeCollector) Describe(chan<- *prometheus.Desc) {}

func (p *probeCollector) Collect(ch chan<- prometheus.Metric) {
	p.probe.Collect(ch)
	result, success := "success", 1.0
	if !p.probe.Success {
		result, success = "failure", 0
	}
	probesTotal.WithLabelValues(p.module, result).Inc()
	ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, success)
	ch <- prometheus.MustNewConstMetric(probeDurationDesc, prometheus.GaugeValue, time.Since(p.start).Seconds())
}

type constCollector []prometheus.Metric

func (c constCollector) Describe(chan<- *prometheus.Desc) {}

func (c constCollector) Collect(ch chan<- prometheus.Metric) {
	for _, m := range c {
		ch <- m
	}
}

// scrapeTimeout honours Prometheus' X-Prometheus-Scrape-Timeout-Seconds header.
func scrapeTimeout(r *http.Request, offset time.Duration) time.Duration {
	const fallback = 10 * time.Second
	h := r.Header.Get("X-Prometheus-Scrape-Timeout-Seconds")
	if h == "" {
		return fallback
	}
	secs, err := strconv.ParseFloat(h, 64)
	if err != nil || secs <= 0 {
		return fallback
	}
	d := time.Duration(secs*float64(time.Second)) - offset
	return max(d, time.Second)
}

// splitTarget accepts "host", "host:port", "[v6]" and "[v6]:port".
func splitTarget(t string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(t)
	if err != nil {
		// No port: strip optional IPv6 brackets.
		if len(t) > 1 && t[0] == '[' && t[len(t)-1] == ']' {
			return t[1 : len(t)-1], 0, nil
		}
		return t, 0, nil
	}
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in target %q", t)
	}
	return host, uint16(p), nil
}
