package model

// SafeDeliveryServerCode accepts protocol vocabulary only. An arbitrary peer
// string can contain credentials even when it looks like a harmless identifier.
func SafeDeliveryServerCode(code string) string {
	switch code {
	case "contract_mismatch", "observation_contract_mismatch", "revision_mismatch", "project_mismatch", "topology_reloaded",
		"sample_too_old", "json_required", "observer_scope_violation", "sample_time_out_of_range", "unsupported_batch_schema", "invalid_batch_revision", "invalid_batch_size", "invalid_observation_identity",
		"received_at_is_collector_owned", "invalid_outcome", "evidence_kind_mismatch", "heartbeat_limit_exceeded", "invalid_delivery_status", "invalid_batch_json",
		"observer_storage_quota", "storage_receipt_capacity", "storage_unavailable", "authentication_required", "invalid_token", "role_forbidden",
		"observer_heartbeat_rate", "rate_limited", "concurrency_limited", "connection_limited", "connection_rotating", "preauth_busy", "preauth_rate_limited", "snapshot_busy":
		return code
	}
	return ""
}
func ValidDeliveryCode(code string) bool {
	switch code {
	case "", "collector_unreachable", "storage_backpressure", "authorization_rejected", "sample_rejected", "local_ack_failed", "local_quarantine_failed", "local_read_failed",
		"dns_failure", "dns_timeout", "tcp_failure", "tcp_timeout", "tls_failure", "tls_timeout", "response_timeout", "response_read_timeout", "connection_closed", "connection_rotating", "transport_failure",
		"rate_limited", "configuration_mismatch", "server_error", "ack_format_invalid", "ack_identity_mismatch":
		return true
	}
	return false
}
