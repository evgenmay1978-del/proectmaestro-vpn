package subgen

import (
	"errors"
	"strings"
	"testing"

	incylink "github.com/INCY-DEV/incy-link-encoder/go"
)

func TestBuildDeliveryCreatesOfficialIncyOneTapLink(t *testing.T) {
	const source = "https://sub.example.com/sub/fixture-token"

	delivery, err := BuildDelivery("INCY", source)
	if err != nil {
		t.Fatal("Incy delivery returned an error")
	}
	if delivery.Client != "INCY" {
		t.Fatal("Incy delivery has the wrong client")
	}
	if delivery.Format != "INCY_ONE_TAP" {
		t.Fatal("Incy delivery has the wrong format")
	}
	decoded, err := incylink.DecryptLink(delivery.URL)
	if err != nil {
		t.Fatal("Incy delivery is not an official crypt1 link")
	}
	if decoded.URL != source {
		t.Fatal("Incy delivery changed the subscription URL")
	}
	if decoded.Name != "MaestroVPN" {
		t.Fatal("Incy delivery has the wrong display name")
	}
}

func TestBuildDeliveryKeepsHappOnTheValidatedHTTPSURL(t *testing.T) {
	const source = "https://sub.example.com/sub/fixture-token?format=links"

	delivery, err := BuildDelivery("HAPP", source)
	if err != nil {
		t.Fatal("Happ delivery returned an error")
	}
	if delivery.Client != "HAPP" {
		t.Fatal("Happ delivery has the wrong client")
	}
	if delivery.Format != "COPY_HTTPS_URL_AND_QR" {
		t.Fatal("Happ delivery has the wrong format")
	}
	if delivery.URL != source {
		t.Fatal("Happ delivery wrapped the subscription URL")
	}
}

func TestBuildDeliveryRejectsInvalidSubscriptionURLs(t *testing.T) {
	invalidURLs := []string{
		"http://sub.example.com/sub/fixture-token",
		"https:///sub/fixture-token",
		"https://user@sub.example.com/sub/fixture-token",
		"https://sub.example.com/sub/fixture-token#fragment",
		"https://sub.example.com/sub/",
		"https://sub.example.com/sub/one/two",
		"https://sub.example.com/sub/fixture-token?format=links&extra=value",
		"https://sub.example.com/sub/fixture-token?format=other",
	}

	for _, source := range invalidURLs {
		_, err := BuildDelivery("HAPP", source)
		if !errors.Is(err, ErrInvalidSubscriptionURL) {
			t.Fatal("invalid subscription URL was accepted")
		}
	}
}

func TestBuildDeliveryUsesGenericErrorsWithoutSubscriptionLeakage(t *testing.T) {
	const privateToken = "private-delivery-token"
	const invalidSource = "http://sub.example.com/sub/private-delivery-token"

	_, err := BuildDelivery("HAPP", invalidSource)
	if !errors.Is(err, ErrInvalidSubscriptionURL) {
		t.Fatal("invalid subscription URL returned the wrong error")
	}
	if strings.Contains(err.Error(), invalidSource) || strings.Contains(err.Error(), privateToken) {
		t.Fatal("delivery error leaked subscription data")
	}

	_, err = BuildDelivery("UNSUPPORTED", "https://sub.example.com/sub/fixture-token")
	if !errors.Is(err, ErrUnsupportedDeliveryClient) {
		t.Fatal("unsupported client returned the wrong error")
	}
	if strings.Contains(err.Error(), "fixture-token") {
		t.Fatal("unsupported client error leaked subscription data")
	}
}
