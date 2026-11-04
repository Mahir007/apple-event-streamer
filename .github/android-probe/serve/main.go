// Command serve stands in for the loopback playlist server: it serves the
// files in a folder on 127.0.0.1 and logs each request, to show how often and
// when the mpv app asks for the playlist.
package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	dir, addr := os.Args[1], "127.0.0.1:"+os.Args[2]
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%s %s %s range=%q ua=%q\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path, r.Header.Get("Range"), r.UserAgent())
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
	})
	fmt.Fprintln(os.Stderr, http.ListenAndServe(addr, nil))
	os.Exit(1)
}
