package subgen

import incylink "github.com/INCY-DEV/incy-link-encoder/go"

const maestroIncyDisplayName = "MaestroVPN"

func encodeIncyOneTap(subscriptionURL string) (string, error) {
	return incylink.EncryptLink(subscriptionURL, maestroIncyDisplayName)
}
