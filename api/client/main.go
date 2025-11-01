// client/main.go
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

func udsHTTPClient(sockPath string) *http.Client {
	dialer := &net.Dialer{
		Timeout:   2 * time.Second,
		KeepAlive: 60 * time.Second,
	}
	transport := &http.Transport{
		// Reuso agressivo de conexão
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false, // deixe true para binários; false para JSON grandes

		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Ignora addr; conecta ao socket Unix
			return dialer.DialContext(ctx, "unix", sockPath)
		},
		// TLS desnecessário no UDS
	}
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}
}

func main() {
	c := udsHTTPClient("/tmp/app_api.sock")

	// Use host “falso” estável; precisa ser consistente para hit do pool.
	req, _ := http.NewRequest("GET", "http://unix/api/health", nil)
	// Define Host lógico (opcional, caso o servidor distinga virtual hosts)
	req.Host = "internal.local"

	resp, err := c.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status)
	fmt.Println(string(b))
}
