package store

import (
	"context"
	"net"
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5"
)

type requestMetadataKey struct{}

// RequestMetadata is what an audit event records about the request that
// caused it.
type RequestMetadata struct {
	IP        string
	UserAgent string
}

// WithRequestMetadata carries a request's metadata to the audit events it
// causes.
func WithRequestMetadata(ctx context.Context, metadata RequestMetadata) context.Context {
	return context.WithValue(ctx, requestMetadataKey{}, metadata)
}

func requestMetadataFrom(ctx context.Context) (RequestMetadata, bool) {
	metadata, ok := ctx.Value(requestMetadataKey{}).(RequestMetadata)
	return metadata, ok && (metadata.IP != "" || metadata.UserAgent != "")
}

// RequestMetadataHandler records the client address and user agent of each
// changing request for the audit events it causes.
func RequestMetadataHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			ip := r.RemoteAddr
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				ip = host
			}
			userAgent := r.UserAgent()
			if len(userAgent) > 512 {
				userAgent = userAgent[:512]
			}
			r = r.WithContext(WithRequestMetadata(r.Context(), RequestMetadata{IP: ip, UserAgent: userAgent}))
		}
		next.ServeHTTP(w, r)
	})
}

// annotatedConnections are pool connections carrying a request's metadata;
// they are cleared before returning to the pool.
var annotatedConnections sync.Map

// prepareRequestConnection sets the request's metadata on a connection as it
// leaves the pool, where the audit event trigger reads it.
func prepareRequestConnection(ctx context.Context, conn *pgx.Conn) (bool, error) {
	metadata, ok := requestMetadataFrom(ctx)
	if !ok {
		return true, nil
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('zzira.request_ip',$1,false),set_config('zzira.user_agent',$2,false)`, metadata.IP, metadata.UserAgent); err != nil {
		return false, err
	}
	annotatedConnections.Store(conn, true)
	return true, nil
}

// releaseRequestConnection clears a request's metadata from a connection
// returning to the pool, discarding the connection if that fails.
func releaseRequestConnection(conn *pgx.Conn) bool {
	if _, annotated := annotatedConnections.LoadAndDelete(conn); !annotated {
		return true
	}
	_, err := conn.Exec(context.Background(), `SELECT set_config('zzira.request_ip','',false),set_config('zzira.user_agent','',false)`)
	return err == nil
}
