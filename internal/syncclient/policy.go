// Package syncclient contains platform-neutral policy shared by the browser
// sync worker and ordinary Go tests.
package syncclient

// OutboxDisposition decides whether an HTTP response permanently acknowledges
// a queued command. Transient responses must retain the command to avoid data
// loss during overloads and short-lived upstream failures.
type OutboxDisposition int

const (
	OutboxRetry OutboxDisposition = iota
	OutboxAccepted
	OutboxRejected
)

func DispositionForStatus(status int) OutboxDisposition {
	switch {
	case status >= 200 && status < 300:
		return OutboxAccepted
	case status == 408 || status == 425 || status == 429 || status >= 500:
		return OutboxRetry
	case status >= 400 && status < 500:
		return OutboxRejected
	default:
		return OutboxRetry
	}
}

type SyncDisposition int

const (
	SyncRetry SyncDisposition = iota
	SyncAccepted
	SyncRevoked
)

// DispositionForSyncStatus treats authentication and authorization failures as
// a replica revocation boundary. A private offline copy must be purged before a
// different user or a restored account can reuse the browser context.
func DispositionForSyncStatus(status int) SyncDisposition {
	switch status {
	case 200, 304:
		return SyncAccepted
	case 401, 403:
		return SyncRevoked
	default:
		return SyncRetry
	}
}
