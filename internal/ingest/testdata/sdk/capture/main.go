// Command capture receives one Sentry event envelope from a real SDK and writes
// it, decompressed, to a file. It exits after that envelope or the timeout, so
// it never stays resident.
package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	// All interfaces, not loopback: the SDK runs in a container and reaches this
	// listener through host.docker.internal, which loopback would refuse.
	addr := flag.String("addr", ":18081", "listen address")
	out := flag.String("out", "", "file to write the captured envelope to")
	timeout := flag.Duration("timeout", 3*time.Minute, "give up after this long")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "capture: -out is required")
		os.Exit(2)
	}

	captured := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body, err = io.ReadAll(reader); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
		if !bytes.Contains(body, []byte(`"type":"event"`)) {
			return
		}
		select {
		case <-captured:
		default:
			if err := os.WriteFile(*out, body, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "capture:", err)
				os.Exit(1)
			}
			close(captured)
		}
	})

	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "capture:", err)
			os.Exit(1)
		}
	}()

	select {
	case <-captured:
		time.Sleep(200 * time.Millisecond)
	case <-time.After(*timeout):
		fmt.Fprintln(os.Stderr, "capture: timed out waiting for an event envelope")
		os.Exit(1)
	}
	_ = server.Close()
}
