package monitors

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The web app's cURL import leaves reserved headers out before the form is saved (web/src/curl.ts). Its list must be the server's, or a pasted
// command would fill in a header the server then refuses, or drop one it would have accepted.
func TestReservedHeadersMatchTheWebApp(t *testing.T) {
	src, err := os.ReadFile("../../web/src/reservedHeaders.ts")
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`(?s)RESERVED_HEADERS[^=]*=\s*\[(.*?)\]`).FindSubmatch(src)
	if list == nil {
		t.Fatal("RESERVED_HEADERS not found in web/src/reservedHeaders.ts")
	}
	var web []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(list[1], -1) {
		web = append(web, strings.ToLower(string(m[1])))
	}
	var server []string
	for h := range reservedHeaders {
		if h != "Authorization" { // the web app moves it into credentials instead of leaving it out
			server = append(server, strings.ToLower(h))
		}
	}
	slices.Sort(web)
	slices.Sort(server)
	if !slices.Equal(web, server) {
		t.Errorf("web/src/reservedHeaders.ts lists %v, the server reserves %v (Authorization aside)", web, server)
	}
}
