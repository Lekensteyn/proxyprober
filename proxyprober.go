package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type CountingWriter struct {
	io.Writer
	Written int
}

func (w *CountingWriter) Write(b []byte) (int, error) {
	w.Written += len(b)
	return len(b), nil
}

func (w *CountingWriter) Count() int {
	count := w.Written
	w.Written = 0
	return count
}

const (
	headerKey    = "X-Pad"
	lineOverhead = len(": \r\n")
	// make sure there is enough space for at least two header fields.
	minimumOverhead = 2 * (len(headerKey) + lineOverhead + 1)
)

func padRequest(req *http.Request, maxLine, maxSize int) *http.Request {
	r := req.Clone(context.TODO())
	cw := &CountingWriter{}
	r.Write(cw)

	n := maxSize - cw.Count()
	key := headerKey
	for i := 0; n > 0; i++ {
		nextkey := fmt.Sprintf("%s%d", headerKey, i+1)
		headerOverhead := len(key) + lineOverhead
		n -= headerOverhead
		if n < 0 {
			log.Printf("Warning: maximum length not reached, cannot fit \"%s\", missing %d bytes", key, -n)
			break
		}

		sz := n
		if sz > maxLine-headerOverhead {
			// Split over two headers. Make sure the next header has
			// enough space available to fit the key and overhead.
			nextHeaderOverhead := len(nextkey) + lineOverhead
			sz = min(maxLine-headerOverhead, n-nextHeaderOverhead)
		}
		if sz <= 0 {
			r.Header.Set(key, "")
		} else {
			value := strings.Repeat("x", sz)
			r.Header.Set(key, value)
			n -= sz
		}
		key = nextkey
	}
	return r
}

func main() {
	var minSize, maxSize, maxLine int
	var method, urlarg, userAgent string
	var detect bool
	var codeOk, codeBad int
	flag.StringVar(&urlarg, "url", "http://localhost/", "URL")
	flag.StringVar(&method, "method", "HEAD", "HTTP method")
	flag.StringVar(&userAgent, "user-agent", "", "User Agent")
	flag.IntVar(&minSize, "min-size", 0, "Minimum header size for auto-detection")
	flag.IntVar(&maxSize, "max-size", 8192*4+127, "Maximum header size")
	flag.IntVar(&maxLine, "max-line", 8192, "Maximum header line size")
	flag.BoolVar(&detect, "detect", false, "Detect maximum header size")
	flag.IntVar(&codeOk, "code-ok", 0, "Additional HTTP status code for success")
	flag.IntVar(&codeBad, "code-bad", 0, "Additional HTTP status code for failure")
	flag.Parse()

	u, err := url.Parse(urlarg)
	if err != nil {
		log.Fatal(err)
	}
	if u.Hostname() == "" {
		log.Fatal("Missing hostname in -url")
	}
	if minMaxLine := minimumOverhead; maxLine < minMaxLine {
		log.Fatalf("-max-line must be at least %d", minMaxLine)
	}
	if detect && minSize > maxSize {
		log.Fatalln("-min-size cannot be larger than -max-size")
	}

	// Disable HTTP/2 by not setting ALPN.
	tlsConfig := &tls.Config{}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// Disable redirects.
			return http.ErrUseLastResponse
		},
	}
	if keylogfile := os.Getenv("SSLKEYLOGFILE"); keylogfile != "" {
		tlsConfig.KeyLogWriter, _ = os.OpenFile(keylogfile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	}

	req, err := http.NewRequest(method, urlarg, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("user-agent", userAgent)
	log.Println("Initial request:")
	req.Write(os.Stdout)
	// For nginx, no large buffer is allocated for the first full headers
	// that fit within "client_header_buffer_size".
	// TODO if headers are shuffled by the proxy, the initial headers will
	// be unknown.
	/*
		var b strings.Builder
		req.Write(b)
	*/

	cw := &CountingWriter{}
	req.Write(cw)
	minimumRequestSize := cw.Count()
	if minimumRequestSize == maxSize {
		log.Println("No need for further padding")
	} else if maxSize-minimumRequestSize < minimumOverhead {
		log.Println("Unable to add additional padding!")
		maxSize = minimumRequestSize
	}

	if detect {
		if minimumRequestSize > minSize {
			minSize = minimumRequestSize
		}
		r := padRequest(req, maxLine, minSize)
		refResp, err := httpClient.Do(r)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Initial response status code: %d\n", refResp.StatusCode)
		switch refResp.StatusCode {
		case http.StatusBadRequest,
			http.StatusRequestHeaderFieldsTooLarge,
			http.StatusRequestURITooLong:
			log.Fatal("Initial request is already rejected, cannot detect maximum size")
		}

		errorCount := 0
		detectedSize := minSize
		successResponse := refResp
		var badResponse *http.Response
		minSize += 1
		for minSize <= maxSize {
			midSize := (minSize + maxSize) / 2
			r := padRequest(req, maxLine, midSize)
			trialResp, err := httpClient.Do(r)
			if err != nil {
				log.Fatal(err)
			}
			probesRemaining := int(math.Log2(float64(maxSize - minSize + 1)))
			log.Printf("Tried %d - status %d (about %d probes remaining)\n",
				midSize, trialResp.StatusCode, probesRemaining)
			switch {
			case trialResp.StatusCode == refResp.StatusCode,
				(codeOk != 0 && trialResp.StatusCode == codeOk):
				// Length was ok.
				minSize = midSize + 1
				detectedSize = midSize
				successResponse = trialResp
			case trialResp.StatusCode == http.StatusBadRequest,
				trialResp.StatusCode == http.StatusRequestHeaderFieldsTooLarge,
				(codeBad != 0 && trialResp.StatusCode == codeBad):
				// Length was bad
				maxSize = midSize - 1
				badResponse = trialResp
			case trialResp.StatusCode/100 == 5:
				// Server error, retry
				errorCount += 1
				if errorCount > 5 {
					log.Fatalf("Too many failures, tried %d in [%d,%d]", midSize, minSize, maxSize)
				}
			default:
				log.Fatalf("Unexpected response code: %d", trialResp.StatusCode)
			}
		}
		if badResponse == nil {
			log.Println("Warning: maximum header size was not exceeded and could be larger")
		}
		log.Printf("Detected maximum header size: %d", detectedSize)
		successResponse.Write(os.Stdout)
	} else {
		r := padRequest(req, maxLine, maxSize)
		resp, err := httpClient.Do(r)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Request size: %d", maxSize)
		log.Printf("response: %d\n", resp.StatusCode)
		resp.Write(os.Stdout)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	} else {
		return b
	}
}
