//go:build rqlite_integration

package controlplane

import (
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestMigrationWhiteListCommercialBalanceEnforcesLedgerInvariants(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	const (
		customerID       = "whitelist-balance-customer"
		otherCustomerID  = "whitelist-balance-other-customer"
		entitlementID    = "wl-ent-00000000000000000000000000000011"
		otherEntitlement = "wl-ent-00000000000000000000000000000012"
		orderID          = "whitelist-balance-access-order"
		otherOrderID     = "whitelist-balance-other-order"
		foreignOrderID   = "whitelist-balance-foreign-order"
		unconfirmedOrder = "whitelist-balance-unconfirmed-order"
		periodID         = "whitelist-balance-period"
		otherPeriodID    = "whitelist-balance-other-period"
		meterEpoch       = "origin-s2-source-xray-boot-a-reset-0"
		otherMeterEpoch  = "origin-s2-source-xray-boot-a-reset-1"
		intervalID       = "whitelist-balance-interval-1"
		secondIntervalID = "whitelist-balance-interval-2"
		purchasedEntryID = "whitelist-balance-purchased-entry"
		consumedEntryID  = "whitelist-balance-consumed-entry"
		usageID          = "whitelist-balance-usage"
		xrayIdentity     = "wl:" + entitlementID
	)

	mustRequest(t, ctx, db,
		rqlite.Statement{
			SQL: `INSERT INTO customers(
				customer_id,display_login,login_key_hmac,status,expires_at_unix,
				generation,created_at_unix,updated_at_unix
			) VALUES(?,?,?,'active',5000000,1,1000000,1000000)`,
			Args: []any{customerID, "WhitelistBalance", strings.Repeat("1", 64)},
		},
		rqlite.Statement{
			SQL: `INSERT INTO customers(
				customer_id,display_login,login_key_hmac,status,expires_at_unix,
				generation,created_at_unix,updated_at_unix
			) VALUES(?,?,?,'active',5000000,1,1000000,1000000)`,
			Args: []any{otherCustomerID, "WhitelistBalanceOther", strings.Repeat("2", 64)},
		},
		rqlite.Statement{
			SQL:  `INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) VALUES(?,?,1000000)`,
			Args: []any{entitlementID, customerID},
		},
		rqlite.Statement{
			SQL:  `INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) VALUES(?,?,1000000)`,
			Args: []any{otherEntitlement, otherCustomerID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO orders(
				order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
				amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
				provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id
			) VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,1000000,1086400,'confirmed','applied','confirmed',1000000,5000000,1,?)`,
			Args: []any{orderID, "WLBAL1", "migration", strings.Repeat("3", 64), customerID, "whitelist-balance-order-op-1"},
		},
		rqlite.Statement{
			SQL: `INSERT INTO orders(
				order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
				amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
				provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id
			) VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,1000000,1086400,'confirmed','applied','confirmed',1000000,5000000,1,?)`,
			Args: []any{otherOrderID, "WLBAL2", "migration", strings.Repeat("4", 64), customerID, "whitelist-balance-order-op-2"},
		},
		rqlite.Statement{
			SQL: `INSERT INTO orders(
				order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
				amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
				provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id
			) VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,1000000,1086400,'confirmed','applied','confirmed',1000000,5000000,1,?)`,
			Args: []any{foreignOrderID, "WLBAL3", "migration", strings.Repeat("d", 64), otherCustomerID, "whitelist-balance-order-op-3"},
		},
		rqlite.Statement{
			SQL: `INSERT INTO orders(
				order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
				amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
				provisioning_state,operation_id
			) VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,1000000,1086400,'created','none',?)`,
			Args: []any{unconfirmedOrder, "WLBAL4", "migration", strings.Repeat("e", 64), customerID, "whitelist-balance-order-op-4"},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_billing_periods(
				period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
				included_grant_bytes,access_order_id,created_at_unix
			) VALUES(?,?,0,1000000,3592000,2000000000,?,1000000)`,
			Args: []any{periodID, entitlementID, orderID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_billing_periods(
				period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
				included_grant_bytes,access_order_id,created_at_unix
			) VALUES(?,?,0,1000000,3592000,1000000000,?,1000000)`,
			Args: []any{otherPeriodID, otherEntitlement, foreignOrderID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_periods(
				entitlement_id,billing_period_id,account_id,transport_id,xray_identity,unit,basis,
				included_bytes,soft_limit_bytes,hard_limit_bytes,grace_bytes,price_mode,price_source,
				currency,minor_units_per_unit,policy_sha256,created_at_unix
			) VALUES(?,?,?,?,?,'GB_DECIMAL','UPLINK_PLUS_DOWNLINK','2000000000','0','0','0','PAID','GLOBAL','RUB','100',?,1000000)`,
			Args: []any{entitlementID, periodID, customerID, "yandex-cdn", xrayIdentity, strings.Repeat("5", 64)},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_meter_epochs(
				meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix
			) VALUES(?,?,?,?,0,1000000)`,
			Args: []any{meterEpoch, "origin-s2", "origin-s2-xray", "boot-a"},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_meter_epochs(
				meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix
			) VALUES(?,?,?,?,1,1000000)`,
			Args: []any{otherMeterEpoch, "origin-s2", "origin-s2-xray", "boot-a"},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_events(
				event_id,entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
				counter_generation,sample_sequence,uplink_bytes,downlink_bytes,payload_sha256,
				diagnostic,has_interval,result_json,created_at_unix
			) VALUES(?,?,?,?,?,?,'1','2','20','30',?,'',1,'{}',1000100)`,
			Args: []any{intervalID, entitlementID, periodID, "origin-s2", meterEpoch, xrayIdentity, strings.Repeat("6", 64)},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_intervals(
				event_id,uplink_delta_bytes,downlink_delta_bytes,billable_bytes,
				amount_numerator,amount_denominator,currency,created_at_unix
			) VALUES(?,'20','30','50','5000','1000000000','RUB',1000100)`,
			Args: []any{intervalID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_events(
				event_id,entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
				counter_generation,sample_sequence,uplink_bytes,downlink_bytes,payload_sha256,
				diagnostic,has_interval,result_json,created_at_unix
			) VALUES(?,?,?,?,?,?,'1','3','30','50',?,'',1,'{}',1000200)`,
			Args: []any{secondIntervalID, entitlementID, periodID, "origin-s2", meterEpoch, xrayIdentity, strings.Repeat("7", 64)},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_intervals(
				event_id,uplink_delta_bytes,downlink_delta_bytes,billable_bytes,
				amount_numerator,amount_denominator,currency,created_at_unix
			) VALUES(?,'10','20','30','3000','1000000000','RUB',1000200)`,
			Args: []any{secondIntervalID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_entries(
				entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
				consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
				idempotency_key,metadata_sha256,created_at_unix
			) VALUES(?,?,?,'INCLUDED_GRANT',2000000000,0,0,0,?,NULL,?,?,1000000)`,
			Args: []any{"whitelist-balance-grant-entry", entitlementID, periodID, orderID, "whitelist-balance-grant-key", strings.Repeat("8", 64)},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_entries(
				entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
				consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
				idempotency_key,metadata_sha256,created_at_unix
			) VALUES(?,?,?,'PURCHASED_CREDIT',0,1000000000,0,0,?,NULL,?,?,1000050)`,
			Args: []any{purchasedEntryID, entitlementID, periodID, orderID, "whitelist-balance-purchased-key", strings.Repeat("9", 64)},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_entries(
				entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
				consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
				idempotency_key,metadata_sha256,created_at_unix
			) VALUES(?,?,?,'CONSUMED',-50,0,50,0,NULL,?,?,?,1000100)`,
			Args: []any{consumedEntryID, entitlementID, periodID, intervalID, "whitelist-balance-consumed-key", strings.Repeat("a", 64)},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_usage_applications(
				application_id,entitlement_id,period_id,meter_epoch,interval_id,entry_id,applied_at_unix
			) VALUES(?,?,?,?,?,?,1000100)`,
			Args: []any{usageID, entitlementID, periodID, meterEpoch, intervalID, consumedEntryID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_projections(
				entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
				lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix
			) VALUES(?,?,1999999950,1000000000,50,0,1,0,1000300,1000200)`,
			Args: []any{entitlementID, periodID},
		},
	)

	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_meter_epochs(
			meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix
		) VALUES(?,?,?,?,1,1000300)`,
		Args: []any{meterEpoch, "origin-s3", "origin-s3-xray", "boot-b"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_meter_epochs(
			meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix
		) VALUES(?,?,?,?,0,1000300)`,
		Args: []any{"different-epoch", "origin-s2", "origin-s2-xray", "boot-a"},
	})

	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
			period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
			included_grant_bytes,access_order_id,created_at_unix
		) VALUES(?,?,0,1000000,3592000,1,?,1000000)`,
		Args: []any{"whitelist-balance-duplicate-sequence", entitlementID, otherOrderID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
			period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
			included_grant_bytes,access_order_id,created_at_unix
		) VALUES(?,?,0,1000000,3592000,1,?,1000000)`,
		Args: []any{"whitelist-balance-duplicate-access-grant", otherEntitlement, orderID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
			period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
			included_grant_bytes,access_order_id,created_at_unix
		) VALUES(?,?,2,1000000,3592000,1,'missing-order',1000000)`,
		Args: []any{"whitelist-balance-missing-order-period", entitlementID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
			period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
			included_grant_bytes,access_order_id,created_at_unix
		) VALUES(?,?,2,1000000,3592000,1,?,1000000)`,
		Args: []any{"whitelist-balance-foreign-order-period", entitlementID, foreignOrderID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
			period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
			included_grant_bytes,access_order_id,created_at_unix
		) VALUES(?,?,3,1000000,3592000,9223372036854775807,?,1000000)`,
		Args: []any{"whitelist-balance-max-int-period", entitlementID, otherOrderID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
			period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
			included_grant_bytes,access_order_id,created_at_unix
		) VALUES(?,?,4,1000000,3592000,1,?,1000000)`,
		Args: []any{"whitelist-balance-unconfirmed-order-period", entitlementID, unconfirmedOrder},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
			period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
			included_grant_bytes,access_order_id,created_at_unix
		) VALUES(?,?,5,1000000,3592000,0.5,?,1000000)`,
		Args: []any{"whitelist-balance-fractional-period", entitlementID, otherOrderID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_meter_epochs(
			meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix
		) VALUES('fractional-meter-epoch','origin-s4','origin-s4-xray','boot-fractional',0.5,1000300)`,
	})

	insertBalance := func(entryID, kind string, includedDelta, purchasedDelta, consumedDelta, uncoveredDelta int64, sourceOrderID, sourceIntervalID any, idempotencyKey string) rqlite.Statement {
		return rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_entries(
				entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
				consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
				idempotency_key,metadata_sha256,created_at_unix
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,1000300)`,
			Args: []any{entryID, entitlementID, periodID, kind, includedDelta, purchasedDelta, consumedDelta, uncoveredDelta, sourceOrderID, sourceIntervalID, idempotencyKey, strings.Repeat("b", 64)},
		}
	}
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-duplicate-purchased", "PURCHASED_CREDIT", 0, 1, 0, 0,
		orderID, nil, "whitelist-balance-duplicate-purchased-key",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-duplicate-included", "INCLUDED_GRANT", 1, 0, 0, 0,
		orderID, nil, "whitelist-balance-duplicate-included-key",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-wrong-order-included", "INCLUDED_GRANT", 1, 0, 0, 0,
		otherOrderID, nil, "whitelist-balance-wrong-order-included-key",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-consumed-mints-purchased", "CONSUMED", 0, 1, 1, 0,
		nil, secondIntervalID, "whitelist-balance-invalid-consumed-shape-key",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-credit-mutates-included", "PURCHASED_CREDIT", 1, 1, 0, 0,
		otherOrderID, nil, "whitelist-balance-invalid-purchased-shape-key",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-unconfirmed-order-credit", "PURCHASED_CREDIT", 0, 1, 0, 0,
		unconfirmedOrder, nil, "whitelist-balance-unconfirmed-order-credit-key",
	))
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_entries(
			entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
			consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
			idempotency_key,metadata_sha256,created_at_unix
		) VALUES(?,?,?,'PURCHASED_CREDIT',0,0.5,0,0,?,NULL,?,?,1000300)`,
		Args: []any{"whitelist-balance-fractional-credit", entitlementID, periodID, otherOrderID, "whitelist-balance-fractional-credit-key", strings.Repeat("f", 64)},
	})
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-duplicate-idempotency", "ADJUSTMENT", 0, 1, 0, 0,
		otherOrderID, nil, "whitelist-balance-purchased-key",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-purchased-from-interval", "PURCHASED_CREDIT", 0, 1, 0, 0,
		nil, secondIntervalID, "whitelist-balance-invalid-source-key-1",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-consumed-from-order", "CONSUMED", 0, 0, 1, 0,
		otherOrderID, nil, "whitelist-balance-invalid-source-key-2",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-both-sources", "ADJUSTMENT", 0, 0, 0, 1,
		otherOrderID, secondIntervalID, "whitelist-balance-invalid-source-key-3",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-missing-interval", "CONSUMED", 0, 0, 1, 0,
		nil, "missing-interval", "whitelist-balance-missing-interval-key",
	))
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_entries(
			entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
			consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
			idempotency_key,metadata_sha256,created_at_unix
		) VALUES(?,?,?,'ADJUSTMENT',0,1,0,0,?,NULL,?,?,1000300)`,
		Args: []any{"whitelist-balance-cross-period-entry", otherEntitlement, periodID, otherOrderID, "whitelist-balance-cross-period-key", strings.Repeat("c", 64)},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_entries(
			entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
			consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
			idempotency_key,metadata_sha256,created_at_unix
		) VALUES(?,?,?,'CONSUMED',-1,0,1,0,NULL,?,?,?,1000300)`,
		Args: []any{"whitelist-balance-cross-interval-entry", otherEntitlement, otherPeriodID, intervalID, "whitelist-balance-cross-interval-key", strings.Repeat("e", 64)},
	})
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-foreign-order-credit", "PURCHASED_CREDIT", 0, 1, 0, 0,
		foreignOrderID, nil, "whitelist-balance-foreign-order-credit-key",
	))
	mustRequestFail(t, ctx, db, insertBalance(
		"whitelist-balance-max-int-adjustment", "ADJUSTMENT", 0, 9223372036854775807, 0, 0,
		otherOrderID, nil, "whitelist-balance-max-int-adjustment-key",
	))

	mustRequest(t, ctx, db, insertBalance(
		"whitelist-balance-second-consumed-entry", "CONSUMED", -20, -10, 50, 20,
		nil, secondIntervalID, "whitelist-balance-second-consumed-key",
	))
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_usage_applications(
			application_id,entitlement_id,period_id,meter_epoch,interval_id,entry_id,applied_at_unix
		) VALUES(?,?,?,?,?,?,1000300)`,
		Args: []any{"whitelist-balance-wrong-epoch-usage", entitlementID, periodID, otherMeterEpoch, secondIntervalID, "whitelist-balance-second-consumed-entry"},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_usage_applications(
			application_id,entitlement_id,period_id,meter_epoch,interval_id,entry_id,applied_at_unix
		) VALUES(?,?,?,?,?,?,1000300.5)`,
		Args: []any{"whitelist-balance-fractional-usage", entitlementID, periodID, meterEpoch, secondIntervalID, "whitelist-balance-second-consumed-entry"},
	})
	mustRequest(t, ctx, db, insertBalance(
		"whitelist-balance-replay-entry", "UNCOVERED", 0, 0, 50, 50,
		nil, intervalID, "whitelist-balance-replay-entry-key",
	))
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_usage_applications(
			application_id,entitlement_id,period_id,meter_epoch,interval_id,entry_id,applied_at_unix
		) VALUES(?,?,?,?,?,?,1000300)`,
		Args: []any{"whitelist-balance-replay-usage", entitlementID, periodID, meterEpoch, intervalID, "whitelist-balance-replay-entry"},
	})

	for _, statement := range []rqlite.Statement{
		{SQL: `UPDATE whitelist_meter_epochs SET origin_id='origin-s3' WHERE meter_epoch=?`, Args: []any{meterEpoch}},
		{SQL: `DELETE FROM whitelist_meter_epochs WHERE meter_epoch=?`, Args: []any{meterEpoch}},
		{SQL: `UPDATE whitelist_billing_periods SET ends_at_unix=3592001 WHERE period_id=?`, Args: []any{periodID}},
		{SQL: `DELETE FROM whitelist_billing_periods WHERE period_id=?`, Args: []any{periodID}},
		{SQL: `UPDATE whitelist_balance_entries SET purchased_delta_bytes=2 WHERE entry_id=?`, Args: []any{purchasedEntryID}},
		{SQL: `DELETE FROM whitelist_balance_entries WHERE entry_id=?`, Args: []any{purchasedEntryID}},
		{SQL: `UPDATE whitelist_usage_applications SET applied_at_unix=1000400 WHERE application_id=?`, Args: []any{usageID}},
		{SQL: `DELETE FROM whitelist_usage_applications WHERE application_id=?`, Args: []any{usageID}},
		{SQL: `DELETE FROM whitelist_balance_projections WHERE entitlement_id=?`, Args: []any{entitlementID}},
	} {
		mustRequestFail(t, ctx, db, statement)
	}

	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_projections(
			entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
			lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix
		) VALUES(?,?,0,0,0,0,1,0,1000300,1000300)`,
		Args: []any{otherEntitlement, periodID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_projections(
			entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
			lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix
		) VALUES(?,?,0,0,0,0,2,0,1000300,1000300)`,
		Args: []any{otherEntitlement, otherPeriodID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  `UPDATE whitelist_balance_projections SET version=1 WHERE entitlement_id=?`,
		Args: []any{entitlementID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  `UPDATE whitelist_balance_projections SET version=0 WHERE entitlement_id=?`,
		Args: []any{entitlementID},
	})
	mustRequestFail(t, ctx, db, rqlite.Statement{
		SQL:  `UPDATE whitelist_balance_projections SET version=3 WHERE entitlement_id=? AND version=1`,
		Args: []any{entitlementID},
	})
	mustRequest(t, ctx, db, rqlite.Statement{
		SQL:  `UPDATE whitelist_balance_projections SET version=2,updated_at_unix=1000300 WHERE entitlement_id=? AND version=1`,
		Args: []any{entitlementID},
	})
	projection := mustStrongQuery(t, ctx, db, rqlite.Statement{
		SQL:  `SELECT version FROM whitelist_balance_projections WHERE entitlement_id=?`,
		Args: []any{entitlementID},
	})
	if len(projection.Rows) != 1 {
		t.Fatalf("projection after monotonic update = %#v", projection.Rows)
	}
	version, versionOK := rowInt64(projection.Rows[0], "version")
	if !versionOK || version != 2 {
		t.Fatalf("projection after monotonic update = %#v", projection.Rows)
	}
}
