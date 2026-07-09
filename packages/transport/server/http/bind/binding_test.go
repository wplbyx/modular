package bind

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type queryRequest struct {
	Name   string   `json:"name" binding:"required"`
	Age    int      `json:"age"`
	Tags   []string `json:"tag"`
	Active *bool    `json:"active"`
	Count  uint     `json:"count"`
	Ratio  float64  `json:"ratio"`
}

func TestBindQueryUsesJSONTags(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?name=device&age=7&tag=a&tag=b&active=true&count=3&ratio=1.5", nil)

	var req queryRequest
	if err := BindQuery(ctx, &req); err != nil {
		t.Fatalf("BindQuery() error = %v", err)
	}

	if req.Name != "device" || req.Age != 7 || req.Count != 3 || req.Ratio != 1.5 {
		t.Fatalf("BindQuery() = %+v", req)
	}
	if req.Active == nil || !*req.Active {
		t.Fatalf("BindQuery() active = %v", req.Active)
	}
	if len(req.Tags) != 2 || req.Tags[0] != "a" || req.Tags[1] != "b" {
		t.Fatalf("BindQuery() tags = %v", req.Tags)
	}
}

func TestBindQueryValidatesAfterBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?age=7", nil)

	var req queryRequest
	if err := BindQuery(ctx, &req); err == nil {
		t.Fatal("BindQuery() expected validation error")
	}
}

func TestBindQuerySupportsDurationAndTextUnmarshaler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type advancedQuery struct {
		Timeout time.Duration `json:"timeout"`
		When    time.Time     `json:"when"`
		Mode    queryMode     `json:"mode"`
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?timeout=5s&when=2026-07-09T10:11:12Z&mode=fast", nil)

	var req advancedQuery
	if err := BindQuery(ctx, &req); err != nil {
		t.Fatalf("BindQuery() error = %v", err)
	}
	if req.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v", req.Timeout)
	}
	if req.When.Format(time.RFC3339) != "2026-07-09T10:11:12Z" {
		t.Fatalf("When = %v", req.When)
	}
	if req.Mode != "mode:fast" {
		t.Fatalf("Mode = %q", req.Mode)
	}
}

func TestBindQueryUnsupportedTypeErrorIncludesFieldName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type unsupportedQuery struct {
		Filters map[string]string `json:"filters"`
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?filters=x", nil)

	var req unsupportedQuery
	err := BindQuery(ctx, &req)
	if err == nil || !strings.Contains(err.Error(), "Filters") {
		t.Fatalf("BindQuery() error = %v, want field name", err)
	}
}

type queryMode string

func (m *queryMode) UnmarshalText(text []byte) error {
	*m = queryMode("mode:" + string(text))
	return nil
}
