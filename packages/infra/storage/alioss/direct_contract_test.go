package alioss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/infra/storage"
)

func TestDirectStorageContractAndCallbackHandlerExist(t *testing.T) {
	var _ storage.DirectStorage = (*OssStorage)(nil)

	handler := NewCallbackHandler(func(context.Context, CallbackPayload) error {
		return nil
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/callbacks/oss", nil))

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
