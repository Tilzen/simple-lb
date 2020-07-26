package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Attempts int = iota
	Retry
)

type Backend struct {
	URL          *url.URL
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

func (backend *Backend) SetAlive(alive bool) {
	backend.mux.Lock()
	backend.Alive = alive
	backend.mux.Unlock()
}

func (backend *Backend) IsAlive() (alive bool) {
	backend.mux.RLock()
	alive = backend.Alive
	backend.mux.RUnlock()
	return
}

type ServerPool struct {
	backends []*Backend
	current  uint64
}

func (serverPool *ServerPool) AddBackend(backend *Backend) {
	serverPool.backends = append(serverPool.backends, backend)
}

func (serverPool *ServerPool) NextIndex() int {
	return int(atomic.AddUint64(&serverPool.current, uint64(1)) % uint64(len(serverPool.backends)))
}

func (serverPool *ServerPool) MarkBackendStatus(backendUrl *url.URL, alive bool) {
	for _, backend := range serverPool.backends {
		if backend.URL.String() == backendUrl.String() {
			backend.SetAlive(alive)
			break
		}
	}
}

func (serverPool *ServerPool) GetNextPeer() *Backend {
	next := serverPool.NextIndex()
	amountBackends := len(serverPool.backends)
	length := amountBackends + next

	for i := next; i < length; i++ {
		index := i % amountBackends

		if serverPool.backends[index].IsAlive() {
			if i != next {
				atomic.StoreUint64(&serverPool.current, uint64(index))
			}
			return serverPool.backends[index]
		}
	}
	return nil
}

func (serverPool *ServerPool) HealthCheck() {
	for _, backend := range serverPool.backends {
		status := "up"
		alive := isBackendAlive(backend.URL)
		backend.SetAlive(alive)

		if !alive {
			status = "down"
		}
		log.Printf("%s [%s]\n", backend.URL, status)
	}
}

func GetAttemptsFromContext(request *http.Request) int {
	if attempts, ok := request.Context().Value(Attempts).(int); ok {
		return attempts
	}
	return 1
}

func GetRetryFromContext(request *http.Request) int {
	if retry, ok := request.Context().Value(Retry).(int); ok {
		return retry
	}
	return 0
}

func loadBalancer(responseWritter http.ResponseWriter, request *http.Request) {
	attempts := GetAttemptsFromContext(request)
	if attempts > 3 {
		log.Printf("%s(%s) Max attempts reached, terminating\n", request.RemoteAddr, request.URL.Path)
		http.Error(responseWritter, "Service not available", http.StatusServiceUnavailable)
		return
	}

	peer := serverPool.GetNextPeer()
	if peer != nil {
		peer.ReverseProxy.ServeHTTP(responseWritter, request)
		return
	}
	http.Error(responseWritter, "Service not available", http.StatusServiceUnavailable)
}

func isBackendAlive(urlBackend *url.URL) bool {
	timeout := 2 * time.Second
	connection, err := net.DialTimeout("tcp", urlBackend.Host, timeout)
	if err != nil {
		log.Println("Site unreachable, error: ", err)
		return false
	}
	_ = connection.Close()
	return true
}

func healthCheck() {
	timer := time.NewTicker(time.Minute * 2)
	for {
		select {
		case <- timer.C:
			log.Println("Starting health check...")
			serverPool.HealthCheck()
			log.Println("Health check completed")
		}
	}
}

var serverPool ServerPool

func main() {
	var serverList string
	var port int
	flag.StringVar(&serverList, "backends", "", "Load balanced backends, use commas to separate")
	flag.IntVar(&port, "port", 3030, "Port to serve")
	flag.Parse()

	if len(serverList) == 0 {
		log.Fatal("Please provide one or more backends to load balance")
	}

	tokens := strings.Split(serverList, ",")
	for _, token := range tokens {
		serverUrl, err := url.Parse(token)
		if err != nil {
			log.Fatal(err)
		}

		proxy := httputil.NewSingleHostReverseProxy(serverUrl)
		proxy.ErrorHandler = func(responseWriter http.ResponseWriter, request *http.Request, err error) {
			log.Printf("[%s] %s\n", serverUrl.Host, err.Error())

			retries := GetRetryFromContext(request)
			if retries < 3 {
				select {
				case <-time.After(10 * time.Millisecond):
					theContext := context.WithValue(request.Context(), Retry, retries + 1)
					proxy.ServeHTTP(responseWriter, request.WithContext(theContext))
				}
				return
			}

			serverPool.MarkBackendStatus(serverUrl, false)

			attempts := GetAttemptsFromContext(request)
			log.Printf("%s(%s) Attempting retry %d\n", request.RemoteAddr, request.URL.Path, attempts)

			theContext := context.WithValue(request.Context(), Attempts, attempts + 1)
			loadBalancer(responseWriter, request.WithContext(theContext))
		}

		serverPool.AddBackend(&Backend{
			URL:          serverUrl,
			Alive:        true,
			ReverseProxy: proxy,
		})
		log.Printf("Configured server: %s\n", serverUrl)
	}

	server := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: http.HandlerFunc(loadBalancer),
	}

	go healthCheck()

	log.Printf("Load Balancer started at :%d\n", port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
