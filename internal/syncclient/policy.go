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
