package subgen

import (
	"errors"
	"net/url"
	"strings"
)

const (
	IncyDeliveryClient = "INCY"
	HappDeliveryClient = "HAPP"
	KaringDeliveryClient = "KARING"

	IncyOneTapFormat        = "INCY_ONE_TAP"
	CopyHTTPSURLAndQRFormat = "COPY_HTTPS_URL_AND_QR"
	KaringInstallConfigFormat = "KARING_INSTALL_CONFIG"
)

var (
	ErrInvalidSubscriptionURL    = errors.New("invalid subscription url")
	ErrUnsupportedDeliveryClient = errors.New("unsupported delivery client")
	ErrDeliveryEncoding          = errors.New("delivery encoding failed")
)

// Delivery is the client-facing subscription descriptor for a supported client.
type Delivery struct {
	Client string
	Format string
	URL    string
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
		oneTapURL, err := encodeIncyOneTap(linksURL)
		if err != nil {
			return Delivery{}, ErrDeliveryEncoding
		}
		return Delivery{Client: client, Format: IncyOneTapFormat, URL: oneTapURL}, nil
	case HappDeliveryClient:
		return Delivery{Client: client, Format: CopyHTTPSURLAndQRFormat, URL: linksURL}, nil
	case KaringDeliveryClient:
		return Delivery{
			Client: client,
			Format: KaringInstallConfigFormat,
			URL: "karing://install-config?url=" + url.QueryEscape(linksURL) +
				"&name=" + url.QueryEscape("MaestroVPN"),
		}, nil
	default:
		return Delivery{}, ErrUnsupportedDeliveryClient
	}
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
	if parsed.RawQuery != "" && parsed.RawQuery != "format=links" {
		return false
	}

	parts := strings.Split(parsed.Path, "/")
	return len(parts) == 3 && parts[0] == "" && parts[1] == "sub" && parts[2] != ""
}
