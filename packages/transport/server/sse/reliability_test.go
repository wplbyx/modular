package sse

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestServer_EncodesMultilineAndRejectsClosedConnections(t *testing.T) {
	s := NewServer(4)
	engine := gin.New()
	engine.GET("/events", s.Connect())
	server := httptest.NewServer(engine)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(server.URL + "/events?client_id=one")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.True(t, s.Publish("one", Message{Data: "first\nsecond"}))
	scanner := bufio.NewScanner(resp.Body)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data = append(data, line)
		}
		if line == "" && len(data) > 1 {
			break
		}
	}
	require.Contains(t, data, "data: second")
	require.False(t, s.Publish("one", Message{Event: "bad\nevent", Data: "x"}))
	require.NoError(t, s.Shutdown(context.Background()))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest("GET", "/events?client_id=two", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
