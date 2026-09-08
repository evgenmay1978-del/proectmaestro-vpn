package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

const maxLegacyLinksBytes = ((1 << 20) + 2) / 3 * 4

type legacySubscriptionXrayJSONKey struct{}

// WrapLegacySubscriptions keeps the live legacy server authoritative for token,
// expiry, device admission and all ordinary protocol settings. Verified endpoint
// labels may add country names. Only an already accepted base64 share-link
// response can receive paid native CDN publication.
// JSON, helper/info responses and all legacy rejection responses pass unchanged.
func WrapLegacySubscriptions(
	next http.Handler,
	upstream string,
	publication WhiteListPublicationSource,
	publicationTimeout time.Duration,
	topologies ...subgen.Customer,
) (http.Handler, error) {
	if next == nil || len(topologies) > 1 {
		return nil, errors.New("legacy subscription upstream unavailable")
	}
	if upstream == "" {
		return next, nil
	}
	// This migration seam can only reach the existing loopback legacy panel.
	// It must never forward private subscription paths through a remote origin,
	// a configurable proxy, redirects, or an operator-supplied URL credential.
	if upstream != "http://127.0.0.1:8910" {
		return nil, errors.New("invalid legacy subscription upstream")
	}
	origin, err := url.Parse(upstream)
	if err != nil {
		return nil, errors.New("invalid legacy subscription upstream")
	}
	if publicationTimeout <= 0 {
		publicationTimeout = time.Second
	}
	var labeler legacyShareLabeler
	if len(topologies) == 1 {
		labeler = newLegacyShareLabeler(topologies[0])
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 20 * time.Second
	proxy := httputil.NewSingleHostReverseProxy(origin)
	proxy.Transport = transport
	// The default reverse-proxy logger may include the private request URL.
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "subscription unavailable", http.StatusBadGateway)
	}
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		if legacySubscriptionToken(request) != "" {
			request.Header.Set("Accept-Encoding", "identity")
			query := request.URL.Query()
			_, formatSelected := query["format"]
			_, appSelected := query["app"]
			client := strings.TrimSpace(request.Header.Get("X-Client"))
			userAgent := strings.ToUpper(strings.TrimSpace(request.UserAgent()))
			incy := strings.EqualFold(client, "INCY") ||
				userAgent == "INCY" || strings.HasPrefix(userAgent, "INCY/")
			karing := strings.EqualFold(client, "KARING") || userAgent == "KARING" || strings.HasPrefix(userAgent, "KARING/")
			xrayJSON := query.Get("format") == "xray" || incy && !formatSelected && !appSelected
			if !formatSelected && (strings.EqualFold(strings.TrimSpace(query.Get("app")), "karing") || karing && !appSelected) {
				query.Set("format", "links")
				request.URL.RawQuery = query.Encode()
			}
			if xrayJSON {
				// The loopback legacy panel remains authoritative for token, expiry
				// and device admission. Ask it for its validated link form, then
				// replace that accepted response with the paid full-Xray CDN form.
				*request = *request.WithContext(context.WithValue(request.Context(), legacySubscriptionXrayJSONKey{}, true))
				query.Set("format", "links")
				request.URL.RawQuery = query.Encode()
			}
			if xrayJSON || query.Get("app") == "karing" || query.Get("format") == "links" {
				// A legacy validator covers ordinary nodes only. CDN access or
				// balance may have changed while those nodes remained identical.
				for _, header := range []string{"If-None-Match", "If-Modified-Since", "If-Range", "Range"} {
					request.Header.Del(header)
				}
			}
		}
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		return appendLegacyPaidWhiteList(response, publication, publicationTimeout, labeler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/sub/") {
			proxy.ServeHTTP(w, request)
			return
		}
		next.ServeHTTP(w, request)
	}), nil
}

func legacySubscriptionToken(request *http.Request) string {
	if request == nil || request.URL == nil || request.Method != http.MethodGet ||
		!strings.HasPrefix(request.URL.Path, "/sub/") {
		return ""
	}
	token := strings.TrimPrefix(request.URL.Path, "/sub/")
	if token == "" || len(token) > 1024 || strings.Contains(token, "/") {
		return ""
	}
	return token
}

func appendLegacyPaidWhiteList(
	response *http.Response,
	publication WhiteListPublicationSource,
	timeout time.Duration,
	labelers ...legacyShareLabeler,
) error {
	token := legacySubscriptionToken(response.Request)
	if token == "" || response.StatusCode != http.StatusOK || response.Body == nil {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/plain" || response.Header.Get("Content-Encoding") != "" {
		return nil
	}
	var labeler legacyShareLabeler
	if len(labelers) == 1 {
		labeler = labelers[0]
	}
	xrayJSON, _ := response.Request.Context().Value(legacySubscriptionXrayJSONKey{}).(bool)
	if publication == nil && labeler == nil && !xrayJSON {
		response.Header.Set("X-Maestro-CDN", "disabled")
		return nil
	}
	body := response.Body
	ordinary, err := io.ReadAll(io.LimitReader(body, maxLegacyLinksBytes+1))
	if err != nil {
		_ = body.Close()
		return errors.New("legacy subscription body unavailable")
	}
	if len(ordinary) > maxLegacyLinksBytes {
		response.Body = &legacySubscriptionReadCloser{Reader: io.MultiReader(bytes.NewReader(ordinary), body), Closer: body}
		return nil
	}
	_ = body.Close()
	response.Body = io.NopCloser(bytes.NewReader(ordinary))
	// Validate the actual representation, independent of client query spelling.
	// No JSON-to-links conversion can discard or alter legacy protocol settings.
	if _, err := subgen.AppendWhiteListShareLinks(string(ordinary), nil); err != nil {
		return nil
	}
	response.Header.Set("Profile-Title", "base64:TWFlc3Ryb1ZQTg==")
	response.Header.Set("Content-Disposition", `attachment; filename="MaestroVPN"`)
	if labeler != nil {
		if renamed, err := labeler(string(ordinary)); err == nil && renamed != string(ordinary) {
			ordinary = []byte(renamed)
			setLegacySubscriptionBody(response, renamed)
		}
	}
	var ordinaryXray []byte
	if xrayJSON {
		ordinaryXray, err = subgen.WhiteListCombinedXrayJSONSubscription(string(ordinary), nil)
		if err != nil {
			return errors.New("legacy Xray subscription unavailable")
		}
		setLegacySubscriptionBody(response, string(ordinaryXray))
		response.Header.Set("Content-Type", "application/json")
	}
	if publication == nil {
		response.Header.Set("X-Maestro-CDN", "disabled")
		return nil
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(response.Request.Context(), timeout)
	defer cancel()
	snapshot, err := publication.WhiteListPublication(ctx, token, now)
	if err != nil {
		response.Header.Set("X-Maestro-CDN", "unavailable")
		return nil
	}
	if snapshot.Verdict != WhiteListPublishable {
		response.Header.Set("X-Maestro-CDN", "omitted")
		return nil
	}
	if snapshot.ProjectionVersion <= 0 || snapshot.DesiredGeneration <= 0 ||
		!snapshot.FreshThrough.After(time.Now()) || len(snapshot.Nodes) == 0 || len(snapshot.Nodes) > 16 {
		response.Header.Set("X-Maestro-CDN", "unavailable")
		return nil
	}
	if xrayJSON {
		document, err := subgen.WhiteListCombinedXrayJSONSubscription(string(ordinary), snapshot.Nodes)
		if err != nil {
			response.Header.Set("X-Maestro-CDN", "unavailable")
			setLegacySubscriptionBody(response, string(ordinaryXray))
			response.Header.Set("Content-Type", "application/json")
			return nil
		}
		setLegacySubscriptionBody(response, string(document))
		response.Header.Set("Content-Type", "application/json")
		response.Header.Set("X-Maestro-CDN", "included")
		return nil
	}
	augmented, err := subgen.AppendWhiteListShareLinks(string(ordinary), snapshot.Nodes)
	if err != nil {
		response.Header.Set("X-Maestro-CDN", "unavailable")
		return nil
	}
	setLegacySubscriptionBody(response, augmented)
	response.Header.Set("X-Maestro-CDN", "included")
	return nil
}

func setLegacySubscriptionBody(response *http.Response, document string) {
	response.Body = io.NopCloser(strings.NewReader(document))
	response.ContentLength = int64(len(document))
	response.Header.Set("Content-Length", fmt.Sprint(len(document)))
	response.Header.Set("Cache-Control", "no-store")
	response.Header.Del("Last-Modified")
	response.Header.Del("Content-MD5")
	response.Header.Del("Digest")
	response.Header.Del("Content-Range")
	response.Header.Del("Accept-Ranges")
	sum := sha256.Sum256([]byte(document))
	response.Header.Set("ETag", fmt.Sprintf("\"%x\"", sum))
}

type legacySubscriptionReadCloser struct {
	io.Reader
	io.Closer
}
