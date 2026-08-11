package importer

import (
	"encoding/json"
	"sort"
	"strings"
)

func Validate(snapshot Snapshot, options PlanOptions) []Blocker {
	blockers := make(map[string]Blocker)
	add := func(code, entity, sourceKey string) {
		if _, exists := blockers[code]; !exists {
			blockers[code] = Blocker{Code: code, Entity: entity, SourceKey: sourceKey}
		}
	}

	customersBySource := make(map[string]LegacyCustomer, len(snapshot.Customers))
	loginKeys := make(map[string]string)
	uuidHMACs := make(map[string]string)
	subIDHMACs := make(map[string]string)
	tokenHMACs := make(map[string]string)
	credentialHMACs := make(map[string]string)
	for _, customer := range snapshot.Customers {
		customersBySource[customer.SourceKey] = customer
		collision(loginKeys, strings.ToLower(strings.TrimSpace(customer.Login)), customer.SourceKey, func() {
			add("login_collision", "customer", customer.SourceKey)
		})
		collision(uuidHMACs, customer.UUIDHMAC, customer.SourceKey, func() {
			add("uuid_collision", "customer", customer.SourceKey)
		})
		collision(subIDHMACs, customer.SubIDHMAC, customer.SourceKey, func() {
			add("sub_id_collision", "customer", customer.SourceKey)
		})
		collision(tokenHMACs, customer.TokenHMAC, customer.SourceKey, func() {
			add("token_hmac_collision", "customer", customer.SourceKey)
		})
		collision(credentialHMACs, customer.CredentialFingerprintHMAC, customer.SourceKey, func() {
			add("credential_collision", "customer", customer.SourceKey)
		})
	}

	for _, order := range snapshot.Orders {
		customer, exists := customersBySource[order.CustomerSourceKey]
		if order.Credited && exists && order.StoredCustomerExpiresAtUnix != customer.ExpiresAtUnix {
			add("expiry_contradiction", "order", order.SourceKey)
		}
	}

	secrets := make(map[string]struct{}, len(snapshot.EncryptedSecrets))
	for _, secret := range snapshot.EncryptedSecrets {
		secrets[secret.SecretID] = struct{}{}
	}
	for _, customer := range snapshot.Customers {
		if customer.IdentitySecretRef != "" {
			if _, exists := secrets[customer.IdentitySecretRef]; !exists {
				add("missing_customer_secret", "customer", customer.SourceKey)
			}
		}
	}
	for _, principal := range snapshot.Principals {
		if principal.CredentialSecretRef == "" {
			add("missing_principal_secret", "principal", principal.SourceKey)
			continue
		}
		if _, exists := secrets[principal.CredentialSecretRef]; !exists {
			add("missing_principal_secret", "principal", principal.SourceKey)
		}
	}
	for _, setting := range snapshot.Settings {
		if setting.SecretRef == "" {
			continue
		}
		if _, exists := secrets[setting.SecretRef]; !exists {
			add("missing_setting_secret", "setting", setting.Key)
		}
	}

	supportedSchemas := make(map[string]struct{}, len(options.SupportedBotSchemas))
	for _, schema := range options.SupportedBotSchemas {
		supportedSchemas[schema] = struct{}{}
	}
	for _, binding := range snapshot.BotBindings {
		if _, exists := supportedSchemas[binding.SchemaFingerprint]; !exists {
			add("unsupported_bot_schema", "bot_binding", binding.BotIdentityHMAC)
		}
	}

	if snapshot.SnapshotKind == "delta" {
		validateDelta(snapshot, options, add)
	}

	result := make([]Blocker, 0, len(blockers))
	for _, blocker := range blockers {
		result = append(result, blocker)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Code != result[j].Code {
			return result[i].Code < result[j].Code
		}
		if result[i].Entity != result[j].Entity {
			return result[i].Entity < result[j].Entity
		}
		return result[i].SourceKey < result[j].SourceKey
	})
	return result
}

func validateDelta(snapshot Snapshot, options PlanOptions, add func(string, string, string)) {
	if snapshot.ParentSourceDigest == "" || snapshot.ParentSourceDigest != options.AppliedParentDigest {
		add("delta_parent_digest_mismatch", "snapshot", "")
	}
	if options.ParentSnapshot == nil {
		add("delta_parent_snapshot_missing", "snapshot", "")
		return
	}
	if digestSnapshot(*options.ParentSnapshot) != snapshot.ParentSourceDigest {
		add("delta_parent_digest_mismatch", "snapshot", "")
	}

	presentCustomers := make(map[string]struct{}, len(snapshot.Customers))
	for _, customer := range snapshot.Customers {
		presentCustomers[customer.SourceKey] = struct{}{}
	}
	explicitDeletes := make(map[string]struct{}, len(snapshot.Deletes))
	for _, deletion := range snapshot.Deletes {
		if !deletion.Explicit {
			add("delta_delete_not_explicit", deletion.Entity, deletion.SourceKey)
			continue
		}
		explicitDeletes[deletion.Entity+"\x00"+deletion.SourceKey] = struct{}{}
	}
	for _, previous := range options.ParentSnapshot.Customers {
		if _, stillPresent := presentCustomers[previous.SourceKey]; stillPresent {
			continue
		}
		if _, deleted := explicitDeletes["customer\x00"+previous.SourceKey]; !deleted {
			add("delta_missing_delete_marker", "customer", previous.SourceKey)
		}
	}
}

func collision(index map[string]string, value, sourceKey string, onCollision func()) {
	if value == "" {
		return
	}
	if previous, exists := index[value]; exists && previous != sourceKey {
		onCollision()
		return
	}
	index[value] = sourceKey
}

func Plan(snapshot Snapshot, options PlanOptions) (ImportPlan, Report) {
	plan := ImportPlan{
		FormatVersion:          snapshot.FormatVersion,
		SnapshotKind:           snapshot.SnapshotKind,
		ParentSourceDigest:     snapshot.ParentSourceDigest,
		SourceDigest:           digestSnapshot(snapshot),
		Trials:                 append([]LegacyTrial(nil), snapshot.Trials...),
		BotBindings:            append([]LegacyBotBinding(nil), snapshot.BotBindings...),
		Settings:               cloneSettings(snapshot.Settings),
		Principals:             clonePrincipals(snapshot.Principals),
		EncryptedSecrets:       append([]LegacyEncryptedSecret(nil), snapshot.EncryptedSecrets...),
		BotPollStates:          append([]LegacyBotPollState(nil), snapshot.BotPollStates...),
		PendingCallbacks:       append([]LegacyCallback(nil), snapshot.PendingCallbacks...),
		BotCredentialRotations: append([]LegacyBotCredentialRotation(nil), snapshot.BotCredentialRotations...),
	}

	customerInternalIDs := make(map[string]string, len(snapshot.Customers))
	for _, customer := range snapshot.Customers {
		internalID := deterministicID(options.Namespace, "customer", customer.SourceKey)
		customerInternalIDs[customer.SourceKey] = internalID
		plan.Customers = append(plan.Customers, PlannedCustomer{
			InternalID:                internalID,
			SourceKey:                 customer.SourceKey,
			DisplayLogin:              customer.Login,
			LoginKeyHMAC:              customer.LoginKeyHMAC,
			UUIDHMAC:                  customer.UUIDHMAC,
			SubIDHMAC:                 customer.SubIDHMAC,
			TokenHMAC:                 customer.TokenHMAC,
			CredentialFingerprintHMAC: customer.CredentialFingerprintHMAC,
			IdentitySecretRef:         customer.IdentitySecretRef,
			ExpiresAtUnix:             customer.ExpiresAtUnix,
			Generation:                customer.Generation,
			Status:                    customer.Status,
		})
	}
	for _, order := range snapshot.Orders {
		planned := PlannedOrder{
			InternalID:        deterministicID(options.Namespace, "order", order.SourceKey),
			SourceKey:         order.SourceKey,
			CustomerInternalID: customerInternalIDs[order.CustomerSourceKey],
			CustomerSourceKey: order.CustomerSourceKey,
			BuyerScope:        order.BuyerScope,
			BuyerKeyHMAC:      order.BuyerKeyHMAC,
			TariffVersionID:   order.TariffVersionID,
			AmountMinor:       order.AmountMinor,
			Currency:          order.Currency,
			DurationDays:      order.DurationDays,
			PaymentCode:       order.PaymentCode,
			CreatedAtUnix:     order.CreatedAtUnix,
			ExpiresAtUnix:     order.CreatedAtUnix + 86400,
			ResultGeneration:  order.ResultGeneration,
			ImportState:       order.State,
		}
		if order.State == "pending" && order.Credited {
			planned.PaymentState = "confirmed"
			planned.ProvisioningState = "pending"
			planned.ResultExpiresAtUnix = order.StoredCustomerExpiresAtUnix
			planned.AuditMarkers = []string{"legacy_credit_preserved"}
		} else if order.State == "pending" {
			planned.PaymentState = "created"
			planned.ProvisioningState = "pending"
			planned.ImportState = "created"
		} else {
			planned.PaymentState = order.State
			planned.ProvisioningState = order.State
		}
		plan.Orders = append(plan.Orders, planned)
	}
	for _, deletion := range snapshot.Deletes {
		plan.Deletes = append(plan.Deletes, PlannedDelete{
			Entity:              deletion.Entity,
			SourceKey:           deletion.SourceKey,
			ExpectedPriorDigest: deletion.ExpectedPriorDigest,
			Tombstone:           deletion.Explicit,
		})
		plan.CascadeDeletes = append(plan.CascadeDeletes, cascadeDeletes(snapshot, options.ParentSnapshot, deletion)...)
	}

	sortPlan(&plan)
	plan.Counts = planCounts(plan)
	plan.Blockers = Validate(snapshot, options)
	plan.PlanDigest = Digest(plan)
	report := Report{
		SourceDigest: plan.SourceDigest,
		PlanDigest:   plan.PlanDigest,
		Counts:       cloneCounts(plan.Counts),
		Blockers:     append([]Blocker(nil), plan.Blockers...),
	}
	return plan, report
}

func cascadeDeletes(snapshot Snapshot, parent *Snapshot, deletion LegacyDelete) []PlannedDelete {
	if parent == nil || deletion.Entity != "customer" || !deletion.Explicit {
		return nil
	}
	var result []PlannedDelete
	for _, secret := range parent.EncryptedSecrets {
		if secret.OwnerType == "customer" && secret.OwnerSourceKey == deletion.SourceKey {
			result = append(result, PlannedDelete{Entity: "encrypted_secret", SourceKey: secret.SecretID, Tombstone: true})
		}
	}
	return result
}

func cloneSettings(settings []LegacySetting) []LegacySetting {
	result := make([]LegacySetting, len(settings))
	for index, setting := range settings {
		result[index] = setting
		result[index].PublicValueJSON = append(json.RawMessage(nil), setting.PublicValueJSON...)
	}
	return result
}

func clonePrincipals(principals []LegacyPrincipal) []LegacyPrincipal {
	result := make([]LegacyPrincipal, len(principals))
	for index, principal := range principals {
		result[index] = principal
		result[index].Roles = append([]string(nil), principal.Roles...)
	}
	return result
}

func sortPlan(plan *ImportPlan) {
	sort.Slice(plan.Customers, func(i, j int) bool { return plan.Customers[i].SourceKey < plan.Customers[j].SourceKey })
	sort.Slice(plan.Orders, func(i, j int) bool { return plan.Orders[i].SourceKey < plan.Orders[j].SourceKey })
	sort.Slice(plan.Settings, func(i, j int) bool { return plan.Settings[i].Key < plan.Settings[j].Key })
	sort.Slice(plan.Principals, func(i, j int) bool { return plan.Principals[i].SourceKey < plan.Principals[j].SourceKey })
	sort.Slice(plan.EncryptedSecrets, func(i, j int) bool { return plan.EncryptedSecrets[i].SecretID < plan.EncryptedSecrets[j].SecretID })
	sort.Slice(plan.Deletes, func(i, j int) bool {
		return plan.Deletes[i].Entity+"\x00"+plan.Deletes[i].SourceKey < plan.Deletes[j].Entity+"\x00"+plan.Deletes[j].SourceKey
	})
	sort.Slice(plan.CascadeDeletes, func(i, j int) bool {
		return plan.CascadeDeletes[i].Entity+"\x00"+plan.CascadeDeletes[i].SourceKey < plan.CascadeDeletes[j].Entity+"\x00"+plan.CascadeDeletes[j].SourceKey
	})
}

func planCounts(plan ImportPlan) map[string]int {
	return map[string]int{
		"customers":          len(plan.Customers),
		"orders":             len(plan.Orders),
		"trials":             len(plan.Trials),
		"bot_bindings":       len(plan.BotBindings),
		"settings":           len(plan.Settings),
		"principals":         len(plan.Principals),
		"encrypted_secrets":  len(plan.EncryptedSecrets),
		"deletes":            len(plan.Deletes),
		"bot_poll_states":    len(plan.BotPollStates),
		"pending_callbacks":  len(plan.PendingCallbacks),
		"credential_rotations": len(plan.BotCredentialRotations),
	}
}

func cloneCounts(counts map[string]int) map[string]int {
	result := make(map[string]int, len(counts))
	for key, value := range counts {
		result[key] = value
	}
	return result
}
