package transport

import (
	"net/http"
	"sort"
	"strings"

	grpcmetadata "google.golang.org/grpc/metadata"
)

// HTTPHeaderCarrier adapts HTTP headers to metadata.Carrier.
type HTTPHeaderCarrier struct{ Header http.Header }

func (carrier HTTPHeaderCarrier) Get(key string) string {
	return carrier.Header.Get(key)
}

func (carrier HTTPHeaderCarrier) Set(key, value string) {
	carrier.Header.Set(key, value)
}

func (carrier HTTPHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier.Header))
	for key := range carrier.Header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// GRPCMetadataCarrier adapts gRPC metadata to metadata.Carrier.
type GRPCMetadataCarrier struct{ Metadata grpcmetadata.MD }

func (carrier GRPCMetadataCarrier) Get(key string) string {
	return strings.Join(carrier.Metadata.Get(key), ",")
}

func (carrier GRPCMetadataCarrier) Set(key, value string) {
	carrier.Metadata.Set(key, value)
}

func (carrier GRPCMetadataCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier.Metadata))
	for key := range carrier.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
