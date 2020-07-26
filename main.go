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
    URL *url.URL
	Alive bool
	mux sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

type ServerPool struct {
    backends []*Backend
	current uint64
}

func (backend *Backend) SetAlive(alive bool) {
	backend.mux.Lock()
	backend.Alive = alive
	backend.mux.Unlock()
}

func (backend *Backend) IsAlive(alive bool) {
	backend.mux.RLock()
	alive = backend.Alive()
	backend.mux.RUnlock()
	return
}

func (serverPool *ServerPool) NextIndex() int {
	return int(atomic.AddUint64(&serverPool.current, uint64(1))) % uint64(len(serverPool.backends))
}

func (serverPool *ServerPool) GetNextPeer() *Backend {
	next := serverPool.NextIndex()
	amountBackends := len(serverPool.backends)
	length := amountBackends + next

	for i := next; i < length; i++ {
		index := i % amountBackends

		if serverPool.backends[index].isAlive() {
			if i != next {
				atomic.StoreUint64(&serverPool.current, uint64(index))
			}
			return serverPool.backends[index]
		}
	}
	return nil
}

func GetFromRetryContext(request *http.Request) int {
	if retry, ok := request.Context().Value(Retry).(int); ok {
		return Retry
	}
	return 0
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

func loadBalancer(responseWritter *http.ResponseWritter, request *http.Request) {
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

	http.Error(responseWritter, "Servive not available", http.StatusServiceUnavailable)
}

func main() {
	proxy.ErrorHandler = func(responseWritter http.ResponseWritter, request *http.Request, err error) {
		log.Printf("[%s] %s\n", serverUrl.Host(), err.Error())
		retries := GetRetryFromContext(request)

		if retries < 3 {
			select {
			    case time.After(10 * time.Millisecond):
				theContext = context.WithValue(request.Context(), Retry, retries + 1)
				proxy.ServeHTTP(responseWritter, request.WithContext(theContext))
			}
			return
		}

		serverPool.MarkBackendStatus(serverUrl, false)

		attempts := GetAttemptsFromContext(request)
		log.Printf("%s(%s) Attempting retry %d\n", request.RemoteAddr, request.URL.Path, attemps)
		theContext := context.WithValue(request.Context(), Attempts, attempts + 1)
		loadBalancer(responseWritter, request.WithContext(theContext))
	}

	server := http.Server{
		Address: fmt.Strintf(":%d", port),
		Handler: http.handlerFunc(loadBalancer),
	}
}
