//go:build go1.20
// +build go1.20

package main

import (
	"bytes"
	"embed"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

//go:embed web/*
var web embed.FS

// -------------- 配置 --------------
type Config struct {
	TEMSName   string        `yaml:"tems_name"`
	ListenAddr string        `yaml:"listen_addr"`
	TEPSURL    string        `yaml:"teps_url"`
	Interval   time.Duration `yaml:"interval"`
}

// -------------- 数据结构 --------------
type Metric struct {
	Hostname  string                 `json:"hostname"`
	IP        string                 `json:"ip"`
	CPU       float64                `json:"cpu_percent"`
	Mem       float64                `json:"mem_percent"`
	Disk      float64                `json:"disk_percent"`
	Network   map[string]interface{} `json:"network"`
	Processes []interface{}          `json:"processes"`
	LastSeen  int64                  `json:"last_seen"`
}

// -------------- 内存存储 --------------
var (
	cfg      Config
	metrics  = make(map[string]Metric) // key = hostname
	metricsM sync.RWMutex
)

func loadConfig(path string) Config {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}
	var c Config
	_ = yaml.Unmarshal(b, &c)
	return c
}

// -------------- 接收 Agent 数据 --------------
func metricsHandler(w http.ResponseWriter, r *http.Request) {
	var m Metric
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	m.LastSeen = time.Now().Unix()
	metricsM.Lock()
	metrics[m.Hostname] = m
	metricsM.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// -------------- 查询 API --------------
func apiHandler(w http.ResponseWriter, r *http.Request) {
	metricsM.RLock()
	defer metricsM.RUnlock()
	_ = json.NewEncoder(w).Encode(metrics)
}

// -------------- 推送给 TEPS --------------
func pushToTEPS() {
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		<-ticker.C
		metricsM.RLock()
		var list []Metric
		for _, v := range metrics {
			list = append(list, v)
		}
		metricsM.RUnlock()

		payload := map[string]interface{}{
			"tems_name": cfg.TEMSName,
			"timestamp": time.Now().Unix(),
			"agents":    list,
		}
		b, _ := json.Marshal(payload)
		http.Post(cfg.TEPSURL, "application/json", bytes.NewReader(b))
	}
}

// -------------- 启动 Web --------------
func webHandler() http.Handler {
	return http.StripPrefix("/web/", http.FileServer(http.FS(web)))
}

// -------------- main --------------
func main() {
	cfg = loadConfig("config.yaml")
	go pushToTEPS()

	r := mux.NewRouter()
	r.HandleFunc("/metrics", metricsHandler).Methods("POST")
	r.HandleFunc("/api", apiHandler)
	r.PathPrefix("/web/").Handler(webHandler())

	log.Printf("TEMS %s ready | /metrics (Agent) | /web (Dashboard) -> TEPS %s",
		cfg.TEMSName, cfg.TEPSURL)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, r))
}
