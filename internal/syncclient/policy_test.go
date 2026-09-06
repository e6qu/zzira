package syncclient

import "testing"

func TestDispositionForStatus(t *testing.T) {
	tests := []struct {
		status int
		want   OutboxDisposition
	}{
		{200, OutboxAccepted},
		{204, OutboxAccepted},
		{400, OutboxRejected},
		{404, OutboxRejected},
		{409, OutboxRejected},
		{408, OutboxRetry},
		{425, OutboxRetry},
		{429, OutboxRetry},
		{500, OutboxRetry},
		{503, OutboxRetry},
		{302, OutboxRetry},
	}
	for _, tt := range tests {
		if got := DispositionForStatus(tt.status); got != tt.want {
			t.Errorf("DispositionForStatus(%d)=%v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestDispositionForSyncStatus(t *testing.T) {
	for _, test := range []struct {
		status int
		want   SyncDisposition
	}{{200, SyncAccepted}, {304, SyncAccepted}, {401, SyncRevoked}, {403, SyncRevoked}, {429, SyncRetry}, {500, SyncRetry}} {
		if got := DispositionForSyncStatus(test.status); got != test.want {
			t.Errorf("DispositionForSyncStatus(%d)=%v, want %v", test.status, got, test.want)
		}
	}
}
