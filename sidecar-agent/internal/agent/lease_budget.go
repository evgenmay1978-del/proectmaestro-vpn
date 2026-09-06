package agent

// Clone mutable wire fields before hashing, journaling, or returning a pending
// operation. A caller cannot replace a durable nonce's ceilings through a map.
func cloneUseLeaseRequest(request UseLeaseRequest) UseLeaseRequest {
	request.Emails = append([]string{}, request.Emails...)
	if request.CumulativeByteCeilings != nil {
		ceilings := make(map[string]int64, len(request.CumulativeByteCeilings))
		for email, ceiling := range request.CumulativeByteCeilings {
			ceilings[email] = ceiling
		}
		request.CumulativeByteCeilings = ceilings
	}
	if request.ExpectedFenceGenerations != nil {
		generations := make(map[string]uint64, len(request.ExpectedFenceGenerations))
		for email, generation := range request.ExpectedFenceGenerations {
			generations[email] = generation
		}
		request.ExpectedFenceGenerations = generations
	}
	return request
}

func matchesByteFenceGenerations(state *leaseState, request UseLeaseRequest) bool {
	if request.Schema != 3 {
		return true
	}
	for _, email := range request.Emails {
		expected, supplied := request.ExpectedFenceGenerations[email]
		user, exists := state.Users[leaseUserKey(request.XrayProcessBootID, email)]
		if !supplied || !exists || expected != user.LastFencedGeneration {
			return false
		}
	}
	return true
}
