package app_test

// Pages must never load anything from other sites: Bootstrap, the icons,
// charts, the video player and exif-js are bundled under web/static/vendor/
// so the UI works without internet access and nothing phones home.

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

var outsideLoad = []*regexp.Regexp{
	// <script src>, <link href>, <img src>, ... pointing at another host.
	regexp.MustCompile(`(?i)<(script|link|img|iframe|source|video|audio|embed)\b[^>]*\b(src|href)\s*=\s*["']?(https?:)?//`),
	regexp.MustCompile(`(?i)url\(\s*["']?(https?:)?//`),
	regexp.MustCompile(`(?i)@import\s+(url\()?["']?(https?:)?//`),
	regexp.MustCompile("(?i)fetch\\(\\s*[\"'`](https?:)?//"),
}

func TestPagesLoadNothingFromOtherSites(t *testing.T) {
	t.Parallel()
	repo := testutil.RepoFS(t)

	checked := 0
	for _, root := range []string{"web/templates", "web/static"} {
		require.NoError(t, fs.WalkDir(repo, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			vendored := strings.HasPrefix(path, "web/static/vendor/")
			ext := path[strings.LastIndex(path, ".")+1:]
			// Bundled libraries are checked only for stylesheets that pull
			// fonts/images from elsewhere; their minified JS is left alone.
			if vendored && ext != "css" {
				return nil
			}
			if ext != "html" && ext != "js" && ext != "css" {
				return nil
			}
			body, err := fs.ReadFile(repo, path)
			require.NoError(t, err)
			checked++
			for _, re := range outsideLoad {
				if m := re.Find(body); m != nil {
					t.Errorf("%s loads from another site: %s", path, m)
				}
			}
			return nil
		}))
	}
	assert.Greater(t, checked, 50, "walked the templates and static files")
}

func TestSecurityPolicyAllowsOnlyThisServer(t *testing.T) {
	t.Parallel()
	server := testutil.NewTestServer(t, testutil.NewTestDB(t))
	resp, err := http.Get(server.URL + "/login")
	require.NoError(t, err)
	defer testutil.DrainAndClose(resp)

	csp := resp.Header.Get("Content-Security-Policy")
	require.NotEmpty(t, csp)
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		name := strings.SplitN(directive, " ", 2)[0]
		switch name {
		case "script-src", "style-src", "font-src", "img-src", "connect-src", "default-src":
			assert.NotContains(t, directive, "http", "%s must not allow other sites", name)
		}
	}
}
