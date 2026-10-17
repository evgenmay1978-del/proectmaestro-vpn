package controlplane

import "encoding/binary"

const legacyPublicIdempotencyVersion = "legacy-public-v1"

// LegacyPublicIdempotencyKey derives a cluster-stable opaque key for an
// unauthenticated compatibility request that already has a stable identity.
func (s *Service) LegacyPublicIdempotencyKey(route string, values ...string) (string, error) {
	if s == nil || s.store == nil || s.store.secrets == nil || route == "" {
		return "", ErrUnavailable
	}

	framed := make([]byte, 0)
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		framed = append(framed, length[:]...)
		framed = append(framed, value...)
	}
	defer clear(framed)

	digest := s.store.secrets.LookupHMAC(legacyPublicIdempotencyVersion+":"+route, framed)
	if digest == "" {
		return "", ErrUnavailable
	}
	return legacyPublicIdempotencyVersion + ":" + digest, nil
}
