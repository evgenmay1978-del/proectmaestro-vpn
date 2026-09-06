package sidecaragentclient

import "time"

// NewByteBudgetUseLeaseRequest binds an absolute physical-boot byte ceiling to
// the original authenticated agent-clock challenge.
func NewByteBudgetUseLeaseRequest(snapshot UsageSnapshot, budget time.Duration, emails []string, ceilings map[string]int64, fences map[string]uint64) (UseLeaseRequest, error) {
	if !validByteCeilings(3, emails, ceilings, fences) {
		return UseLeaseRequest{}, ErrInvalidRequest
	}
	request, err := NewUseLeaseRequest(snapshot, budget, emails)
	if err != nil {
		return UseLeaseRequest{}, err
	}
	request.Schema = 3
	request.CumulativeByteCeilings = make(map[string]int64, len(ceilings))
	request.ExpectedFenceGenerations = make(map[string]uint64, len(fences))
	for email, ceiling := range ceilings {
		request.CumulativeByteCeilings[email] = ceiling
		request.ExpectedFenceGenerations[email] = fences[email]
	}
	return request, nil
}

func validByteCeilings(schema int, emails []string, ceilings map[string]int64, fences map[string]uint64) bool {
	if schema == 2 {
		return len(ceilings) == 0 && len(fences) == 0
	}
	if schema != 3 || len(emails) != len(ceilings) || len(emails) != len(fences) {
		return false
	}
	for _, email := range emails {
		if _, ok := fences[email]; !ok || ceilings[email] <= 0 {
			return false
		}
	}
	return true
}
