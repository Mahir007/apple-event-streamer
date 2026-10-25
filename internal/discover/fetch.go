package discover

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// MaxBody is the most Fetch and ReadLimited accept. Apple's pages and
// playlists are well under 1 MiB.
const MaxBody = 16 << 20

// Fetcher downloads Apple's pages and playlists. A failure that is likely
// to be brief (a dropped or refused connection, a 5xx answer) is retried
// once; timeouts are not, since the user has already waited for them.
type Fetcher struct {
	Client *http.Client
	// Logf, if set, is told about retries.
	Logf func(format string, a ...any)

	retryDelay time.Duration // tests shorten it; zero means one second
}

// Error is a failed download, worded for the user. Err is the cause.
type Error struct {
	// What names the document, e.g. "the Apple Events page".
	What string
	URL  string
	Err  error
	// Timeout is the client timeout, for wording timeouts.
	Timeout time.Duration
}

func (e *Error) Error() string {
	host := e.URL
	if u, err := url.Parse(e.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	return fmt.Sprintf("couldn't load %s from %s: %s", e.What, host, e.reason())
}

func (e *Error) Unwrap() error { return e.Err }

var errTooLarge = fmt.Errorf("larger than %d MiB", MaxBody>>20)

// statusError is a non-2xx answer.
type statusError struct {
	code   int
	status string
}

func (s *statusError) Error() string { return s.status }

// cutOffError is a body that stopped arriving before it was complete.
type cutOffError struct{ err error }

func (c *cutOffError) Error() string { return "reading body: " + c.err.Error() }
func (c *cutOffError) Unwrap() error { return c.err }

func (e *Error) reason() string {
	var (
		status   *statusError
		netErr   net.Error
		dnsErr   *net.DNSError
		opErr    *net.OpError
		verify   *tls.CertificateVerificationError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		recordHd tls.RecordHeaderError
		cutOff   *cutOffError
	)
	switch err := e.Err; {
	case errors.As(err, &status):
		return "the server answered " + status.status
	case errors.Is(err, errTooLarge):
		return "the response is " + errTooLarge.Error()
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("couldn't look up %s (check your internet connection)", dnsErr.Name)
	case errors.As(err, &netErr) && netErr.Timeout():
		if e.Timeout > 0 {
			return fmt.Sprintf("no complete answer within %v (raise it with --timeout)", e.Timeout)
		}
		return "the connection timed out"
	case errors.As(err, &hostname):
		return "its security certificate is for a different site (is something intercepting HTTPS?)"
	case errors.As(err, &unknown):
		return "its security certificate isn't from a trusted authority (is something intercepting HTTPS?)"
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "its security certificate has expired or isn't valid yet (check your clock)"
		}
		return "its security certificate isn't valid"
	case errors.As(err, &verify), errors.As(err, &recordHd):
		return "the secure connection failed"
	case errors.As(err, &opErr) && opErr.Op == "proxyconnect":
		return "couldn't connect through the proxy set in HTTPS_PROXY/HTTP_PROXY"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "couldn't connect to the server (check your internet connection)"
	case errors.As(err, &cutOff), errors.As(err, &opErr),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "the connection was cut off"
	}
	return e.Err.Error()
}

// retryable reports whether a second attempt might succeed.
func retryable(err error) bool {
	var (
		status *statusError
		netErr net.Error
		dnsErr *net.DNSError
		opErr  *net.OpError
		cutOff *cutOffError
	)
	switch {
	case errors.As(err, &status):
		return status.code >= 500 || status.code == http.StatusRequestTimeout || status.code == http.StatusTooManyRequests
	case errors.As(err, &netErr) && netErr.Timeout():
		return false
	case errors.As(err, &dnsErr):
		return dnsErr.IsTemporary
	case errors.As(err, &cutOff), errors.As(err, &opErr),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return true
	}
	return false
}

// Fetch GETs rawURL and returns the body. what names the document in errors
// and log lines, e.g. "the Apple Events page". If ctx is cancelled the
// context's error is returned as is.
func (f *Fetcher) Fetch(ctx context.Context, what, rawURL string) ([]byte, error) {
	body, err := f.get(ctx, rawURL)
	if err != nil && ctx.Err() == nil && retryable(err) {
		delay := f.retryDelay
		if delay == 0 {
			delay = time.Second
		}
		if f.Logf != nil {
			f.Logf("retrying %s in %v: %v", what, delay, err)
		}
		t := time.NewTimer(delay)
		select {
		case <-t.C:
			body, err = f.get(ctx, rawURL)
		case <-ctx.Done():
			t.Stop()
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, &Error{What: what, URL: rawURL, Err: err, Timeout: f.Client.Timeout}
	}
	return body, nil
}

func (f *Fetcher) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &statusError{code: resp.StatusCode, status: resp.Status}
	}
	body, err := ReadLimited(resp.Body)
	var netErr net.Error
	if err != nil && !errors.Is(err, errTooLarge) && !(errors.As(err, &netErr) && netErr.Timeout()) {
		err = &cutOffError{err}
	}
	return body, err
}

// ReadLimited reads r to the end, failing if it holds more than MaxBody
// bytes rather than silently using a truncated prefix.
func ReadLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if err == nil && len(b) > MaxBody {
		return nil, errTooLarge
	}
	return b, err
}
