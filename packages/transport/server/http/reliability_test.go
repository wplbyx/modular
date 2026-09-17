package http

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/resilience"
	"github.com/wplbyx/modular/packages/transport"
)

type recordingProtection struct{ results []error }

func (p *recordingProtection) Allow(context.Context, string) (resilience.DoneFunc, error) {
	return func(err error) { p.results = append(p.results, err) }, nil
}
func TestServer_ProtectionCompletesFailedRequests(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			protection := &recordingProtection{}
			srv, err := NewServer(nil, WithPolicy(transport.NewPolicy("test", transport.WithProtection(protection))))
			require.NoError(t, err)
			withStop(t, srv)
			srv.RegisterRoute(func(engine *gin.Engine) {
				engine.GET("/fail", Wrap(func(*gin.Context) error {
					if panics {
						panic("failed")
					}
					return errors.New("failed")
				}))
			})
			response := doRequest(t, srv, http.MethodGet, "/fail")
			require.Equal(t, 500, response.Code)
			require.Len(t, protection.results, 1)
			require.Error(t, protection.results[0])
		})
	}
}
