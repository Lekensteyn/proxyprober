package main

import (
	"net/http"
	"testing"
)

func checkPadRequest(t *testing.T, req *http.Request, maxLine, maxSize int) {
	r := padRequest(req, maxLine, maxSize)
	cw := &CountingWriter{}
	r.Write(cw)
	n := cw.Count()
	if n != maxSize {
		t.Errorf("padRequest (maxLine=%d) = %d; want %d", maxLine, n, maxSize)
	}
}

func prepareRequest(t *testing.T) (*http.Request, int) {
	req, err := http.NewRequest("GET", "https://localhost/", nil)
	if err != nil {
		t.Errorf("NewRequest failed: %s", err)
	}
	cw := &CountingWriter{}
	req.Write(cw)
	minSize := cw.Count() + len("X-Pad: \r\n")
	return req, minSize
}

func TestPadRequest(t *testing.T) {
	req, minSize := prepareRequest(t)
	lineLength := 4096
	maxMaxSize := 3 * lineLength

	for maxSize := minSize; maxSize < maxMaxSize; maxSize++ {
		checkPadRequest(t, req, lineLength, maxSize)
	}
}

func TestPadRequest_MinLine(t *testing.T) {
	req, minSize := prepareRequest(t)
	for i := -minimumOverhead; i <= minimumOverhead*3; i++ {
		checkPadRequest(t, req, minimumOverhead, minSize+minimumOverhead+i)
	}
}
