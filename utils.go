package wtf

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/btwiuse/version"
	"github.com/webteleport/webteleport"
)

// listen with a timeout
func listenWithTimeout(addr *url.URL, timeout time.Duration) (net.Listener, error) {
	if os.Getenv("WTF_SPINNER") != "" {
		s := spinner.New(Turbofish, 50*time.Millisecond)
		s.Prefix = "🐱"
		s.Suffix = fmt.Sprintf("🌐 %s", addr.Hostname())
		s.Color("yellow")
		s.Start()
		defer s.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return webteleport.Listen(ctx, addr.String())
}

// createURLWithQueryParams creates a URL with query parameters
func createURLWithQueryParams(stationURL string) (*url.URL, error) {
	// parse the station URL
	u, err := url.Parse(stationURL)
	if err != nil {
		return nil, err
	}

	// attach extra info to the query string
	q := u.Query()
	q.Add("clientlib", "webteleport/wtf")
	if os.Getenv("WTF_DEBUG") != "" {
		for _, arg := range os.Args {
			q.Add("os.Args", arg)
		}
		for _, env := range os.Environ() {
			q.Add("os.Environ", env)
		}
	}
	q.Add("version.Major", version.Info.Major)
	q.Add("version.Minor", version.Info.Minor)
	q.Add("version.GitVersion", version.Info.GitVersion)
	q.Add("version.GitComit", version.Info.GitCommit)
	q.Add("version.GitTreeState", version.Info.GitTreeState)
	q.Add("version.BuildDate", version.Info.BuildDate)
	q.Add("version.GoVersion", version.Info.GoVersion)
	q.Add("version.Compiler", version.Info.Compiler)
	q.Add("version.Platform", version.Info.Platform)
	u.RawQuery = q.Encode()

	return u, nil
}

// logServerStatus logs the status of the server.
func logServerStatus(ln net.Listener, u *url.URL) {
	endpoint := fmt.Sprintf("%s://%s", ln.Addr().Network(), ln.Addr().String())
	slog.Info(fmt.Sprintf("🛸 listening on %s", endpoint))

	if u.Fragment == "" {
		slog.Info("🔓 publicly accessible without a password")
	} else {
		slog.Info("🔒 secured by password authentication")
	}
}

// parseQuietParam parses the 'quiet' query parameter.
func parseQuietParam(query url.Values) (bool, error) {
	q := query.Get("quiet")
	// If no quiet is specified, be loggy
	if q == "" {
		return false, nil
	}
	return strconv.ParseBool(q)
}

// parseTimeoutParam parses the 'timeout' query parameter.
func parseTimeoutParam(query url.Values) (time.Duration, error) {
	t := query.Get("timeout")
	// If no timeout is specified, use the default
	if t == "" {
		return DefaultTimeout, nil
	}
	return time.ParseDuration(t)
}

// parseGcIntervalParam parses the 'gc' query parameter.
func parseGcIntervalParam(query url.Values) (time.Duration, error) {
	t := query.Get("gc")
	// If no gc interval is specified, use the default
	if t == "" {
		return DefaultGcInterval, nil
	}
	return time.ParseDuration(t)
}

// parseGcRetryParam parses the 'retry' query parameter.
func parseGcRetryParam(query url.Values) (int64, error) {
	r := query.Get("retry")
	// If no retry limit is specified, use the default
	if r == "" {
		return DefaultGcRetry, nil
	}
	return strconv.ParseInt(r, 10, 64)
}

// parsePersistParam parses the 'persist' query parameter.
func parsePersistParam(query url.Values) (bool, error) {
	p := query.Get("persist")
	// If no persist is specified, be ephemeral
	if p == "" {
		return false, nil
	}
	return strconv.ParseBool(p)
}

// gc probes the remote endpoint status and closes the listener if it's unresponsive.
func gc(ln net.Listener, interval time.Duration, limit int64) {
	endpoint := fmt.Sprintf("%s://%s", ln.Addr().Network(), ln.Addr().String())
	if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}
	endpoint += ".well-known/health"

	_, err := url.Parse(endpoint)
	if err != nil {
		slog.Info(fmt.Sprintf("🛸 failed to parse endpoint: %v", err))
		return
	}

	client := &http.Client{
		Timeout: interval,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	// trigger lazy certificate
	client.Get(endpoint)

	retry := limit
	for {
		if retry == 0 {
			slog.Info("🛸 max retry reached")
			break
		}
		// Wait for either the task to complete or a timeout to occur
		time.Sleep(interval)

		resp, err := client.Get(endpoint)
		// if request isn't successful, decrease retry
		if err != nil {
			retry -= 1
			werr := fmt.Errorf("🛸 failed to reach healthcheck endpoint (retry = %d): %v", retry, err)
			slog.Info(werr.Error())
			continue
		}
		// if response stats code is not 200, decrease retry
		if resp.StatusCode != 200 {
			retry -= 1
			werr := fmt.Errorf("🛸 healthcheck endpoint returns status %d (retry = %d): %v", resp.StatusCode, retry, err)
			slog.Info(werr.Error())
			continue
		}

		if retry != limit {
			slog.Info("🛸 back online")
		}

		// otherwise reset retry to limit
		retry = limit

		resp.Body.Close()
	}
	slog.Info("🛸 closing the listener")
	ln.Close()
}
