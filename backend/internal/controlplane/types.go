package controlplane

// PaymentState is the durable payment decision recorded on an order.
type PaymentState string

const (
	PaymentPending   PaymentState = "pending"
	PaymentClaimed   PaymentState = "claimed"
	PaymentConfirmed PaymentState = "confirmed"
	PaymentRejected  PaymentState = "rejected"
)

// ProvisioningState is the durable provisioning lifecycle of an order.
type ProvisioningState string

const (
	ProvisioningPending  ProvisioningState = "pending"
	ProvisioningApplying ProvisioningState = "applying"
	ProvisioningApplied  ProvisioningState = "applied"
	ProvisioningFailed   ProvisioningState = "failed"
)

// NodeApplyState is the durable outbox/apply lifecycle for a target service.
type NodeApplyState string

const (
	NodeApplyPending  NodeApplyState = "pending"
	NodeApplyApplying NodeApplyState = "applying"
	NodeApplyApplied  NodeApplyState = "applied"
	NodeApplyFailed   NodeApplyState = "failed"
)

// TelegramInboxState is the durable state of one normalized Telegram update.
type TelegramInboxState string

const (
	TelegramInboxPending    TelegramInboxState = "pending"
	TelegramInboxProcessing TelegramInboxState = "processing"
	TelegramInboxApplied    TelegramInboxState = "applied"
	TelegramInboxRejected   TelegramInboxState = "rejected"
)

// OrderDecision is separate from payment and provisioning progress.
type OrderDecision string

const (
	OrderConfirmed OrderDecision = "confirmed"
	OrderRejected  OrderDecision = "rejected"
	OrderExpired   OrderDecision = "expired"
	OrderCancelled OrderDecision = "cancelled"
)

// OperationState is the durable lifecycle of a bounded operation.
type OperationState string

const (
	OperationPending  OperationState = "pending"
	OperationApplying OperationState = "applying"
	OperationApplied  OperationState = "applied"
	OperationFailed   OperationState = "failed"
)

// ExternalActionState distinguishes a known failure from an unknown outcome.
type ExternalActionState string

const (
	ExternalActionPending  ExternalActionState = "pending"
	ExternalActionApplying ExternalActionState = "applying"
	ExternalActionApplied  ExternalActionState = "applied"
	ExternalActionUnknown  ExternalActionState = "unknown"
	ExternalActionFailed   ExternalActionState = "failed"
)
