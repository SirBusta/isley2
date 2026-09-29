package handlers

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIsPublicIP(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"8.8.8.8":       true,
		"104.21.0.1":    true,
		"127.0.0.1":     false,
		"10.0.0.5":      false,
		"192.168.1.10":  false,
		"172.16.0.1":    false,
		"169.254.1.1":   false,
		"100.64.0.1":    false,
		"0.0.0.0":       false,
		"::1":           false,
		"fe80::1":       false,
		"fd00::1":       false,
		"2606:4700::1":  true,
	}
	for ip, want := range cases {
		if got := isPublicIP(net.ParseIP(ip)); got != want {
			t.Errorf("isPublicIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestDIDFromATURI(t *testing.T) {
	t.Parallel()
	if got := didFromATURI("at://did:plc:abc/org.cannadb.strain/xyz"); got != "did:plc:abc" {
		t.Errorf("got %q", got)
	}
	if got := didFromATURI("https://example.com"); got != "" {
		t.Errorf("got %q", got)
	}
}

// fakeAtproto serves a PLC directory and a PDS from one test server.
func fakeAtproto(t *testing.T, blob []byte) (*httptest.Server, atprotoBlobFetcher) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/plc/did:plc:"):
			_, _ = w.Write([]byte(`{"service":[{"id":"#atproto_pds","type":"AtprotoPersonalDataServer","serviceEndpoint":"` + srv.URL + `/pds"}]}`))
		case r.URL.Path == "/pds/xrpc/com.atproto.sync.getBlob":
			if r.URL.Query().Get("cid") != "bafyblob" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(blob)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, atprotoBlobFetcher{client: srv.Client(), plcBase: srv.URL + "/plc/"}
}

func TestFetchBlob_ReturnsValidatedImage(t *testing.T) {
	t.Parallel()
	img := tinyPNG(t)
	_, f := fakeAtproto(t, img)

	body, ext, err := f.fetchBlob(context.Background(), "did:plc:test", "bafyblob")
	if err != nil {
		t.Fatalf("fetchBlob: %v", err)
	}
	if ext != ".png" || !bytes.Equal(body, img) {
		t.Fatalf("got ext %q, %d bytes", ext, len(body))
	}
}

func TestFetchBlob_RejectsNonImage(t *testing.T) {
	t.Parallel()
	_, f := fakeAtproto(t, []byte("<html>not an image</html>"))
	if _, _, err := f.fetchBlob(context.Background(), "did:plc:test", "bafyblob"); err == nil {
		t.Fatal("expected an error for non-image content")
	}
}

func TestFetchBlob_RejectsOversizedBlob(t *testing.T) {
	t.Parallel()
	big := append(tinyPNG(t), make([]byte, maxPackagingImageSize)...)
	_, f := fakeAtproto(t, big)
	if _, _, err := f.fetchBlob(context.Background(), "did:plc:test", "bafyblob"); err == nil {
		t.Fatal("expected an error for an oversized blob")
	}
}

func TestFetchBlob_RejectsUnsupportedDID(t *testing.T) {
	t.Parallel()
	_, f := fakeAtproto(t, tinyPNG(t))
	for _, did := range []string{"did:web:evil.example", "did:plc:x/../y", ""} {
		if _, _, err := f.fetchBlob(context.Background(), did, "bafyblob"); err == nil {
			t.Errorf("expected an error for DID %q", did)
		}
	}
}

func TestPublicOnlyClient_RefusesLoopback(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should not be reached"))
	}))
	defer srv.Close()

	resp, err := publicOnlyClient.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("publicOnlyClient connected to a loopback address")
	}
}
