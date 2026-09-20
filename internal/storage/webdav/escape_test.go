package webdav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Keys that exercise the characters which break raw URL concatenation:
// "#" starts a fragment, "?" starts a query, "%" starts an escape sequence,
// and a space is not legal in a request target at all.
var trickyKeys = []string{
	"attachments/a #1?.txt.age",
	"dir/100%/file.age",
	"sessions/rollout 2026.jsonl.age",
	"sessions/a+b&c=d.age",
}

func TestEscapeKeyPreservesSeparators(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"sessions/abc.age", "sessions/abc.age"},
		{"attachments/a #1?.txt.age", "attachments/a%20%231%3F.txt.age"},
		{"dir/100%/file.age", "dir/100%25/file.age"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := escapeKey(tt.key); got != tt.want {
			t.Errorf("escapeKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
		if got := unescapeKey(escapeKey(tt.key)); got != tt.key {
			t.Errorf("unescapeKey(escapeKey(%q)) = %q, want round trip", tt.key, got)
		}
	}
}

func TestFullURLEscapesKeyAndPrefix(t *testing.T) {
	c := &Client{baseURL: "https://cloud.example.com/dav", pathPrefix: "codex sync"}

	if got, want := c.collectionURL(), "https://cloud.example.com/dav/codex%20sync/"; got != want {
		t.Errorf("collectionURL() = %q, want %q", got, want)
	}

	for _, key := range trickyKeys {
		got := c.fullURL(key)
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("fullURL(%q) = %q is not parseable: %v", key, got, err)
		}
		if u.RawQuery != "" || u.Fragment != "" {
			t.Errorf("fullURL(%q) = %q leaked a query (%q) or fragment (%q)", key, got, u.RawQuery, u.Fragment)
		}
		if want := "/dav/codex sync/" + key; u.Path != want {
			t.Errorf("fullURL(%q) decoded path = %q, want %q", key, u.Path, want)
		}
	}
}

func TestHrefToKeyRoundTripsEscapedPaths(t *testing.T) {
	clients := map[string]*Client{
		"with prefix":    {baseURL: "https://cloud.example.com/dav", pathPrefix: "codex-sync"},
		"spaced prefix":  {baseURL: "https://cloud.example.com/dav", pathPrefix: "codex sync"},
		"without prefix": {baseURL: "https://cloud.example.com/dav", pathPrefix: ""},
	}

	for name, c := range clients {
		t.Run(name, func(t *testing.T) {
			for _, key := range trickyKeys {
				u, err := url.Parse(c.fullURL(key))
				if err != nil {
					t.Fatalf("fullURL(%q) not parseable: %v", key, err)
				}
				// Servers echo back the escaped path, either absolute or relative.
				if got := c.hrefToKey(u.EscapedPath()); got != key {
					t.Errorf("hrefToKey(%q) = %q, want %q", u.EscapedPath(), got, key)
				}
				if got := c.hrefToKey(u.String()); got != key {
					t.Errorf("hrefToKey(%q) = %q, want %q", u.String(), got, key)
				}
			}
		})
	}
}

func TestUploadDownloadDeleteEscapeKeys(t *testing.T) {
	for _, key := range trickyKeys {
		t.Run(key, func(t *testing.T) {
			var seen []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RawQuery != "" {
					t.Errorf("%s request leaked a query string: %q", r.Method, r.URL.RawQuery)
				}
				switch r.Method {
				case "MKCOL":
					w.WriteHeader(http.StatusCreated)
				case "PUT":
					seen = append(seen, r.URL.Path)
					w.WriteHeader(http.StatusCreated)
				case "GET":
					seen = append(seen, r.URL.Path)
					_, _ = w.Write([]byte("payload"))
				case "DELETE":
					seen = append(seen, r.URL.Path)
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer server.Close()

			client := &Client{
				baseURL:    server.URL,
				pathPrefix: "backup dir",
				httpClient: server.Client(),
			}

			ctx := context.Background()
			if err := client.Upload(ctx, key, []byte("payload")); err != nil {
				t.Fatalf("Upload() error = %v", err)
			}
			if _, err := client.Download(ctx, key); err != nil {
				t.Fatalf("Download() error = %v", err)
			}
			if err := client.Delete(ctx, key); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			want := "/backup dir/" + key
			if len(seen) != 3 {
				t.Fatalf("expected 3 requests to reach the server, got %d (%v)", len(seen), seen)
			}
			for _, got := range seen {
				if got != want {
					t.Errorf("server saw path %q, want %q", got, want)
				}
			}
		})
	}
}

func TestListDecodesEscapedHrefs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body := `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`
		body += `<d:response><d:href>/backup%20dir/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
		for _, key := range trickyKeys {
			// "&" is legal in a path segment, so escapeKey leaves it alone;
			// the XML document still has to escape it.
			href := strings.ReplaceAll(escapeKey(key), "&", "&amp;")
			body += fmt.Sprintf(
				`<d:response><d:href>/backup%%20dir/%s</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>7</d:getcontentlength><d:getetag>"e-%s"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
				href, href)
		}
		body += `</d:multistatus>`
		w.WriteHeader(207)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := &Client{
		baseURL:    server.URL,
		pathPrefix: "backup dir",
		httpClient: server.Client(),
	}

	objects, err := client.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(objects) != len(trickyKeys) {
		t.Fatalf("expected %d objects, got %d (%+v)", len(trickyKeys), len(objects), objects)
	}
	for i, key := range trickyKeys {
		if objects[i].Key != key {
			t.Errorf("object %d key = %q, want %q", i, objects[i].Key, key)
		}
	}
}
