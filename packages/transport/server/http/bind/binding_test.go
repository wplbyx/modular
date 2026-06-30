package bind

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
