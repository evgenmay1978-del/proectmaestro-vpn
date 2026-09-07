package subgen

import (
	"errors"
	"net/url"
	"strings"
)

const (
	IncyDeliveryClient   = "INCY"
	HappDeliveryClient   = "HAPP"
	KaringDeliveryClient = "KARING"

	IncyOneTapFormat          = "INCY_ONE_TAP"
	CopyHTTPSURLAndQRFormat   = "COPY_HTTPS_URL_AND_QR"
	KaringInstallConfigFormat = "KARING_INSTALL_CONFIG"
)

var (
	ErrInvalidSubscriptionURL    = errors.New("invalid subscription url")
	ErrUnsupportedDeliveryClient = errors.New("unsupported delivery client")
	ErrDeliveryEncoding          = errors.New("delivery encoding failed")
)

// Delivery is the client-facing subscription descriptor for a supported client.
type Delivery struct {
	Client  string
	Format  string
	URL     string
	CopyURL string
}

// BuildDelivery validates a private subscription URL and returns the client-safe
// delivery form. Happ keeps the HTTPS URL for copy/QR until device proof exists.
func BuildDelivery(client, subscriptionURL string) (Delivery, error) {
	linksURL, err := BuildLinksSubscriptionURL(subscriptionURL)
	if err != nil {
		return Delivery{}, err
	}

	switch client {
	case IncyDeliveryClient:
		xrayURL, err := BuildXraySubscriptionURL(subscriptionURL)
		if err != nil {
			return Delivery{}, err
		}
		oneTapURL, err := encodeIncyOneTap(xrayURL)
		if err != nil {
			return Delivery{}, ErrDeliveryEncoding
		}
		return Delivery{Client: client, Format: IncyOneTapFormat, URL: oneTapURL, CopyURL: xrayURL}, nil
	case HappDeliveryClient:
		xrayURL, err := BuildXraySubscriptionURL(subscriptionURL)
		if err != nil {
			return Delivery{}, err
		}
		return Delivery{Client: client, Format: CopyHTTPSURLAndQRFormat, URL: xrayURL, CopyURL: xrayURL}, nil
	case KaringDeliveryClient:
		return Delivery{
			Client:  client,
			Format:  KaringInstallConfigFormat,
			CopyURL: linksURL,
			URL: "karing://install-config?url=" + url.QueryEscape(linksURL) +
				"&name=" + url.QueryEscape("MaestroVPN"),
		}, nil
	default:
		return Delivery{}, ErrUnsupportedDeliveryClient
	}
}

// BuildXraySubscriptionURL preserves the private token while selecting the
// full-Xray CDN representation proven by the existing Incy subscription.
func BuildXraySubscriptionURL(rawURL string) (string, error) {
	if !isValidSubscriptionURL(rawURL) {
		return "", ErrInvalidSubscriptionURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", ErrInvalidSubscriptionURL
	}
	query := parsed.Query()
	query.Set("format", "xray")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// BuildLinksSubscriptionURL preserves the existing subscription token while
// selecting the share-link representation used by third-party clients.
func BuildLinksSubscriptionURL(rawURL string) (string, error) {
	if !isValidSubscriptionURL(rawURL) {
		return "", ErrInvalidSubscriptionURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", ErrInvalidSubscriptionURL
	}
	query := parsed.Query()
	query.Set("format", "links")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func isValidSubscriptionURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.ForceQuery || strings.Contains(rawURL, "#") {
		return false
	}
	if parsed.RawQuery != "" && parsed.RawQuery != "format=links" && parsed.RawQuery != "format=xray" {
		return false
	}

	parts := strings.Split(parsed.Path, "/")
	return len(parts) == 3 && parts[0] == "" && parts[1] == "sub" && parts[2] != ""
}
