package main

import (
	"errors"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/daskol/url-shortener-go/core"
)

var configPath = flag.String("config", "", "Path to *.toml config.")
var urlStorageKind = flag.String("url-storage", "map", "Set how to store URLs.")
var uriLength = flag.Int("uri-length", 8, "Length of randomly generated URI.")
var ttl = flag.Int64("ttl", 3600, "URI's time to live.")
var host = flag.String("host", "localhost", "Address to listen.")
var port = flag.Int("port", 8080, "Port to listen.")
var hostname = flag.String("hostname", "", "Force host name definition for URL building.")
var boltDatabase = flag.String("bolt-db", "url-storage-bolt.db", "Path to Bolt DB database file.")

type Config struct {
	Host string
	Port int

	ExpiringTime time.Duration `toml:"expiring_time"`
	HostName     string        `toml:"host_name"`
	UriLength    int           `toml:"uri_length"`
	UrlStorage   string        `toml:"url_storage"`

	Bolt BoltStorageConfig `toml:"bolt-storage"`
}

type BoltStorageConfig struct {
	Database string `toml:"database"`
}

var config Config

var urlStorage core.UrlStorage

var tplIndexFiles = []string{"templates/index.html"}
var tplIndex = template.Must(template.ParseFiles(tplIndexFiles...))

// getHost tries its best to return the request host.
func getHost(r *http.Request) string {
	if r.URL.IsAbs() {
		host := r.Host
		// Slice off any port information.
		if i := strings.Index(host, ":"); i != -1 {
			host = host[:i]
		}
		return host
	}
	return r.URL.Host
}

// formatRequest generates ascii representation of a request
func formatRequest(r *http.Request) string {
	// Create return string
	var request []string
	// Add the request string
	url := fmt.Sprintf("%v %v %v", r.Method, r.URL, r.Proto)
	request = append(request, url)
	// Add the host
	request = append(request, fmt.Sprintf("Host: %v", r.Host))
	// Loop through headers
	for name, headers := range r.Header {
		name = strings.ToLower(name)
		for _, h := range headers {
			request = append(request, fmt.Sprintf("%v: %v", name, h))
		}
	}

	// If this is a POST, add post data
	if r.Method == "POST" {
		r.ParseForm()
		request = append(request, "\n")
		request = append(request, r.Form.Encode())
	}
	// Return the request as a string
	return strings.Join(request, "\n")
}

func extractHostname(r *http.Request) string {
	var hostname string

	if schema, ok := r.Header["X-Forwarded-Proto"]; ok {
		hostname = schema[0]
	} else {
		hostname = "http"
	}

	hostname += "://"

	if forwarded_host, ok := r.Header["X-Forwarded-Host"]; ok {
		hostname += forwarded_host[0]
	} else if len(r.Host) > 0 {
		hostname += r.Host
	}

	if config.HostName != "" {
		hostname = config.HostName	
	}

	return hostname
}

func HandleShortRequest(w http.ResponseWriter, r *http.Request) {
	shorten := func() (string, bool) {
		url := r.FormValue("url")

		if len(url) == 0 {
			http.Error(w, "No url to shorten.", http.StatusBadRequest)
			return "", false
		}

		uri := urlStorage.Put(core.Url(url), config.ExpiringTime)
		hostname := extractHostname(r)
		location := hostname + string(uri)

		return location, true
	}

	switch r.Method {
	case "POST":
		if location, ok := shorten(); ok {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusCreated)
		}
	case "GET":
		if location, ok := shorten(); ok {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(location))
		}
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}

}

func HandleExpandRequest(w http.ResponseWriter, r *http.Request) {
	linkId := "/" + string(path.Base(r.URL.Path))
	// linkId := r.URL.Path

	if url, ok := urlStorage.Get(core.Uri(linkId)); ok {
		w.Header().Set("Location", string(url))
		w.WriteHeader(http.StatusFound)
	} else if r.URL.Path == "/" {
		if err := tplIndex.Execute(w, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		http.Error(w, "", http.StatusNotFound)
	}
}

func ReadConfig(path string) Config {
	log.Printf("read config from %s\n", path)

	config := Config{
		Host:         *host,
		Port:         *port,
		HostName:     *hostname,
		ExpiringTime: time.Duration(*ttl) * time.Second,
		UriLength:    *uriLength,
		UrlStorage:   *urlStorageKind,
		Bolt: BoltStorageConfig{
			Database: *boltDatabase,
		},
	}

	if len(path) == 0 {
		return config
	}

	if _, err := os.Stat(path); err != nil {
		log.Fatal("Config file is missing: ", path)
	}

	if _, err := toml.DecodeFile(path, &config); err != nil {
		log.Fatal(err)
	}

	config.ExpiringTime *= time.Second

	if config.ExpiringTime <= 0 {
		log.Println("ttl of url is set to store urls forever")
	}

	return config
}

func NewUrlStorage(config *Config) (core.UrlStorage, error) {
	switch config.UrlStorage {
	case "map":
		return core.NewMapStorage(config.ExpiringTime, config.UriLength)
	case "bolt":
		return core.NewBoltStorage(
			config.ExpiringTime,
			config.UriLength,
			config.Bolt.Database)
	default:
		return nil, errors.New("unknown storage " + config.UrlStorage)
	}
}

func main() {
	flag.Parse()

	log.Println("URL Shortener")

	config = ReadConfig(*configPath)
	storage, err := NewUrlStorage(&config)

	if err != nil {
		log.Fatal(err)
	} else {
		log.Println("use storage: " + config.UrlStorage)
		urlStorage = storage
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/shorten/", HandleShortRequest)
	mux.HandleFunc("/", HandleExpandRequest)

	server := &http.Server{
		Addr:    config.Host + ":" + strconv.Itoa(config.Port),
		Handler: mux,
	}

	log.Fatal(server.ListenAndServe())
}
